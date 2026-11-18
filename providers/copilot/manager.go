package loomcopilot

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
)

type sessionManager struct {
	cfg Config

	clientMu sync.Mutex
	client   *copilot.Client

	mu      sync.Mutex
	closed  bool
	byToken map[string]*copilotSession
	allSess map[*copilotSession]struct{}

	idleMu        sync.Mutex
	idleListeners map[int64]llm.IdleMessageHandler
	nextIdleID    int64

	stopReap chan struct{}
	reapDone chan struct{}
}

func newSessionManager(cfg Config) (*sessionManager, error) {
	m := &sessionManager{
		cfg:           cfg,
		byToken:       make(map[string]*copilotSession),
		allSess:       make(map[*copilotSession]struct{}),
		idleListeners: make(map[int64]llm.IdleMessageHandler),
		stopReap:      make(chan struct{}),
		reapDone:      make(chan struct{}),
	}

	if cfg.OnIdleMessage != nil {
		m.addIdleListener(cfg.OnIdleMessage)
	}

	if cfg.IdleTTL > 0 {
		go m.reapLoop(cfg.IdleTTL)
	} else {
		close(m.reapDone)
	}

	return m, nil
}

func (m *sessionManager) ensureClient(ctx context.Context) (*copilot.Client, error) {
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("copilot: provider is closed")
	}

	m.clientMu.Lock()
	defer m.clientMu.Unlock()

	if m.client != nil {
		return m.client, nil
	}

	conn := m.cfg.Connection
	if conn == nil {
		conn = copilot.StdioConnection{
			Path: m.cfg.CLIPath,
			Args: m.cfg.CLIArgs,
		}
	}

	var env []string
	if len(m.cfg.Env) > 0 {
		env = append(os.Environ(), m.cfg.Env...)
	}

	clientOpts := &copilot.ClientOptions{
		Connection:       conn,
		WorkingDirectory: m.cfg.WorkingDir,
		BaseDirectory:    strings.TrimSpace(m.cfg.BaseDirectory),
		LogLevel:         m.cfg.LogLevel,
		Env:              env,
		GitHubToken:      m.cfg.GitHubToken,
		UseLoggedInUser:  m.cfg.UseLoggedInUser,
		OnListModels:     m.cfg.OnListModels,
		ClientInfo:       m.cfg.ClientInfo,
		Mode:             m.cfg.ClientMode,
	}
	if _, ok := conn.(copilot.URIConnection); ok {
		clientOpts.BaseDirectory = ""
	}
	if _, ok := conn.(copilot.InProcessConnection); ok {
		// InProcessConnection rejects WorkingDirectory and Env on ClientOptions.
		clientOpts.WorkingDirectory = ""
		clientOpts.Env = nil
	}

	client := copilot.NewClient(clientOpts)
	if err := client.Start(ctx); err != nil {
		return nil, fmt.Errorf("copilot: start sdk client: %w", err)
	}

	m.client = client
	return client, nil
}

func (m *sessionManager) listModels(ctx context.Context) ([]copilot.ModelInfo, error) {
	client, err := m.ensureClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.ListModels(ctx)
}

func (m *sessionManager) getQuotas(ctx context.Context) (*rpc.AccountGetQuotaResult, error) {
	client, err := m.ensureClient(ctx)
	if err != nil {
		return nil, err
	}
	if client.RPC == nil || client.RPC.Account == nil {
		return nil, fmt.Errorf("copilot: server account RPC is unavailable")
	}
	return client.RPC.Account.GetQuota(ctx, nil)
}

func (m *sessionManager) reapLoop(ttl time.Duration) {
	defer close(m.reapDone)
	interval := min(max(ttl/2, time.Second), time.Minute)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopReap:
			return
		case now := <-ticker.C:
			m.reapExpired(now, ttl)
		}
	}
}

func (m *sessionManager) reapExpired(now time.Time, ttl time.Duration) {
	var toClose []*copilotSession
	m.mu.Lock()
	for tok, s := range m.byToken {
		s.mu.Lock()
		expired := !s.closed && now.Sub(s.lastUsed) > ttl
		s.mu.Unlock()
		if expired {
			delete(m.byToken, tok)
			delete(m.allSess, s)
			toClose = append(toClose, s)
		}
	}
	m.mu.Unlock()

	for _, s := range toClose {
		_ = s.close()
	}
}

func (m *sessionManager) close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	toClose := make([]*copilotSession, 0, len(m.allSess))
	for s := range m.allSess {
		toClose = append(toClose, s)
	}
	m.byToken = make(map[string]*copilotSession)
	m.allSess = make(map[*copilotSession]struct{})
	m.mu.Unlock()

	close(m.stopReap)
	<-m.reapDone

	for _, s := range toClose {
		_ = s.close()
	}

	m.clientMu.Lock()
	c := m.client
	m.client = nil
	m.clientMu.Unlock()
	if c != nil {
		_ = c.Stop()
	}

	return nil
}

func (m *sessionManager) addIdleListener(fn llm.IdleMessageHandler) func() {
	if fn == nil {
		return func() {}
	}
	m.idleMu.Lock()
	m.nextIdleID++
	id := m.nextIdleID
	m.idleListeners[id] = fn
	m.idleMu.Unlock()

	return func() {
		m.idleMu.Lock()
		delete(m.idleListeners, id)
		m.idleMu.Unlock()
	}
}

func (m *sessionManager) handleSessionIdle(s *copilotSession, newToken string, msg message.Message) {
	if newToken != "" && s != nil {
		m.mu.Lock()
		if !m.closed {
			m.byToken[newToken] = s
		}
		m.mu.Unlock()
	}

	m.idleMu.Lock()
	listeners := make([]llm.IdleMessageHandler, 0, len(m.idleListeners))
	for _, fn := range m.idleListeners {
		listeners = append(listeners, fn)
	}
	m.idleMu.Unlock()

	for _, fn := range listeners {
		fn(msg)
	}
}

type routePlan struct {
	route         string // "parked_tool", "warm_user", "fresh"
	session       *copilotSession
	turnQueue     *turnQueue
	priorMessages []message.Message
	systemPrompt  string
	ext           Session
	workingDir    string
	compact       compactState
	compactOnly   bool
	sendFn        func() (compactState, error)
}

func (m *sessionManager) buildPermissionHandler() copilot.PermissionHandlerFunc {
	if m.cfg.PermissionHandler != nil {
		return m.cfg.PermissionHandler
	}
	if m.cfg.AllowAllPermissions {
		return copilot.PermissionHandler.ApproveAll
	}
	return func(req copilot.PermissionRequest, _ copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
		feedback := "permission denied by default; bind Loom tools or configure PermissionHandler/AllowAllPermissions"
		return &rpc.PermissionDecisionReject{
			Feedback: &feedback,
		}, nil
	}
}

func (m *sessionManager) prepareAndDispatch(ctx context.Context, req *llm.Request) (*routePlan, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, fmt.Errorf("copilot: provider is closed")
	}
	m.mu.Unlock()

	ext, _ := extractSessionExt(req)
	syncOnWrite := m.cfg.SyncOnWrite
	if ext.SyncOnWrite != nil {
		syncOnWrite = *ext.SyncOnWrite
	}

	systemPrompt, convMessages, err := splitSystemAndConversationChecked(req.Model, req.Messages)
	if err != nil {
		return nil, err
	}
	allowedNative := resolveAllowedNativeTools(m.cfg, ext)
	workingDir := m.cfg.WorkingDir
	if ext.WorkingDir != "" {
		workingDir = ext.WorkingDir
	}

	if !ext.ForceFresh {
		if meta, idx, found := findLastSessionCheckpoint(convMessages); found {
			prefixMsgs := convMessages[:idx+1]
			deltaMsgs := convMessages[idx+1:]
			currentDigest := computePrefixDigest(req, systemPrompt, prefixMsgs, ext, allowedNative, workingDir)

			sess, idleCS, syncErr := m.claimLiveSession(meta, currentDigest, syncOnWrite)
			if syncErr != nil {
				return nil, syncErr
			}
			if sess != nil {
				if toolMsgs, ok := areAllToolResults(deltaMsgs); ok && sess.hasParkedCalls() {
					preparedResults, err := prepareToolResults(req.Model, toolMsgs)
					if err != nil {
						m.retireSession(sess)
						return nil, err
					}
					q := sess.beginTurn()
					return &routePlan{
						route:         "parked_tool",
						session:       sess,
						turnQueue:     q,
						priorMessages: convMessages,
						systemPrompt:  systemPrompt,
						ext:           ext,
						workingDir:    workingDir,
						compact:       idleCS,
						sendFn: func() (compactState, error) {
							sess.tracef("[tool.deliver] session=%s count=%d", sess.id, len(toolMsgs))
							return idleCS, sess.sendToolResults(toolMsgs, preparedResults)
						},
					}, nil
				}

				if _, isTools := areAllToolResults(deltaMsgs); !isTools && !sess.hasParkedCalls() && (len(deltaMsgs) > 0 || ext.Compact) {
					if len(deltaMsgs) == 0 && ext.Compact {
						q := sess.beginTurn()
						return &routePlan{
							route:         "warm_user",
							session:       sess,
							turnQueue:     q,
							priorMessages: convMessages,
							systemPrompt:  systemPrompt,
							ext:           ext,
							workingDir:    workingDir,
							compact:       idleCS,
							compactOnly:   true,
						}, nil
					}

					prompt, attachments, err := formatUserDeltaPrompt(req.Model, deltaMsgs)
					if err != nil {
						m.retireSession(sess)
						return nil, err
					}
					msgOpts, err := buildMessageOptions(req, prompt, attachments)
					if err != nil {
						m.retireSession(sess)
						return nil, err
					}
					q := sess.beginTurn()

					return &routePlan{
						route:         "warm_user",
						session:       sess,
						turnQueue:     q,
						priorMessages: convMessages,
						systemPrompt:  systemPrompt,
						ext:           ext,
						workingDir:    workingDir,
						compact:       idleCS,
						sendFn: func() (compactState, error) {
							cs := idleCS
							if ext.Compact {
								mCS, cerr := sess.runCompactTurn(ctx, ext.CompactInstructions)
								if cerr != nil {
									return cs, cerr
								}
								cs = mCS
							}
							sess.tracef("[send] session=%s route=warm_user len=%d attachments=%d", sess.id, len(msgOpts.Prompt), len(msgOpts.Attachments))
							_, serr := sess.sdkSession.Send(ctx, msgOpts)
							return cs, serr
						},
					}, nil
				}

				// Mismatched delta shape for live session; retire it cleanly before fresh start.
				m.retireSession(sess)
			}

			if syncOnWrite {
				reason := "session_unavailable"
				if currentDigest != meta.PrefixDigest {
					reason = "prefix_mismatch"
				}
				return nil, &llm.OutOfSyncError{
					Provider: "copilot",
					Reason:   reason,
				}
			}
		}
	}

	// Path C: Create a fresh Copilot SDK session seeded from Request.Messages.
	prompt, attachments, err := formatMessagesTranscript(req.Model, convMessages)
	if err != nil {
		return nil, err
	}
	msgOpts, err := buildMessageOptions(req, prompt, attachments)
	if err != nil {
		return nil, err
	}

	client, err := m.ensureClient(ctx)
	if err != nil {
		return nil, err
	}

	wrapper := newCopilotSessionWrapper(
		req.Model,
		req.Tools,
		allowedNative,
		m.handleSessionIdle,
		m.cfg.TraceWriter,
	)

	tools, err := buildCopilotTools(req.Tools, wrapper.makeToolHandler())
	if err != nil {
		return nil, err
	}

	availableTools, excludedTools := buildToolFilterLists(req.Tools, allowedNative)
	sessionCfg := &copilot.SessionConfig{
		Model:                 req.Model,
		ClientName:            m.cfg.ClientName,
		WorkingDirectory:      workingDir,
		Streaming:             new(true),
		ReasoningEffort:       mapThinkingToReasoningEffort(req.Thinking),
		ReasoningSummary:      ext.ReasoningSummary,
		Tools:                 tools,
		AvailableTools:        availableTools,
		ExcludedTools:         excludedTools,
		IncludedBuiltInAgents: []string{},
		OnPermissionRequest:   m.buildPermissionHandler(),
		Provider:              m.cfg.BYOKProvider,
		Providers:             m.cfg.BYOKProviders,
		Models:                m.cfg.BYOKModels,
	}
	if systemPrompt != "" {
		sessionCfg.SystemMessage = &copilot.SystemMessageConfig{
			Mode:    m.cfg.SystemMessageMode,
			Content: systemPrompt,
		}
	}

	sdkSess, err := client.CreateSession(ctx, sessionCfg)
	if err != nil {
		return nil, fmt.Errorf("copilot: create session: %w", err)
	}
	wrapper.attach(sdkSess)
	wrapper.tracef("[session.create] id=%s model=%s tools=%d", sdkSess.SessionID, req.Model, len(tools))
	m.trackSession(wrapper)

	q := wrapper.beginTurn()

	return &routePlan{
		route:         "fresh",
		session:       wrapper,
		turnQueue:     q,
		priorMessages: convMessages,
		systemPrompt:  systemPrompt,
		ext:           ext,
		workingDir:    workingDir,
		sendFn: func() (compactState, error) {
			wrapper.tracef("[send] session=%s route=fresh len=%d attachments=%d", wrapper.id, len(msgOpts.Prompt), len(msgOpts.Attachments))
			_, serr := sdkSess.Send(ctx, msgOpts)
			return compactState{}, serr
		},
	}, nil
}

func (m *sessionManager) trackSession(s *copilotSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.allSess[s] = struct{}{}
}

func (m *sessionManager) retireSession(s *copilotSession) {
	if s == nil {
		return
	}
	m.mu.Lock()
	m.deleteTokensForSessionLocked(s)
	delete(m.allSess, s)
	m.mu.Unlock()
	_ = s.close()
}

func (m *sessionManager) deleteTokensForSessionLocked(s *copilotSession) {
	for tok, cur := range m.byToken {
		if cur == s {
			delete(m.byToken, tok)
		}
	}
}

func (m *sessionManager) claimLiveSession(meta sessionMeta, currentDigest string, syncOnWrite bool) (*copilotSession, compactState, error) {
	m.mu.Lock()
	s, ok := m.byToken[meta.TurnToken]
	m.mu.Unlock()
	if !ok {
		return nil, compactState{}, nil
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, compactState{}, nil
	}

	if s.needsSync {
		if meta.SyncToken != "" && meta.SyncToken == s.pendingSyncToken {
			s.needsSync = false
			s.syncReason = ""
			s.pendingSyncToken = ""
			s.pendingSyncMessages = nil
			cs := s.pendingCompact
			s.pendingCompact = compactState{}
			s.prefixDigest = currentDigest
			s.expectedToken = ""
			s.lastUsed = time.Now()
			s.mu.Unlock()

			m.mu.Lock()
			m.deleteTokensForSessionLocked(s)
			m.mu.Unlock()
			return s, cs, nil
		}
		if syncOnWrite {
			reason := s.syncReason
			msgs := slices.Clone(s.pendingSyncMessages)
			s.mu.Unlock()
			return nil, compactState{}, &llm.OutOfSyncError{
				Provider: "copilot",
				Reason:   reason,
				Messages: msgs,
			}
		}
		if s.idleExited {
			s.mu.Unlock()
			m.retireSession(s)
			return nil, compactState{}, nil
		}
		if currentDigest != meta.PrefixDigest {
			s.mu.Unlock()
			return nil, compactState{}, nil
		}
		s.needsSync = false
		s.syncReason = ""
		s.pendingSyncToken = ""
		s.pendingSyncMessages = nil
		cs := s.pendingCompact
		s.pendingCompact = compactState{}
		s.expectedToken = ""
		s.lastUsed = time.Now()
		s.mu.Unlock()

		m.mu.Lock()
		m.deleteTokensForSessionLocked(s)
		m.mu.Unlock()
		return s, cs, nil
	}

	if s.expectedToken != meta.TurnToken || s.prefixDigest != currentDigest {
		s.mu.Unlock()
		return nil, compactState{}, nil
	}

	// Atomically invalidate expectedToken while this turn is in flight so a
	// concurrent branch or retry cannot reuse the same live session.
	s.expectedToken = ""
	s.lastUsed = time.Now()
	s.mu.Unlock()

	m.mu.Lock()
	m.deleteTokensForSessionLocked(s)
	m.mu.Unlock()
	return s, compactState{}, nil
}

func (m *sessionManager) completeTurn(s *copilotSession, res turnResult) {
	if res.errored || s == nil {
		m.retireSession(s)
		return
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = s.close()
		return
	}
	m.deleteTokensForSessionLocked(s)
	if res.turnToken != "" {
		m.byToken[res.turnToken] = s
	}
	m.mu.Unlock()
}
