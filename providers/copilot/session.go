package loomcopilot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/stream"
	"github.com/masterkeysrd/loom/tool"
)

type pendingToolCall struct {
	ToolCallID string
	ToolName   string
	RequestID  string
	Args       any
	respCh     chan copilot.ToolResult
}

type turnItem struct {
	event  *copilot.SessionEvent
	parked *pendingToolCall
	closed bool
}

type turnQueue struct {
	mu       sync.Mutex
	items    []turnItem
	notifyCh chan struct{}
	closed   bool
}

func newTurnQueue() *turnQueue {
	return &turnQueue{
		notifyCh: make(chan struct{}, 1),
	}
}

func (q *turnQueue) push(item turnItem) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.items = append(q.items, item)
	q.mu.Unlock()
	select {
	case q.notifyCh <- struct{}{}:
	default:
	}
}

func (q *turnQueue) close() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	q.items = append(q.items, turnItem{closed: true})
	q.mu.Unlock()
	select {
	case q.notifyCh <- struct{}{}:
	default:
	}
}

func (q *turnQueue) pop(ctx context.Context) (turnItem, error) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			item := q.items[0]
			q.items = q.items[1:]
			q.mu.Unlock()
			return item, nil
		}
		q.mu.Unlock()

		select {
		case <-ctx.Done():
			return turnItem{}, ctx.Err()
		case <-q.notifyCh:
		}
	}
}

func (q *turnQueue) tryPop() (turnItem, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return turnItem{}, false
	}
	item := q.items[0]
	q.items = q.items[1:]
	return item, true
}

type idleNotifyFn func(s *copilotSession, newToken string, msg message.Message)

type turnResult struct {
	sessionID    string
	turnToken    string
	prefixDigest string
	parked       bool
	errored      bool
}

type compactState struct {
	compacted       bool
	trigger         string
	tokensRemoved   int64
	messagesRemoved int64
	summary         string
	streamEmitted   bool
}

type copilotSession struct {
	id                 string
	model              string
	allowedNativeTools []string
	customTools        map[string]bool
	tools              []tool.Definition
	onIdle             idleNotifyFn
	trace              io.Writer
	traceMu            sync.Mutex

	sdkSession *copilot.Session
	unsub      func()

	turnMu      sync.Mutex
	activeQueue *turnQueue

	mu                  sync.Mutex
	expectedToken       string
	prefixDigest        string
	parkedCalls         []*pendingToolCall
	needsSync           bool
	idleExited          bool
	syncReason          string
	pendingSyncToken    string
	pendingSyncMessages message.MessageList
	pendingCompact      compactState
	lastUsed            time.Time
	closed              bool
	reqSeq              atomic.Int64
}

func newCopilotSessionWrapper(
	model string,
	tools []tool.Definition,
	allowedNative []string,
	onIdle idleNotifyFn,
	traceWriter io.Writer,
) *copilotSession {
	customMap := make(map[string]bool, len(tools))
	for _, t := range tools {
		customMap[t.Name] = true
	}
	return &copilotSession{
		model:              model,
		allowedNativeTools: append([]string(nil), allowedNative...),
		customTools:        customMap,
		tools:              append([]tool.Definition(nil), tools...),
		onIdle:             onIdle,
		trace:              traceWriter,
		lastUsed:           time.Now(),
	}
}

func (s *copilotSession) attach(sdkSess *copilot.Session) {
	s.sdkSession = sdkSess
	s.id = sdkSess.SessionID
	s.unsub = sdkSess.On(s.handleSDKEvent)
}

func (s *copilotSession) tracef(format string, args ...any) {
	if s.trace == nil {
		return
	}
	s.traceMu.Lock()
	defer s.traceMu.Unlock()
	_, _ = fmt.Fprintf(s.trace, format+"\n", args...)
}

func (s *copilotSession) beginTurn() *turnQueue {
	q := newTurnQueue()
	s.turnMu.Lock()
	s.activeQueue = q
	s.turnMu.Unlock()
	return q
}

func (s *copilotSession) endTurn(q *turnQueue) {
	s.turnMu.Lock()
	if s.activeQueue == q {
		s.activeQueue = nil
	}
	s.turnMu.Unlock()
	if q != nil {
		q.close()
	}
}

func (s *copilotSession) currentQueue() *turnQueue {
	s.turnMu.Lock()
	q := s.activeQueue
	s.turnMu.Unlock()
	return q
}

func (s *copilotSession) hasParkedCalls() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.parkedCalls) > 0
}

func (s *copilotSession) close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	parked := s.parkedCalls
	s.parkedCalls = nil
	s.mu.Unlock()

	for _, p := range parked {
		select {
		case p.respCh <- copilot.ToolResult{
			TextResultForLLM: "session closed",
			ResultType:       "failure",
			Error:            "session closed",
		}:
		default:
		}
	}

	if q := s.currentQueue(); q != nil {
		q.close()
	}
	if s.unsub != nil {
		s.unsub()
		s.unsub = nil
	}
	if s.sdkSession != nil {
		return s.sdkSession.Disconnect()
	}
	return nil
}

// makeToolHandler returns a copilot.ToolHandler that parks external tool calls
// when a Loom turn is active so Loom's Tools node executes them cleanly.
func (s *copilotSession) makeToolHandler() copilot.ToolHandler {
	return func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
		s.tracef("[tool.park] session=%s id=%s name=%s", s.id, inv.ToolCallID, inv.ToolName)
		q := s.currentQueue()
		if q == nil {
			return copilot.ToolResult{
				TextResultForLLM: "no active turn to receive tool call",
				ResultType:       "failure",
				Error:            "no active turn to receive tool call",
			}, nil
		}

		pending := &pendingToolCall{
			ToolCallID: inv.ToolCallID,
			ToolName:   inv.ToolName,
			RequestID:  inv.RequestID,
			Args:       inv.Arguments,
			respCh:     make(chan copilot.ToolResult, 1),
		}
		q.push(turnItem{parked: pending})

		toolCtx := inv.TraceContext
		if toolCtx == nil {
			toolCtx = context.Background()
		}

		select {
		case res := <-pending.respCh:
			s.tracef("[tool.resume] session=%s id=%s name=%s type=%s", s.id, inv.ToolCallID, inv.ToolName, res.ResultType)
			return res, nil
		case <-toolCtx.Done():
			return copilot.ToolResult{}, toolCtx.Err()
		}
	}
}

func (s *copilotSession) sendToolResults(toolMsgs []*message.Tool, prepared []copilot.ToolResult) error {
	s.mu.Lock()
	pending := slices.Clone(s.parkedCalls)
	s.parkedCalls = nil
	s.mu.Unlock()

	if len(pending) == 0 {
		return fmt.Errorf("copilot: no parked tool calls awaiting results")
	}

	used := make([]bool, len(pending))
	deliveries := make([]copilot.ToolResult, len(pending))
	delivered := make([]bool, len(pending))

	for i, tm := range toolMsgs {
		idx := matchPendingToolCall(pending, used, tm)
		if idx < 0 {
			continue
		}
		used[idx] = true
		delivered[idx] = true
		if i < len(prepared) {
			deliveries[idx] = prepared[i]
		} else {
			res, err := toToolResult(tm, s.model)
			if err != nil {
				return err
			}
			deliveries[idx] = res
		}
	}

	for i := range pending {
		if !delivered[i] {
			fallback, _ := toToolResult(nil, s.model)
			deliveries[i] = fallback
		}
		select {
		case pending[i].respCh <- deliveries[i]:
		default:
		}
	}
	return nil
}

func matchPendingToolCall(pending []*pendingToolCall, used []bool, tm *message.Tool) int {
	if tm.ToolCallID != "" {
		for i, p := range pending {
			if !used[i] && p.ToolCallID == tm.ToolCallID {
				return i
			}
		}
	}
	if tm.Name != "" {
		for i, p := range pending {
			if !used[i] && p.ToolName == tm.Name {
				return i
			}
		}
	}
	for i := range pending {
		if !used[i] {
			return i
		}
	}
	return -1
}

func (s *copilotSession) handleSDKEvent(event copilot.SessionEvent) {
	s.tracef("[event] session=%s type=%s id=%s", s.id, event.Type, event.ID)
	q := s.currentQueue()
	if q != nil {
		evCopy := event
		q.push(turnItem{event: &evCopy})
		return
	}
	s.handleIdleEvent(event)
}

func (s *copilotSession) handleIdleEvent(event copilot.SessionEvent) {
	if s.onIdle == nil {
		return
	}

	switch d := event.Data.(type) {
	case *copilot.SessionCompactionStartData:
		msg := message.NewSystem(&message.TextBlock{Text: "Copilot session is compacting context while idle."})
		msg.Metadata = map[string]any{
			SessionIDKey: s.id,
		}
		msg.Extensions = map[string]message.Extension{
			IdleEventExtensionID: &IdleEventExtension{
				Kind:      "compacting",
				SessionID: s.id,
			},
		}
		s.onIdle(s, "", msg)

	case *copilot.SessionCompactionCompleteData:
		if !d.Success {
			return
		}
		var tokensRemoved, msgsRemoved int64
		if d.TokensRemoved != nil {
			tokensRemoved = *d.TokensRemoved
		}
		if d.MessagesRemoved != nil {
			msgsRemoved = *d.MessagesRemoved
		}
		summary := ""
		if d.SummaryContent != nil {
			summary = strings.TrimSpace(*d.SummaryContent)
		}
		if summary == "" {
			summary = fmt.Sprintf("Copilot session %s compacted while idle (%d messages, %d tokens removed).", s.id, msgsRemoved, tokensRemoved)
		}

		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		digest := s.prefixDigest
		nonce := fmt.Sprintf("idle%d", s.reqSeq.Add(1))
		newTurnTok := computeTurnToken(s.id, nonce, digest)
		syncTok := fmt.Sprintf("sync_%s_%s", s.id, nonce)

		syncMsg := &message.Assistant{
			Base: message.Base{
				ID: "copilot-sync-" + syncTok,
				Metadata: map[string]any{
					SessionIDKey:              s.id,
					TurnTokenKey:              newTurnTok,
					PrefixDigestKey:           digest,
					CompactedKey:              true,
					CompactTriggerKey:         "auto",
					CompactTokensRemovedKey:   tokensRemoved,
					CompactMessagesRemovedKey: msgsRemoved,
				},
				Extensions: map[string]message.Extension{
					IdleEventExtensionID: &IdleEventExtension{
						Kind:            "compacted",
						SessionID:       s.id,
						Trigger:         "auto",
						TokensRemoved:   tokensRemoved,
						MessagesRemoved: msgsRemoved,
					},
					SyncCheckpointExtensionID: &SyncCheckpointExtension{
						SessionID: s.id,
						TurnToken: newTurnTok,
						SyncToken: syncTok,
					},
				},
			},
			Content:    message.Content{&message.TextBlock{Text: summary}},
			Done:       true,
			DoneReason: "compacted",
		}

		s.needsSync = true
		s.syncReason = "background_compaction"
		s.pendingSyncToken = syncTok
		s.pendingSyncMessages = message.MessageList{syncMsg}
		s.pendingCompact = compactState{
			compacted:       true,
			trigger:         "auto",
			tokensRemoved:   tokensRemoved,
			messagesRemoved: msgsRemoved,
			summary:         summary,
		}
		s.expectedToken = newTurnTok
		s.mu.Unlock()

		s.onIdle(s, newTurnTok, syncMsg)

	case *copilot.SessionTruncationData:
		summary := fmt.Sprintf("Copilot session %s truncated context while idle (%d messages, %d tokens removed).",
			s.id, d.MessagesRemovedDuringTruncation, d.TokensRemovedDuringTruncation)

		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		digest := s.prefixDigest
		nonce := fmt.Sprintf("idle%d", s.reqSeq.Add(1))
		newTurnTok := computeTurnToken(s.id, nonce, digest)
		syncTok := fmt.Sprintf("sync_%s_%s", s.id, nonce)

		syncMsg := &message.Assistant{
			Base: message.Base{
				ID: "copilot-sync-" + syncTok,
				Metadata: map[string]any{
					SessionIDKey:    s.id,
					TurnTokenKey:    newTurnTok,
					PrefixDigestKey: digest,
				},
				Extensions: map[string]message.Extension{
					IdleEventExtensionID: &IdleEventExtension{
						Kind:            "truncated",
						SessionID:       s.id,
						TokensRemoved:   d.TokensRemovedDuringTruncation,
						MessagesRemoved: d.MessagesRemovedDuringTruncation,
					},
					SyncCheckpointExtensionID: &SyncCheckpointExtension{
						SessionID: s.id,
						TurnToken: newTurnTok,
						SyncToken: syncTok,
					},
				},
			},
			Content:    message.Content{&message.TextBlock{Text: summary}},
			Done:       true,
			DoneReason: "truncated",
		}

		s.needsSync = true
		s.syncReason = "background_truncation"
		s.pendingSyncToken = syncTok
		s.pendingSyncMessages = message.MessageList{syncMsg}
		s.expectedToken = newTurnTok
		s.mu.Unlock()

		s.onIdle(s, newTurnTok, syncMsg)

	case *copilot.SessionShutdownData, *copilot.SessionErrorData:
		errText := "Copilot session terminated while idle."
		kind := "exited"
		if se, ok := d.(*copilot.SessionErrorData); ok {
			kind = "error"
			if se.Message != "" {
				errText = fmt.Sprintf("Copilot session error while idle: %s", se.Message)
			}
		}

		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		s.needsSync = true
		s.idleExited = true
		s.syncReason = "session_" + kind
		s.mu.Unlock()

		msg := message.NewSystem(&message.TextBlock{Text: errText})
		msg.Metadata = map[string]any{
			SessionIDKey: s.id,
		}
		msg.Extensions = map[string]message.Extension{
			IdleEventExtensionID: &IdleEventExtension{
				Kind:      kind,
				SessionID: s.id,
				Message:   errText,
			},
		}
		s.onIdle(s, "", msg)
	}
}

func (s *copilotSession) runCompactTurn(ctx context.Context, instructions string) (compactState, error) {
	var cs compactState
	if s.sdkSession == nil || s.sdkSession.RPC == nil || s.sdkSession.RPC.History == nil {
		return cs, fmt.Errorf("copilot: session RPC unavailable for compaction")
	}

	if sw, ok := stream.WriterFromContext(ctx); ok {
		_ = sw.Write(ctx, stream.Event{
			Name: "on_llm_compacting",
			Data: map[string]any{
				"provider":   "copilot",
				"session_id": s.id,
			},
		})
	}

	var reqParams *rpc.SessionHistoryCompactRequest
	if trimmed := strings.TrimSpace(instructions); trimmed != "" {
		reqParams = &rpc.SessionHistoryCompactRequest{
			CustomInstructions: &trimmed,
		}
	}
	res, err := s.sdkSession.RPC.History.Compact(ctx, reqParams)
	if err != nil {
		return cs, fmt.Errorf("copilot: compact failed: %w", err)
	}
	if res == nil || !res.Success {
		return cs, fmt.Errorf("copilot: compact unsuccessful")
	}

	cs = compactState{
		compacted:       true,
		trigger:         "manual",
		tokensRemoved:   res.TokensRemoved,
		messagesRemoved: res.MessagesRemoved,
		streamEmitted:   true,
	}
	if res.SummaryContent != nil {
		cs.summary = *res.SummaryContent
	}

	if sw, ok := stream.WriterFromContext(ctx); ok {
		_ = sw.Write(ctx, stream.Event{
			Name: "on_llm_compacted",
			Data: map[string]any{
				"provider":         "copilot",
				"session_id":       s.id,
				"trigger":          cs.trigger,
				"tokens_removed":   cs.tokensRemoved,
				"messages_removed": cs.messagesRemoved,
			},
		})
	}

	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()

	return cs, nil
}

func (s *copilotSession) streamTurn(
	ctx context.Context,
	q *turnQueue,
	req *llm.Request,
	systemPrompt string,
	priorConvMessages []message.Message,
	ext Session,
	workingDir string,
	initialCompact compactState,
	sendFn func() (compactState, error),
	onDone func(turnResult),
	yield func(message.AssistantChunk, error) bool,
) {
	defer s.endTurn(q)

	var (
		msgID                 string
		resolvedModel         = req.Model
		turnNonce             = fmt.Sprintf("t%d", s.reqSeq.Add(1))
		agg                   = message.NewAssistantAggregator()
		cs                    = initialCompact
		latestMetrics         *message.TokenMetrics
		emittedTextByMsg      = make(map[string]bool)
		emittedReasoning      bool
		streamedToolIDs       = make(map[string]int)
		expectedCustomTools   int
		sawAuthoritativeMsg   bool
		parkedCalls           []*pendingToolCall
		finishedWithToolCalls bool
	)

	if sendFn != nil {
		updatedCS, err := sendFn()
		if err != nil {
			onDone(turnResult{errored: true})
			yield(message.AssistantChunk{}, err)
			return
		}
		if updatedCS.compacted {
			cs = updatedCS
		}
	}

	emitChunk := func(chunk message.AssistantChunk) bool {
		aggCopy := chunk
		agg.Add(&aggCopy)
		return yield(chunk, nil)
	}

	buildTurnMeta := func() (string, string, map[string]any) {
		prefixMsgs := slices.Clone(priorConvMessages)
		if built, err := agg.Build(); err == nil && built != nil {
			prefixMsgs = append(prefixMsgs, built)
		}
		digest := computePrefixDigest(req, systemPrompt, prefixMsgs, ext, s.allowedNativeTools, workingDir)
		tok := computeTurnToken(s.id, turnNonce, digest)
		meta := map[string]any{
			SessionIDKey:    s.id,
			TurnTokenKey:    tok,
			PrefixDigestKey: digest,
		}
		if msgID != "" {
			meta[EventIDKey] = msgID
		}
		if cs.compacted {
			meta[CompactedKey] = true
			if cs.trigger != "" {
				meta[CompactTriggerKey] = cs.trigger
			}
			if cs.tokensRemoved > 0 {
				meta[CompactTokensRemovedKey] = cs.tokensRemoved
			}
			if cs.messagesRemoved > 0 {
				meta[CompactMessagesRemovedKey] = cs.messagesRemoved
			}
		}
		return tok, digest, meta
	}

	emitCompactionStart := func() {
		if cs.streamEmitted {
			return
		}
		if sw, ok := stream.WriterFromContext(ctx); ok {
			_ = sw.Write(ctx, stream.Event{
				Name: "on_llm_compacting",
				Data: map[string]any{
					"provider":   "copilot",
					"session_id": s.id,
				},
			})
		}
	}

	emitCompactionComplete := func(d *copilot.SessionCompactionCompleteData) {
		if d == nil || !d.Success {
			return
		}
		alreadyEmitted := cs.streamEmitted
		cs.compacted = true
		if cs.trigger == "" {
			cs.trigger = "auto"
		}
		if d.TokensRemoved != nil {
			cs.tokensRemoved = *d.TokensRemoved
		}
		if d.MessagesRemoved != nil {
			cs.messagesRemoved = *d.MessagesRemoved
		}
		if alreadyEmitted {
			return
		}
		if sw, ok := stream.WriterFromContext(ctx); ok {
			_ = sw.Write(ctx, stream.Event{
				Name: "on_llm_compacted",
				Data: map[string]any{
					"provider":         "copilot",
					"session_id":       s.id,
					"trigger":          cs.trigger,
					"tokens_removed":   cs.tokensRemoved,
					"messages_removed": cs.messagesRemoved,
				},
			})
		}
	}

	processItem := func(item turnItem) (done bool, failed bool) {
		if item.closed {
			onDone(turnResult{errored: true})
			yield(message.AssistantChunk{}, errors.New("copilot: session closed before turn completed"))
			return true, true
		}

		if item.parked != nil {
			parkedCalls = append(parkedCalls, item.parked)
			// If deltas weren't emitted (or no assistant.message arrived yet),
			// keep track; assistant.message handles authoritative emission or fallback below.
			return false, false
		}

		ev := item.event
		if ev == nil {
			return false, false
		}
		// Ignore subagent streaming/assistant events on the root turn stream.
		if ev.AgentID != nil && *ev.AgentID != "" {
			return false, false
		}

		switch d := ev.Data.(type) {
		case *copilot.AssistantReasoningDeltaData:
			if d.DeltaContent != "" {
				emittedReasoning = true
				if !emitChunk(message.AssistantChunk{
					BaseChunk: message.BaseChunk{ID: msgID},
					Model:     resolvedModel,
					Content:   []message.Block{&message.ThinkingBlock{Thinking: d.DeltaContent}},
				}) {
					onDone(turnResult{errored: true})
					return true, true
				}
			}

		case *copilot.AssistantReasoningData:
			if !emittedReasoning && d.Content != "" {
				emittedReasoning = true
				if d.Model != nil && *d.Model != "" {
					resolvedModel = *d.Model
				}
				if !emitChunk(message.AssistantChunk{
					BaseChunk: message.BaseChunk{ID: msgID},
					Model:     resolvedModel,
					Content:   []message.Block{&message.ThinkingBlock{Thinking: d.Content}},
				}) {
					onDone(turnResult{errored: true})
					return true, true
				}
			}

		case *copilot.AssistantMessageDeltaData:
			if d.MessageID != "" {
				msgID = d.MessageID
			}
			if d.DeltaContent != "" {
				emittedTextByMsg[d.MessageID] = true
				if !emitChunk(message.AssistantChunk{
					BaseChunk: message.BaseChunk{ID: msgID},
					Model:     resolvedModel,
					Content:   []message.Block{&message.TextBlock{Text: d.DeltaContent}},
				}) {
					onDone(turnResult{errored: true})
					return true, true
				}
			}

		case *copilot.AssistantToolCallDeltaData:
			// Only emit tool call deltas eagerly when no native tools can interleave.
			if len(s.allowedNativeTools) == 0 && d.ToolCallID != "" {
				idx, ok := streamedToolIDs[d.ToolCallID]
				var name string
				var id string
				if !ok {
					idx = len(streamedToolIDs)
					streamedToolIDs[d.ToolCallID] = idx
					id = d.ToolCallID
					if d.ToolName != nil {
						name = *d.ToolName
					}
				}
				if !emitChunk(message.AssistantChunk{
					BaseChunk: message.BaseChunk{ID: msgID},
					Model:     resolvedModel,
					Content: []message.Block{
						&message.ToolCallChunk{
							Index:     idx,
							ID:        id,
							Name:      name,
							ArgsChunk: d.InputDelta,
						},
					},
				}) {
					onDone(turnResult{errored: true})
					return true, true
				}
			}

		case *copilot.AssistantMessageData:
			sawAuthoritativeMsg = true
			if d.MessageID != "" {
				msgID = d.MessageID
			} else if ev.ID != "" {
				msgID = ev.ID
			}
			if d.Model != nil && *d.Model != "" {
				resolvedModel = *d.Model
			}

			var blocks []message.Block
			if !emittedReasoning && d.ReasoningText != nil && *d.ReasoningText != "" {
				emittedReasoning = true
				blocks = append(blocks, &message.ThinkingBlock{Thinking: *d.ReasoningText})
			}
			if !emittedTextByMsg[d.MessageID] && d.Content != "" {
				emittedTextByMsg[d.MessageID] = true
				blocks = append(blocks, &message.TextBlock{Text: d.Content})
			}

			customIdx := 0
			for _, tr := range d.ToolRequests {
				if !s.customTools[tr.Name] {
					continue
				}
				expectedCustomTools++
				if _, alreadyStreamed := streamedToolIDs[tr.ToolCallID]; !alreadyStreamed {
					blocks = append(blocks, &message.ToolCall{
						ID:    tr.ToolCallID,
						Name:  tr.Name,
						Index: customIdx,
						Args:  toArgsMap(tr.Arguments),
					})
				}
				customIdx++
			}

			if len(blocks) > 0 {
				if !emitChunk(message.AssistantChunk{
					BaseChunk: message.BaseChunk{ID: msgID},
					Model:     resolvedModel,
					Content:   blocks,
				}) {
					onDone(turnResult{errored: true})
					return true, true
				}
			}

		case *copilot.AssistantUsageData:
			if d.Model != "" {
				resolvedModel = d.Model
			}
			m := toTokenMetrics(d)
			if m != nil {
				if latestMetrics == nil {
					latestMetrics = m
				} else {
					sum := latestMetrics.Add(*m)
					// Input is prompt size for the latest call rather than cumulative across hops.
					sum.Tokens.Input = m.Tokens.Input
					sum.TotalTokens = m.Tokens.Input + sum.Tokens.Output
					latestMetrics = &sum
				}
			}

		case *copilot.SessionCompactionStartData:
			emitCompactionStart()

		case *copilot.SessionCompactionCompleteData:
			emitCompactionComplete(d)

		case *copilot.SessionErrorData:
			errMsg := d.Message
			if errMsg == "" {
				errMsg = d.ErrorType
			}
			onDone(turnResult{errored: true})
			yield(message.AssistantChunk{}, fmt.Errorf("copilot: %s", errMsg))
			return true, true

		case *copilot.AbortData:
			onDone(turnResult{errored: true})
			yield(message.AssistantChunk{}, fmt.Errorf("copilot: turn aborted (%s)", d.Reason))
			return true, true

		case *copilot.SessionIdleData:
			if d.Aborted != nil && *d.Aborted {
				onDone(turnResult{errored: true})
				yield(message.AssistantChunk{}, errors.New("copilot: turn aborted"))
				return true, true
			}
			return true, false
		}

		return false, false
	}

	for {
		item, err := q.pop(ctx)
		if err != nil {
			if s.sdkSession != nil {
				_ = s.sdkSession.Abort(context.Background())
			}
			onDone(turnResult{errored: true})
			yield(message.AssistantChunk{}, err)
			return
		}

		done, failed := processItem(item)
		if failed {
			return
		}
		if done {
			break
		}

		// If all requested Loom custom tools are now parked, drain any already
		// queued follow-up events (e.g. assistant.usage) and return control
		// to Loom's Tools node.
		if sawAuthoritativeMsg && expectedCustomTools > 0 && len(parkedCalls) >= expectedCustomTools {
			for {
				extra, ok := q.tryPop()
				if !ok {
					break
				}
				d2, f2 := processItem(extra)
				if f2 {
					return
				}
				if d2 {
					break
				}
			}
			finishedWithToolCalls = true
			break
		}
	}

	// Fallback: if external tools parked without a preceding assistant.message
	// event, emit ToolCall blocks directly from parked calls so Loom stays consistent.
	if !sawAuthoritativeMsg && len(parkedCalls) > 0 {
		finishedWithToolCalls = true
		var fallbackBlocks []message.Block
		for i, p := range parkedCalls {
			if _, ok := streamedToolIDs[p.ToolCallID]; !ok {
				fallbackBlocks = append(fallbackBlocks, &message.ToolCall{
					ID:    p.ToolCallID,
					Name:  p.ToolName,
					Index: i,
					Args:  toArgsMap(p.Args),
				})
			}
		}
		if len(fallbackBlocks) > 0 {
			if !emitChunk(message.AssistantChunk{
				BaseChunk: message.BaseChunk{ID: msgID},
				Model:     resolvedModel,
				Content:   fallbackBlocks,
			}) {
				onDone(turnResult{errored: true})
				return
			}
		}
	}

	reason := "end_turn"
	if finishedWithToolCalls {
		reason = "tool_calls"
	}

	tok, digest, meta := buildTurnMeta()
	s.mu.Lock()
	s.expectedToken = tok
	s.prefixDigest = digest
	if finishedWithToolCalls {
		s.parkedCalls = append([]*pendingToolCall(nil), parkedCalls...)
	} else {
		s.parkedCalls = nil
	}
	s.lastUsed = time.Now()
	s.mu.Unlock()

	if !yield(message.AssistantChunk{
		BaseChunk:  message.BaseChunk{ID: msgID},
		Model:      resolvedModel,
		Metadata:   meta,
		Metrics:    latestMetrics,
		Done:       true,
		DoneReason: reason,
	}, nil) {
		onDone(turnResult{errored: true})
		return
	}

	onDone(turnResult{
		sessionID:    s.id,
		turnToken:    tok,
		prefixDigest: digest,
		parked:       finishedWithToolCalls,
	})
}
