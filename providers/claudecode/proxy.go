package loomclaudecode

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
)

type sessionSnapshot struct {
	token        string
	sessionID    string
	relPath      string
	prefixDigest string
	data         []byte
	createdAt    time.Time
}

// sessionManager is the in-process mini-proxy that manages isolated
// CLAUDE_CONFIG_DIR state, active/parked CLI subprocesses, pre-turn .jsonl
// snapshots for cache-preserving retries/forks, and idle TTL reaping.
type sessionManager struct {
	cfg           Config
	configDir     string
	ownsConfigDir bool

	mu        sync.Mutex
	closed    bool
	byToken   map[string]*cliSession
	allSess   map[*cliSession]struct{}
	snapshots map[string]*sessionSnapshot
	snapOrder []string

	idleMu        sync.Mutex
	idleListeners map[int64]llm.IdleMessageHandler
	nextIdleID    int64

	stopReap chan struct{}
	reapDone chan struct{}
}

func newSessionManager(cfg Config) (*sessionManager, error) {
	configDir := strings.TrimSpace(cfg.ConfigDir)
	ownsDir := false
	if configDir == "" {
		dir, err := os.MkdirTemp("", "loom-claudecode-*")
		if err != nil {
			return nil, fmt.Errorf("claudecode: create isolated config dir: %w", err)
		}
		configDir = dir
		ownsDir = true
	} else {
		if err := os.MkdirAll(configDir, 0o700); err != nil {
			return nil, fmt.Errorf("claudecode: create config dir %q: %w", configDir, err)
		}
	}

	syncAuthCredentials(cfg.SourceAuthDir, configDir)

	m := &sessionManager{
		cfg:           cfg,
		configDir:     configDir,
		ownsConfigDir: ownsDir,
		byToken:       make(map[string]*cliSession),
		allSess:       make(map[*cliSession]struct{}),
		snapshots:     make(map[string]*sessionSnapshot),
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

// syncAuthCredentials copies local Claude CLI auth files (when present and not
// overridden by env tokens) into the isolated CLAUDE_CONFIG_DIR so subscription
// auth works without writing Loom transcripts to ~/.claude.
func syncAuthCredentials(srcDir, dstDir string) {
	if srcDir == "" || dstDir == "" || srcDir == dstDir {
		return
	}
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "" || os.Getenv("ANTHROPIC_API_KEY") != "" {
		return
	}
	for _, name := range []string{".credentials.json", "credentials.json"} {
		src := filepath.Join(srcDir, name)
		dst := filepath.Join(dstDir, name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		_ = os.WriteFile(dst, data, 0o600)
	}
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
	var toClose []*cliSession
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
	toClose := make([]*cliSession, 0, len(m.allSess))
	for s := range m.allSess {
		toClose = append(toClose, s)
	}
	m.byToken = make(map[string]*cliSession)
	m.allSess = make(map[*cliSession]struct{})
	m.snapshots = make(map[string]*sessionSnapshot)
	m.snapOrder = nil
	m.mu.Unlock()

	close(m.stopReap)
	<-m.reapDone

	for _, s := range toClose {
		_ = s.close()
	}

	if m.ownsConfigDir && m.configDir != "" {
		_ = os.RemoveAll(m.configDir)
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

func (m *sessionManager) handleSessionIdle(s *cliSession, newToken string, msg message.Message) {
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
	route           string // "parked_tool", "warm_user", "snapshot_fork", "fresh"
	session         *cliSession
	turnCh          chan readEvent
	priorMessages   []message.Message
	systemPrompt    string
	ext             Session
	promptToSend    string
	toolResults     []*message.Tool
	forkedSessionID string
	compact         compactState
}

func (m *sessionManager) dispatchUserOrCompact(
	ctx context.Context,
	sess *cliSession,
	ch chan readEvent,
	deltaMsgs []message.Message,
	ext Session,
	initialCompact compactState,
) (string, compactState, error) {
	cs := initialCompact
	if len(deltaMsgs) == 0 && ext.Compact {
		prompt := formatCompactPrompt(ext.CompactInstructions)
		if err := sess.sendUserTurn(prompt); err != nil {
			return "", cs, err
		}
		return prompt, cs, nil
	}

	if ext.Compact {
		manualCS, err := sess.runCompactTurn(ctx, ch, ext.CompactInstructions)
		if err != nil {
			return "", cs, err
		}
		cs = manualCS
	}

	prompt, err := formatUserDeltaPrompt(deltaMsgs)
	if err != nil {
		return "", cs, err
	}
	if err := sess.sendUserTurn(prompt); err != nil {
		return "", cs, err
	}
	return prompt, cs, nil
}

func (m *sessionManager) prepareAndDispatch(ctx context.Context, req *llm.Request) (*routePlan, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, fmt.Errorf("claudecode: provider is closed")
	}
	m.mu.Unlock()

	ext, _ := extractSessionExt(req)
	syncOnWrite := m.cfg.SyncOnWrite
	if ext.SyncOnWrite != nil {
		syncOnWrite = *ext.SyncOnWrite
	}

	systemPrompt, convMessages := splitSystemAndConversation(req.Messages)
	allowedNative := resolveAllowedNativeTools(m.cfg, ext)
	workingDir := m.cfg.WorkingDir
	if ext.WorkingDir != "" {
		workingDir = ext.WorkingDir
	}

	if !ext.ForceFresh {
		if meta, idx, found := findLastSessionCheckpoint(convMessages); found {
			prefixMsgs := convMessages[:idx+1]
			deltaMsgs := convMessages[idx+1:]
			currentDigest := computePrefixDigest(req, systemPrompt, prefixMsgs, ext, allowedNative)

			// Path A: Try live tip match (including CAS SyncToken reconciliation).
			sess, idleCS, syncErr := m.claimLiveSession(meta, currentDigest, syncOnWrite)
			if syncErr != nil {
				return nil, syncErr
			}
			if sess != nil {
				if toolMsgs, ok := areAllToolResults(deltaMsgs); ok && sess.hasParkedCalls() {
					ch := sess.beginTurn()
					if err := sess.sendToolResults(toolMsgs); err != nil {
						sess.endTurn(ch)
						m.retireSession(sess)
						return nil, err
					}
					return &routePlan{
						route:         "parked_tool",
						session:       sess,
						turnCh:        ch,
						priorMessages: convMessages,
						systemPrompt:  systemPrompt,
						ext:           ext,
						toolResults:   toolMsgs,
						compact:       idleCS,
					}, nil
				}
				if _, isTools := areAllToolResults(deltaMsgs); !isTools && !sess.hasParkedCalls() && (len(deltaMsgs) > 0 || ext.Compact) {
					ch := sess.beginTurn()
					prompt, cs, err := m.dispatchUserOrCompact(ctx, sess, ch, deltaMsgs, ext, idleCS)
					if err != nil {
						sess.endTurn(ch)
						m.retireSession(sess)
						return nil, err
					}
					return &routePlan{
						route:         "warm_user",
						session:       sess,
						turnCh:        ch,
						priorMessages: convMessages,
						systemPrompt:  systemPrompt,
						ext:           ext,
						promptToSend:  prompt,
						compact:       cs,
					}, nil
				}
				// Mismatched delta shape for live session; retire it cleanly.
				m.retireSession(sess)
			}

			if currentDigest != meta.PrefixDigest {
				if syncOnWrite {
					return nil, &llm.OutOfSyncError{
						Provider: "claudecode",
						Reason:   "prefix_mismatch",
					}
				}
			} else {
				// Path B: Pre-turn .jsonl snapshot fork (retries & parallel graph forks).
				if _, isTools := areAllToolResults(deltaMsgs); !isTools && (len(deltaMsgs) > 0 || ext.Compact) {
					if snap := m.getSnapshot(meta.TurnToken, currentDigest); snap != nil {
						forkedID, err := m.restoreSnapshotAsFork(snap)
						if err == nil {
							args, argErr := buildCLIArgs(req, m.cfg, ext, systemPrompt, forkedID)
							if argErr != nil {
								return nil, argErr
							}
							sess, startErr := startCLISession(
								ctx,
								m.cfg.CLIPath,
								args,
								workingDir,
								m.buildEnv(),
								m.configDir,
								req.Tools,
								allowedNative,
								m.cfg.ShutdownTimeout,
								m.handleSessionIdle,
								m.cfg.TraceWriter,
							)
							if startErr != nil {
								return nil, startErr
							}
							sess.id = forkedID
							m.trackSession(sess)

							ch := sess.beginTurn()
							prompt, cs, pErr := m.dispatchUserOrCompact(ctx, sess, ch, deltaMsgs, ext, compactState{})
							if pErr != nil {
								sess.endTurn(ch)
								m.retireSession(sess)
								return nil, pErr
							}
							return &routePlan{
								route:           "snapshot_fork",
								session:         sess,
								turnCh:          ch,
								priorMessages:   convMessages,
								systemPrompt:    systemPrompt,
								ext:             ext,
								promptToSend:    prompt,
								forkedSessionID: forkedID,
								compact:         cs,
							}, nil
						}
					}
				}
				if syncOnWrite {
					return nil, &llm.OutOfSyncError{
						Provider: "claudecode",
						Reason:   "session_unavailable",
					}
				}
			}
		}
	}

	// Path C: Fresh CLI session fallback (first turn, compaction, cold restore).
	args, err := buildCLIArgs(req, m.cfg, ext, systemPrompt, "")
	if err != nil {
		return nil, err
	}
	sess, err := startCLISession(
		ctx,
		m.cfg.CLIPath,
		args,
		workingDir,
		m.buildEnv(),
		m.configDir,
		req.Tools,
		allowedNative,
		m.cfg.ShutdownTimeout,
		m.handleSessionIdle,
		m.cfg.TraceWriter,
	)
	if err != nil {
		return nil, err
	}
	m.trackSession(sess)

	ch := sess.beginTurn()
	prompt := formatMessagesTranscript(convMessages)
	if err := sess.sendUserTurn(prompt); err != nil {
		sess.endTurn(ch)
		m.retireSession(sess)
		return nil, err
	}

	return &routePlan{
		route:         "fresh",
		session:       sess,
		turnCh:        ch,
		priorMessages: convMessages,
		systemPrompt:  systemPrompt,
		ext:           ext,
		promptToSend:  prompt,
	}, nil
}

func (m *sessionManager) buildEnv() []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env)+len(m.cfg.Env)+2)
	for _, entry := range env {
		if strings.HasPrefix(entry, "CLAUDE_CONFIG_DIR=") || strings.HasPrefix(entry, "CLAUDE_CODE_ENTRYPOINT=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	filtered = append(filtered,
		"CLAUDE_CONFIG_DIR="+m.configDir,
		"CLAUDE_CODE_ENTRYPOINT=loom-claudecode",
	)
	filtered = append(filtered, m.cfg.Env...)
	return filtered
}

func (m *sessionManager) trackSession(s *cliSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.allSess[s] = struct{}{}
}

func (m *sessionManager) retireSession(s *cliSession) {
	if s == nil {
		return
	}
	m.mu.Lock()
	for tok, cur := range m.byToken {
		if cur == s {
			delete(m.byToken, tok)
		}
	}
	delete(m.allSess, s)
	m.mu.Unlock()
	_ = s.close()
}

func (m *sessionManager) deleteTokensForSessionLocked(s *cliSession) {
	for tok, cur := range m.byToken {
		if cur == s {
			delete(m.byToken, tok)
		}
	}
}

func (m *sessionManager) claimLiveSession(meta sessionMeta, currentDigest string, syncOnWrite bool) (*cliSession, compactState, error) {
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
			// Optimistic CAS match: caller applied/acknowledged the sync message.
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
				Provider: "claudecode",
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
	// concurrent branch or retry cannot reuse the same live subprocess.
	s.expectedToken = ""
	s.lastUsed = time.Now()
	s.mu.Unlock()

	m.mu.Lock()
	m.deleteTokensForSessionLocked(s)
	m.mu.Unlock()
	return s, compactState{}, nil
}

func (m *sessionManager) completeTurn(s *cliSession, res turnResult) {
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

	if !res.parked && res.sessionID != "" && res.turnToken != "" {
		m.captureSnapshot(res.sessionID, res.turnToken, res.prefixDigest)
	}
}

func (m *sessionManager) captureSnapshot(sessionID, turnToken, prefixDigest string) {
	relPath, data, ok := findSessionTranscript(m.configDir, sessionID)
	if !ok {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	if _, exists := m.snapshots[turnToken]; exists {
		return
	}
	m.snapshots[turnToken] = &sessionSnapshot{
		token:        turnToken,
		sessionID:    sessionID,
		relPath:      relPath,
		prefixDigest: prefixDigest,
		data:         data,
		createdAt:    time.Now(),
	}
	m.snapOrder = append(m.snapOrder, turnToken)
	for len(m.snapOrder) > m.cfg.MaxSnapshots {
		oldest := m.snapOrder[0]
		m.snapOrder = m.snapOrder[1:]
		delete(m.snapshots, oldest)
	}
}

func (m *sessionManager) getSnapshot(turnToken, prefixDigest string) *sessionSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap, ok := m.snapshots[turnToken]
	if !ok || snap.prefixDigest != prefixDigest {
		return nil
	}
	return snap
}

func (m *sessionManager) restoreSnapshotAsFork(snap *sessionSnapshot) (string, error) {
	forkedID := uuid.NewString()
	relDir := filepath.Dir(snap.relPath)
	targetDir := filepath.Join(m.configDir, relDir)
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return "", err
	}
	targetPath := filepath.Join(targetDir, forkedID+".jsonl")
	if err := os.WriteFile(targetPath, slices.Clone(snap.data), 0o600); err != nil {
		return "", err
	}
	return forkedID, nil
}

func findSessionTranscript(configDir, sessionID string) (string, []byte, bool) {
	targetName := sessionID + ".jsonl"
	var foundRel string
	var foundData []byte

	_ = filepath.WalkDir(configDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() == targetName {
			if data, rerr := os.ReadFile(path); rerr == nil {
				if rel, relErr := filepath.Rel(configDir, path); relErr == nil {
					foundRel = rel
					foundData = data
					return filepath.SkipAll
				}
			}
		}
		return nil
	})

	if foundRel == "" {
		return "", nil, false
	}
	return foundRel, foundData, true
}

func (s *cliSession) hasParkedCalls() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.parkedCalls) > 0
}
