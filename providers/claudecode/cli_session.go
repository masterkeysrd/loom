package loomclaudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/providers/claudecode/internal/protocol"
	"github.com/masterkeysrd/loom/stream"
	"github.com/masterkeysrd/loom/tool"
)

type boundedBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newBoundedBuffer(maxBytes int) *boundedBuffer {
	return &boundedBuffer{max: maxBytes}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.max:]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.buf))
}

type readEvent struct {
	frame protocol.Frame
	err   error
}

type idleNotifyFn func(s *cliSession, newToken string, msg message.Message)

type cliSession struct {
	id                 string
	configDir          string
	allowedNativeTools []string
	tools              []tool.Definition
	shutdownTimeout    time.Duration
	onIdle             idleNotifyFn

	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex
	stderr  *boundedBuffer
	trace   io.Writer
	traceMu sync.Mutex

	turnMu   sync.Mutex
	activeCh chan readEvent
	doneCh   chan struct{}

	mu                  sync.Mutex
	expectedToken       string
	prefixDigest        string
	parkedCalls         []pendingMCPCall
	needsSync           bool
	idleExited          bool
	syncReason          string
	pendingSyncToken    string
	pendingSyncMessages message.MessageList
	pendingCompact      compactState
	idleSummaryText     string
	lastUsed            time.Time
	closed              bool
	reqSeq              atomic.Int64
}

func startCLISession(
	ctx context.Context,
	cliPath string,
	args []string,
	workingDir string,
	env []string,
	configDir string,
	tools []tool.Definition,
	allowedNativeTools []string,
	shutdownTimeout time.Duration,
	onIdle idleNotifyFn,
	traceWriter io.Writer,
) (*cliSession, error) {
	cmd := exec.Command(cliPath, args...)
	if workingDir != "" {
		cmd.Dir = workingDir
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("claudecode: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claudecode: stdout pipe: %w", err)
	}

	stderrBuf := newBoundedBuffer(16 * 1024)
	if traceWriter != nil {
		cmd.Stderr = io.MultiWriter(stderrBuf, &lineTraceWriter{w: traceWriter, prefix: "[stderr] "})
	} else {
		cmd.Stderr = stderrBuf
	}

	if traceWriter != nil {
		_, _ = fmt.Fprintf(traceWriter, "[spawn] %s %s\n", cliPath, strings.Join(args, " "))
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claudecode: start %q: %w", cliPath, err)
	}

	s := &cliSession{
		configDir:          configDir,
		allowedNativeTools: append([]string(nil), allowedNativeTools...),
		tools:              append([]tool.Definition(nil), tools...),
		shutdownTimeout:    shutdownTimeout,
		onIdle:             onIdle,
		cmd:                cmd,
		stdin:              stdin,
		stderr:             stderrBuf,
		trace:              traceWriter,
		doneCh:             make(chan struct{}),
		lastUsed:           time.Now(),
	}

	go s.readLoop(stdout)

	if err := s.initialize(ctx, len(tools) > 0); err != nil {
		_ = s.close()
		return nil, err
	}

	return s, nil
}

func (s *cliSession) beginTurn() chan readEvent {
	ch := make(chan readEvent, 256)
	s.turnMu.Lock()
	s.activeCh = ch
	s.turnMu.Unlock()
	return ch
}

func (s *cliSession) endTurn(ch chan readEvent) {
	s.turnMu.Lock()
	if s.activeCh == ch {
		s.activeCh = nil
	}
	s.turnMu.Unlock()
}

func (s *cliSession) currentActiveCh() chan readEvent {
	s.turnMu.Lock()
	ch := s.activeCh
	s.turnMu.Unlock()
	return ch
}

type lineTraceWriter struct {
	mu     sync.Mutex
	w      io.Writer
	prefix string
}

func (l *lineTraceWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.w, "%s%s", l.prefix, string(p))
	return len(p), nil
}

func (s *cliSession) traceLine(prefix string, line []byte) {
	if s.trace == nil {
		return
	}
	s.traceMu.Lock()
	defer s.traceMu.Unlock()
	_, _ = fmt.Fprintf(s.trace, "%s %s\n", prefix, strings.TrimSpace(string(line)))
}

func (s *cliSession) readLoop(stdout io.Reader) {
	defer close(s.doneCh)

	dispatch := func(ev readEvent) {
		if ch := s.currentActiveCh(); ch != nil {
			select {
			case ch <- ev:
			case <-s.doneCh:
			}
			return
		}
		s.handleIdleEvent(ev)
	}

	reader := bufio.NewReaderSize(stdout, 256*1024)
	for {
		line, err := reader.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			s.traceLine("[stdout]", trimmed)
			var frame protocol.Frame
			if uerr := json.Unmarshal(trimmed, &frame); uerr != nil {
				dispatch(readEvent{
					err: fmt.Errorf("claudecode: decode cli frame: %w (line=%q)", uerr, truncateForErr(string(trimmed), 200)),
				})
				return
			}
			dispatch(readEvent{frame: frame})
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				dispatch(readEvent{err: fmt.Errorf("claudecode: read stdout: %w", err)})
			} else if s.currentActiveCh() == nil {
				s.handleIdleExit()
			}
			return
		}
	}
}

func (s *cliSession) handleIdleExit() {
	if s.isClosed() {
		return
	}
	s.mu.Lock()
	s.needsSync = true
	s.idleExited = true
	s.syncReason = "session_exited"
	s.pendingSyncMessages = nil
	sid := s.id
	s.mu.Unlock()

	if s.onIdle != nil {
		msg := message.NewSystemText("Claude Code session exited while idle").
			WithExtension(&IdleEventExtension{
				Kind:      "exited",
				SessionID: sid,
			})
		s.onIdle(s, "", msg)
	}
}

func (s *cliSession) handleIdleEvent(ev readEvent) {
	if ev.err != nil {
		s.mu.Lock()
		s.needsSync = true
		s.idleExited = true
		s.syncReason = "session_error"
		sid := s.id
		s.mu.Unlock()
		if s.onIdle != nil && !s.isClosed() {
			msg := message.NewSystemText(ev.err.Error()).
				WithExtension(&IdleEventExtension{
					Kind:      "exited",
					SessionID: sid,
				})
			s.onIdle(s, "", msg)
		}
		return
	}

	f := ev.frame
	if f.SessionID != "" {
		s.mu.Lock()
		if s.id == "" {
			s.id = f.SessionID
		}
		s.mu.Unlock()
	}

	switch f.Type {
	case "control_request", "sdk_control_request":
		resp, _, cerr := handleIncomingControlRequest(f, s.tools, s.allowedNativeTools)
		if cerr == nil && resp != nil {
			_ = s.writeJSONLine(resp)
		}

	case "assistant":
		if f.Message != nil {
			var parts []string
			for _, cb := range f.Message.Content {
				if cb.Type == "text" && cb.Text != "" {
					parts = append(parts, cb.Text)
				}
			}
			if txt := strings.Join(parts, "\n"); txt != "" {
				s.mu.Lock()
				s.idleSummaryText = txt
				s.mu.Unlock()
			}
		}

	case "system":
		if f.Subtype == "status" && f.Status == "compacting" {
			if s.onIdle != nil {
				msg := message.NewSystemText("Claude Code is compacting conversation context").
					WithExtension(&IdleEventExtension{
						Kind:      "compacting",
						SessionID: s.id,
					})
				s.onIdle(s, "", msg)
			}
			return
		}

		if trigger, preTokens, ok := f.CompactInfo(); ok {
			s.mu.Lock()
			sid := s.id
			summary := strings.TrimSpace(s.idleSummaryText)
			s.idleSummaryText = ""
			if summary == "" {
				if strings.TrimSpace(f.Result) != "" {
					summary = strings.TrimSpace(f.Result)
				} else {
					summary = "Conversation compacted by Claude Code"
				}
			}
			syncToken := uuid.NewString()
			turnNonce := fmt.Sprintf("idle_%d", s.reqSeq.Add(1))
			newTurnToken := computeTurnToken(sid, turnNonce, "rebased:"+syncToken)

			meta := map[string]any{
				SessionIDKey:      sid,
				TurnTokenKey:      newTurnToken,
				CompactedKey:      true,
				CompactTriggerKey: trigger,
			}
			if preTokens > 0 {
				meta[CompactPreTokensKey] = preTokens
			}

			compactMsg := &message.Assistant{
				Base: message.Base{
					Metadata: meta,
				},
				Content: message.Content{
					&message.TextBlock{Text: summary},
				},
			}
			compactMsg.AddExtension(&SyncCheckpointExtension{
				SessionID: sid,
				TurnToken: newTurnToken,
				SyncToken: syncToken,
			})
			compactMsg.AddExtension(&IdleEventExtension{
				Kind:      "compacted",
				SessionID: sid,
				Trigger:   trigger,
				PreTokens: preTokens,
			})

			s.needsSync = true
			s.syncReason = "compacted"
			s.pendingSyncToken = syncToken
			s.pendingSyncMessages = message.MessageList{compactMsg}
			s.pendingCompact = compactState{
				compacted: true,
				trigger:   trigger,
				preTokens: preTokens,
			}
			s.expectedToken = newTurnToken
			s.mu.Unlock()

			if s.onIdle != nil {
				s.onIdle(s, newTurnToken, compactMsg)
			}
		}
	}
}

func (s *cliSession) writeJSONLine(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("claudecode: marshal frame: %w", err)
	}
	b = append(b, '\n')

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if s.isClosed() {
		return errors.New("claudecode: session closed")
	}
	s.traceLine("[stdin]", b)
	if _, err := s.stdin.Write(b); err != nil {
		return fmt.Errorf("claudecode: write stdin: %w", err)
	}
	return nil
}

func (s *cliSession) recvEvent(ctx context.Context, ch chan readEvent) (readEvent, bool, error) {
	select {
	case <-ctx.Done():
		return readEvent{}, false, ctx.Err()
	case ev := <-ch:
		return ev, true, nil
	case <-s.doneCh:
		select {
		case ev := <-ch:
			return ev, true, nil
		default:
			return readEvent{}, false, nil
		}
	}
}

func (s *cliSession) initialize(ctx context.Context, hasTools bool) error {
	ch := s.beginTurn()
	defer s.endTurn(ch)

	reqID := fmt.Sprintf("init_%d", s.reqSeq.Add(1))
	initFrame, err := buildInitializeControlRequest(reqID, hasTools)
	if err != nil {
		return err
	}
	if err := s.writeJSONLine(initFrame); err != nil {
		return err
	}

	for {
		ev, ok, rerr := s.recvEvent(ctx, ch)
		if rerr != nil {
			return rerr
		}
		if !ok {
			return s.waitExitError("claudecode: cli exited during initialize")
		}
		if ev.err != nil {
			return ev.err
		}
		f := ev.frame
		if f.SessionID != "" && s.id == "" {
			s.id = f.SessionID
		}
		switch f.Type {
		case "control_response":
			if f.Response != nil && f.Response.RequestID == reqID {
				if f.Response.Subtype == "error" {
					return fmt.Errorf("claudecode: initialize failed: %s", f.Response.Error)
				}
				return nil
			}
		case "control_request", "sdk_control_request":
			resp, _, cerr := handleIncomingControlRequest(f, s.tools, s.allowedNativeTools)
			if cerr != nil {
				return cerr
			}
			if resp != nil {
				if werr := s.writeJSONLine(resp); werr != nil {
					return werr
				}
			}
		case "system":
			// System init frames can arrive before or alongside control_response.
			if f.SessionID != "" {
				s.id = f.SessionID
			}
		}
	}
}

func (s *cliSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *cliSession) close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	_ = s.stdin.Close()

	select {
	case <-s.doneCh:
	case <-time.After(s.shutdownTimeout):
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-s.doneCh:
		case <-time.After(500 * time.Millisecond):
			if s.cmd.Process != nil {
				_ = s.cmd.Process.Kill()
			}
			<-s.doneCh
		}
	}

	_ = s.cmd.Wait()
	return nil
}

func (s *cliSession) waitExitError(prefix string) error {
	err := s.cmd.Wait()
	stderr := s.stderr.String()
	if stderr != "" {
		if err != nil {
			return fmt.Errorf("%s: %w (stderr: %s)", prefix, err, stderr)
		}
		return fmt.Errorf("%s (stderr: %s)", prefix, stderr)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	return errors.New(prefix)
}

func (s *cliSession) sendUserTurn(prompt string) error {
	return s.writeJSONLine(protocol.UserInput{
		Type:      "user",
		SessionID: s.id,
		Message: protocol.UserMessage{
			Role:    "user",
			Content: prompt,
		},
	})
}

func (s *cliSession) sendToolResults(toolMsgs []*message.Tool) error {
	s.mu.Lock()
	pending := slicesCopy(s.parkedCalls)
	s.parkedCalls = nil
	s.mu.Unlock()

	if len(pending) == 0 {
		return errors.New("claudecode: no parked MCP tool calls awaiting results")
	}

	used := make([]bool, len(pending))
	responses := make([]*protocol.ControlResponse, 0, len(pending))

	for _, tm := range toolMsgs {
		idx := matchPendingMCPCall(pending, used, tm)
		if idx == -1 {
			continue
		}
		used[idx] = true
		responses = append(responses, buildMCPToolResultControlResponse(pending[idx], tm))
	}

	// Fill any unmatched pending MCP calls with an empty/error response so the
	// CLI never deadlocks waiting for a missing control_response.
	for i, p := range pending {
		if !used[i] {
			responses = append(responses, buildMCPToolResultControlResponse(p, &message.Tool{
				ToolCallID: p.ToolCallID,
				Name:       p.ToolName,
				IsError:    true,
				Content:    message.Content{&message.TextBlock{Text: "missing tool result from caller"}},
			}))
		}
	}

	for _, resp := range responses {
		if err := s.writeJSONLine(resp); err != nil {
			return err
		}
	}
	return nil
}

func matchPendingMCPCall(pending []pendingMCPCall, used []bool, tm *message.Tool) int {
	if tm.ToolCallID != "" {
		for i, p := range pending {
			if !used[i] && p.ToolCallID == tm.ToolCallID {
				return i
			}
		}
	}
	if tm.Name != "" {
		norm := normalizeToolName(tm.Name)
		for i, p := range pending {
			if !used[i] && p.ToolName == norm {
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

type turnResult struct {
	sessionID    string
	turnToken    string
	prefixDigest string
	parked       bool
	errored      bool
}

type compactState struct {
	compacted bool
	trigger   string
	preTokens int
}

func formatCompactPrompt(instructions string) string {
	trimmed := strings.TrimSpace(instructions)
	if trimmed == "" {
		return "/compact"
	}
	return "/compact " + trimmed
}

func handleSystemCompactionFrame(ctx context.Context, sessionID string, f protocol.Frame, cs *compactState) {
	if f.Subtype == "status" && f.Status == "compacting" {
		if sw, ok := stream.WriterFromContext(ctx); ok {
			_ = sw.Write(ctx, stream.Event{
				Name: "on_llm_compacting",
				Data: map[string]any{
					"provider":   "claudecode",
					"session_id": sessionID,
				},
			})
		}
		return
	}
	if trigger, preTokens, ok := f.CompactInfo(); ok {
		cs.compacted = true
		if trigger != "" {
			cs.trigger = trigger
		}
		if preTokens > 0 {
			cs.preTokens = preTokens
		}
		if sw, ok := stream.WriterFromContext(ctx); ok {
			_ = sw.Write(ctx, stream.Event{
				Name: "on_llm_compacted",
				Data: map[string]any{
					"provider":   "claudecode",
					"session_id": sessionID,
					"trigger":    trigger,
					"pre_tokens": preTokens,
				},
			})
		}
	}
}

// runCompactTurn sends `/compact` to the active CLI session and drains stdout
// until the compaction turn completes, emitting stream compaction events.
func (s *cliSession) runCompactTurn(ctx context.Context, ch chan readEvent, instructions string) (compactState, error) {
	var cs compactState
	if err := s.sendUserTurn(formatCompactPrompt(instructions)); err != nil {
		return cs, err
	}

	for {
		ev, ok, rerr := s.recvEvent(ctx, ch)
		if rerr != nil {
			return cs, rerr
		}
		if !ok {
			return cs, s.waitExitError("claudecode: cli stream ended during /compact")
		}
		if ev.err != nil {
			return cs, ev.err
		}
		f := ev.frame
		if f.SessionID != "" {
			s.id = f.SessionID
		}
		switch f.Type {
		case "system":
			handleSystemCompactionFrame(ctx, s.id, f, &cs)
		case "control_request", "sdk_control_request":
			resp, _, cerr := handleIncomingControlRequest(f, s.tools, s.allowedNativeTools)
			if cerr != nil {
				return cs, cerr
			}
			if resp != nil {
				if werr := s.writeJSONLine(resp); werr != nil {
					return cs, werr
				}
			}
		case "result":
			if f.IsError {
				msg := f.Result
				if msg == "" && len(f.Errors) > 0 {
					msg = strings.Join(f.Errors, "; ")
				}
				if msg == "" {
					msg = fmt.Sprintf("claudecode: /compact failed (%s)", f.Subtype)
				}
				return cs, errors.New(msg)
			}
			cs.compacted = true
			if cs.trigger == "" {
				cs.trigger = "manual"
			}
			s.mu.Lock()
			s.lastUsed = time.Now()
			s.mu.Unlock()
			return cs, nil
		}
	}
}

// streamTurn reads CLI stdout frames for the current turn and yields Loom
// AssistantChunks until either the turn completes (`result`) or pauses for
// Loom-side tool execution (`mcp_message` `tools/call`).
func (s *cliSession) streamTurn(
	ctx context.Context,
	ch chan readEvent,
	req *llm.Request,
	systemPrompt string,
	priorConvMessages []message.Message,
	ext Session,
	initialCompact compactState,
	onDone func(turnResult),
	yield func(message.AssistantChunk, error) bool,
) {
	defer s.endTurn(ch)
	type streamedTool struct {
		index int
		id    string
		name  string
	}

	var (
		msgID             string
		resolvedModel     = req.Model
		stopReason        string
		streamedViaEvents bool
		emittedAnyText    bool
		expectedToolCalls int
		sawToolStop       bool
		streamedTools     []streamedTool
		parkedCalls       []pendingMCPCall
		turnNonce         = fmt.Sprintf("t%d", s.reqSeq.Add(1))
		agg               = message.NewAssistantAggregator()
		cs                = initialCompact
	)

	emitChunk := func(chunk message.AssistantChunk) bool {
		aggCopy := chunk
		agg.Add(&aggCopy)
		return yield(chunk, nil)
	}

	buildTurnMeta := func(sid string) (string, string, map[string]any) {
		if sid == "" {
			sid = s.id
		}
		prefixMsgs := slicesCopy(priorConvMessages)
		if built, err := agg.Build(); err == nil && built != nil {
			prefixMsgs = append(prefixMsgs, built)
		}
		digest := computePrefixDigest(req, systemPrompt, prefixMsgs, ext, s.allowedNativeTools)
		tok := computeTurnToken(sid, turnNonce, digest)
		meta := map[string]any{
			SessionIDKey:    sid,
			TurnTokenKey:    tok,
			PrefixDigestKey: digest,
		}
		if cs.compacted {
			meta[CompactedKey] = true
			if cs.trigger != "" {
				meta[CompactTriggerKey] = cs.trigger
			}
			if cs.preTokens > 0 {
				meta[CompactPreTokensKey] = cs.preTokens
			}
		}
		return tok, digest, meta
	}

	attachToolID := func(p *pendingMCPCall) {
		for _, st := range streamedTools {
			if st.name == p.ToolName {
				// Match first unused tool call ID with same name.
				alreadyUsed := false
				for _, prev := range parkedCalls {
					if prev.ToolCallID == st.id {
						alreadyUsed = true
						break
					}
				}
				if !alreadyUsed {
					p.ToolCallID = st.id
					return
				}
			}
		}
		if p.ToolCallID == "" {
			p.ToolCallID = fmt.Sprintf("call_%s_%d", turnNonce, len(parkedCalls)+1)
		}
	}

	for {
		ev, ok, rerr := s.recvEvent(ctx, ch)
		if rerr != nil {
			onDone(turnResult{errored: true})
			yield(message.AssistantChunk{}, rerr)
			return
		}
		if !ok {
			err := s.waitExitError("claudecode: cli stream ended unexpectedly")
			onDone(turnResult{errored: true})
			yield(message.AssistantChunk{}, err)
			return
		}
		if ev.err != nil {
			onDone(turnResult{errored: true})
			yield(message.AssistantChunk{}, ev.err)
			return
		}

		f := ev.frame
		if f.SessionID != "" {
			s.id = f.SessionID
		}

		switch f.Type {
		case "system":
			if f.Model != "" && resolvedModel == "" {
				resolvedModel = f.Model
			}
			handleSystemCompactionFrame(ctx, s.id, f, &cs)

		case "stream_event":
			if f.Event == nil {
				continue
			}
			se := f.Event
			switch se.Type {
			case "message_start":
				if se.Message != nil {
					if se.Message.ID != "" {
						msgID = se.Message.ID
					}
					if se.Message.Model != "" {
						resolvedModel = se.Message.Model
					}
				}

			case "content_block_start":
				if se.ContentBlock != nil && se.ContentBlock.Type == "tool_use" {
					streamedViaEvents = true
					expectedToolCalls++
					normName := normalizeToolName(se.ContentBlock.Name)
					callID := se.ContentBlock.ID
					if callID == "" {
						callID = fmt.Sprintf("call_%s_%d", turnNonce, expectedToolCalls)
					}
					streamedTools = append(streamedTools, streamedTool{
						index: se.Index,
						id:    callID,
						name:  normName,
					})
					if !emitChunk(message.AssistantChunk{
						BaseChunk: message.BaseChunk{ID: msgID},
						Model:     resolvedModel,
						Content: []message.Block{
							&message.ToolCallChunk{
								Index: se.Index,
								ID:    callID,
								Name:  normName,
							},
						},
					}) {
						onDone(turnResult{errored: true})
						return
					}
				}

			case "content_block_delta":
				if se.Delta == nil {
					continue
				}
				switch se.Delta.Type {
				case "text_delta":
					if se.Delta.Text != "" {
						streamedViaEvents = true
						emittedAnyText = true
						if !emitChunk(message.AssistantChunk{
							BaseChunk: message.BaseChunk{ID: msgID},
							Model:     resolvedModel,
							Content:   []message.Block{&message.TextBlock{Text: se.Delta.Text}},
						}) {
							onDone(turnResult{errored: true})
							return
						}
					}
				case "thinking_delta":
					if se.Delta.Thinking != "" {
						streamedViaEvents = true
						if !emitChunk(message.AssistantChunk{
							BaseChunk: message.BaseChunk{ID: msgID},
							Model:     resolvedModel,
							Content:   []message.Block{&message.ThinkingBlock{Thinking: se.Delta.Thinking}},
						}) {
							onDone(turnResult{errored: true})
							return
						}
					}
				case "input_json_delta":
					if se.Delta.PartialJSON != "" {
						streamedViaEvents = true
						if !emitChunk(message.AssistantChunk{
							BaseChunk: message.BaseChunk{ID: msgID},
							Model:     resolvedModel,
							Content: []message.Block{
								&message.ToolCallChunk{
									Index:     se.Index,
									ArgsChunk: se.Delta.PartialJSON,
								},
							},
						}) {
							onDone(turnResult{errored: true})
							return
						}
					}
				}

			case "message_delta":
				if se.Delta != nil && se.Delta.StopReason != "" {
					stopReason = se.Delta.StopReason
					if stopReason == "tool_use" {
						sawToolStop = true
					}
				}
			}

		case "assistant":
			if f.Message == nil {
				continue
			}
			if f.Message.ID != "" {
				msgID = f.Message.ID
			}
			if f.Message.Model != "" {
				resolvedModel = f.Message.Model
			}
			if f.Message.StopReason != "" {
				stopReason = f.Message.StopReason
				if stopReason == "tool_use" {
					sawToolStop = true
				}
			}

			if !streamedViaEvents {
				var blocks []message.Block
				for idx, cb := range f.Message.Content {
					switch cb.Type {
					case "text":
						if cb.Text != "" {
							emittedAnyText = true
							blocks = append(blocks, &message.TextBlock{Text: cb.Text})
						}
					case "thinking":
						if cb.Thinking != "" {
							blocks = append(blocks, &message.ThinkingBlock{Thinking: cb.Thinking})
						}
					case "tool_use":
						expectedToolCalls++
						normName := normalizeToolName(cb.Name)
						callID := cb.ID
						if callID == "" {
							callID = fmt.Sprintf("call_%s_%d", turnNonce, expectedToolCalls)
						}
						streamedTools = append(streamedTools, streamedTool{
							index: idx,
							id:    callID,
							name:  normName,
						})
						argsChunk := "{}"
						if len(cb.Input) > 0 {
							argsChunk = string(cb.Input)
						}
						blocks = append(blocks, &message.ToolCallChunk{
							Index:     idx,
							ID:        callID,
							Name:      normName,
							ArgsChunk: argsChunk,
						})
					}
				}
				if len(blocks) > 0 {
					if !emitChunk(message.AssistantChunk{
						BaseChunk: message.BaseChunk{ID: msgID},
						Model:     resolvedModel,
						Content:   blocks,
					}) {
						onDone(turnResult{errored: true})
						return
					}
				}
			}

		case "control_request", "sdk_control_request":
			resp, parked, cerr := handleIncomingControlRequest(f, s.tools, s.allowedNativeTools)
			if cerr != nil {
				onDone(turnResult{errored: true})
				yield(message.AssistantChunk{}, cerr)
				return
			}
			if resp != nil {
				if werr := s.writeJSONLine(resp); werr != nil {
					onDone(turnResult{errored: true})
					yield(message.AssistantChunk{}, werr)
					return
				}
			}
			if parked != nil {
				attachToolID(parked)
				// If no stream_event/assistant announced this tool call, emit it now.
				if len(streamedTools) == 0 {
					argsBytes, _ := json.Marshal(parked.Args)
					if !emitChunk(message.AssistantChunk{
						BaseChunk: message.BaseChunk{ID: msgID},
						Model:     resolvedModel,
						Content: []message.Block{
							&message.ToolCallChunk{
								Index:     len(parkedCalls),
								ID:        parked.ToolCallID,
								Name:      parked.ToolName,
								ArgsChunk: string(argsBytes),
							},
						},
					}) {
						onDone(turnResult{errored: true})
						return
					}
					expectedToolCalls++
					sawToolStop = true
				}
				parkedCalls = append(parkedCalls, *parked)
			}

		case "result":
			if f.IsError {
				msg := f.Result
				if msg == "" && len(f.Errors) > 0 {
					msg = strings.Join(f.Errors, "; ")
				}
				if msg == "" {
					msg = fmt.Sprintf("claudecode: cli returned error result (%s)", f.Subtype)
				}
				onDone(turnResult{errored: true})
				yield(message.AssistantChunk{}, errors.New(msg))
				return
			}

			if !emittedAnyText && f.Result != "" && len(parkedCalls) == 0 {
				if !emitChunk(message.AssistantChunk{
					BaseChunk: message.BaseChunk{ID: msgID},
					Model:     resolvedModel,
					Content:   []message.Block{&message.TextBlock{Text: f.Result}},
				}) {
					onDone(turnResult{errored: true})
					return
				}
			}

			reason := stopReason
			if reason == "" {
				reason = "end_turn"
			}
			tok, digest, meta := buildTurnMeta(f.SessionID)
			metrics := toTokenMetrics(f.Usage, f.DurationMS, f.TotalCostUSD)
			s.mu.Lock()
			s.expectedToken = tok
			s.prefixDigest = digest
			s.lastUsed = time.Now()
			s.mu.Unlock()

			if !yield(message.AssistantChunk{
				BaseChunk:  message.BaseChunk{ID: msgID},
				Model:      resolvedModel,
				Metadata:   meta,
				Metrics:    metrics,
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
				parked:       false,
			})
			return
		}

		// If the model paused for tool calls and all expected MCP tools/call
		// requests have arrived, yield Done=true ("tool_calls") and park.
		if sawToolStop && expectedToolCalls > 0 && len(parkedCalls) >= expectedToolCalls {
			tok, digest, meta := buildTurnMeta(s.id)
			s.mu.Lock()
			s.parkedCalls = parkedCalls
			s.expectedToken = tok
			s.prefixDigest = digest
			s.lastUsed = time.Now()
			s.mu.Unlock()

			if !yield(message.AssistantChunk{
				BaseChunk:  message.BaseChunk{ID: msgID},
				Model:      resolvedModel,
				Metadata:   meta,
				Done:       true,
				DoneReason: "tool_calls",
			}, nil) {
				onDone(turnResult{errored: true})
				return
			}

			onDone(turnResult{
				sessionID:    s.id,
				turnToken:    tok,
				prefixDigest: digest,
				parked:       true,
			})
			return
		}
	}
}

func truncateForErr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func slicesCopy[T any](in []T) []T {
	if len(in) == 0 {
		return nil
	}
	out := make([]T, len(in))
	copy(out, in)
	return out
}
