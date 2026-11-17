package loomclaudecode

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
)

const (
	// SessionIDKey is the metadata/extras key on Assistant messages and chunks
	// storing the Claude Code session ID.
	SessionIDKey = "claudecode.session_id"

	// TurnTokenKey is the metadata/extras key on Assistant messages and chunks
	// storing the opaque tip token: hash(session_id + turn_nonce + prefix_digest).
	TurnTokenKey = "claudecode.turn_token"

	// PrefixDigestKey is the metadata key storing the prefix digest associated
	// with a TurnToken so retries and forks can verify history integrity.
	PrefixDigestKey = "claudecode.prefix_digest"

	// CompactedKey is set to true in Assistant metadata when Claude Code
	// performed context compaction (auto or manual) during the turn.
	CompactedKey = "claudecode.compacted"

	// CompactTriggerKey stores the compaction trigger ("auto" or "manual") in
	// Assistant metadata when compaction occurred during the turn.
	CompactTriggerKey = "claudecode.compact_trigger"

	// CompactPreTokensKey stores the pre-compaction token count reported by
	// Claude Code's compact_boundary event.
	CompactPreTokensKey = "claudecode.compact_pre_tokens"

	// SessionExtensionID is the unique identifier for the [Session] request extension.
	SessionExtensionID = "claudecode.session"

	// IdleEventExtensionID is the [message.Extension] ID attached to idle
	// notification messages delivered via [llm.IdleMessageNotifier].
	IdleEventExtensionID = "claudecode.idle_event"

	// SyncCheckpointExtensionID is the [message.Extension] ID attached to
	// replacement checkpoint messages delivered via OnIdleMessage or
	// [llm.OutOfSyncError.Messages] to authorize optimistic history re-basing.
	SyncCheckpointExtensionID = "claudecode.sync_checkpoint"

	// DefaultMCPServerName is the in-process SDK MCP server name registered with
	// the Claude Code CLI for Loom-owned tools.
	DefaultMCPServerName = "loom"

	// DefaultCLIPath is the default executable name for Claude Code.
	DefaultCLIPath = "claude"

	// DefaultPermissionMode is used when Loom owns tool execution so in-process
	// MCP tool calls are dispatched without interactive CLI prompts.
	DefaultPermissionMode = "bypassPermissions"

	// DefaultIdleTTL is how long an idle or parked CLI subprocess is kept alive
	// before being reaped by the in-process proxy.
	DefaultIdleTTL = 15 * time.Minute

	// DefaultShutdownTimeout is how long the proxy waits for a CLI process to exit
	// cleanly after closing stdin before sending SIGTERM.
	DefaultShutdownTimeout = 3 * time.Second

	// DefaultMaxSnapshots is the maximum number of pre-turn .jsonl snapshots
	// retained in memory by the in-process proxy.
	DefaultMaxSnapshots = 64
)

func init() {
	message.RegisterExtension(func() message.Extension {
		return &IdleEventExtension{}
	})
	message.RegisterExtension(func() message.Extension {
		return &SyncCheckpointExtension{}
	})
}

// IdleEventExtension is a typed [message.Extension] attached to idle
// notifications emitted via [llm.IdleMessageNotifier].
type IdleEventExtension struct {
	Kind      string `json:"kind"` // "compacting", "compacted", "exited"
	SessionID string `json:"session_id,omitempty"`
	Trigger   string `json:"trigger,omitempty"` // "auto" or "manual"
	PreTokens int    `json:"pre_tokens,omitempty"`
}

// ExtensionID returns [IdleEventExtensionID].
func (e *IdleEventExtension) ExtensionID() string { return IdleEventExtensionID }

// SyncCheckpointExtension is a typed [message.Extension] attached to
// replacement checkpoint messages emitted via OnIdleMessage or
// [llm.OutOfSyncError.Messages] so resubmitting re-arranged history re-bases
// the live CLI session safely.
type SyncCheckpointExtension struct {
	SessionID string `json:"session_id"`
	TurnToken string `json:"turn_token"`
	SyncToken string `json:"sync_token"`
}

// ExtensionID returns [SyncCheckpointExtensionID].
func (e *SyncCheckpointExtension) ExtensionID() string { return SyncCheckpointExtensionID }

// Session is an [llm.Extension] that configures per-request Claude Code CLI
// behavior such as explicit session affinity, working directory, or opt-in
// native Claude Code tools.
type Session struct {
	// WorkingDir overrides Config.WorkingDir for this request when starting a
	// new CLI subprocess.
	WorkingDir string

	// NativeTools opts in specific built-in Claude Code tools (e.g. "Read", "Bash")
	// in addition to Loom's bound tools. Empty keeps built-in tools disabled (`--tools ""`).
	NativeTools []string

	// PermissionMode overrides Config.PermissionMode for this request.
	PermissionMode string

	// ForceFresh forces a fresh CLI session instead of reusing or forking an
	// existing session from message metadata.
	ForceFresh bool

	// Compact triggers Claude Code's native `/compact` command on an active or
	// resumed session before sending any new user prompt in the request (or as a
	// compaction-only turn if there are no new messages after the last assistant
	// checkpoint). Pass per call via llm.WithExtensionOption.
	Compact bool

	// CompactInstructions supplies optional focus instructions appended to
	// `/compact` when Compact is true.
	CompactInstructions string

	// SyncOnWrite overrides Config.SyncOnWrite for this request when non-nil.
	SyncOnWrite *bool
}

// ExtensionID returns [SessionExtensionID].
func (Session) ExtensionID() string { return SessionExtensionID }

func extractSessionExt(req *llm.Request) (Session, bool) {
	if req == nil || req.Extensions == nil {
		return Session{}, false
	}
	ext, ok := req.Extensions[SessionExtensionID]
	if !ok || ext == nil {
		return Session{}, false
	}
	switch v := ext.(type) {
	case Session:
		return v, true
	case *Session:
		if v == nil {
			return Session{}, false
		}
		return *v, true
	default:
		return Session{}, false
	}
}

// Config configures a Claude Code [Provider] and its in-process mini-proxy.
type Config struct {
	// CLIPath is the path or binary name of the `claude` CLI.
	// Defaults to CLAUDE_CODE_CLI_PATH env var or "claude".
	CLIPath string

	// ConfigDir sets an isolated CLAUDE_CONFIG_DIR for managed subprocesses so
	// Loom runs do not pollute ~/.claude. When empty, a temporary directory is
	// created and removed on Provider.Close().
	ConfigDir string

	// SourceAuthDir is the directory from which existing Claude CLI auth files
	// (such as .credentials.json) are synced into an isolated ConfigDir when
	// CLAUDE_CODE_OAUTH_TOKEN and ANTHROPIC_API_KEY are not set.
	// Defaults to ~/.claude when available.
	SourceAuthDir string

	// WorkingDir sets the default working directory for spawned CLI processes.
	WorkingDir string

	// PermissionMode configures `--permission-mode` passed to the CLI.
	// Defaults to DefaultPermissionMode ("bypassPermissions").
	PermissionMode string

	// AllowedNativeTools lists built-in Claude Code tools to leave enabled
	// (e.g. "Read", "Grep"). When empty (default), `--tools ""` is passed so
	// Loom's bound tools own all tool execution.
	AllowedNativeTools []string

	// IdleTTL controls how long idle or parked CLI subprocesses stay warm in the
	// in-process proxy before being closed. Negative disables TTL reaping.
	IdleTTL time.Duration

	// ShutdownTimeout controls how long graceful shutdown waits after closing
	// stdin before sending SIGTERM. Defaults to DefaultShutdownTimeout.
	ShutdownTimeout time.Duration

	// MaxSnapshots bounds how many pre-turn .jsonl snapshots are cached for
	// cache-preserving retries and graph forks. Defaults to DefaultMaxSnapshots.
	MaxSnapshots int

	// Env appends additional KEY=VALUE environment variables to spawned CLI processes.
	Env []string

	// ExtraArgs appends additional raw flags when launching the CLI subprocess.
	ExtraArgs []string

	// SyncOnWrite enables optimistic sync-on-write checks: if the active CLI
	// session has compacted/drifted while idle or history diverges from the
	// session checkpoint, Stream returns *llm.OutOfSyncError instead of writing
	// or silently falling back to a fresh session.
	SyncOnWrite bool

	// OnIdleMessage registers an initial idle notification listener.
	OnIdleMessage llm.IdleMessageHandler

	// TraceWriter optionally receives raw CLI protocol lines (`[stdin]`,
	// `[stdout]`, `[stderr]`) for debugging.
	TraceWriter io.Writer
}

// Option customizes [Config] in [NewDefaultProvider].
type Option func(*Config)

// WithCLIPath sets the path to the `claude` binary.
func WithCLIPath(path string) Option {
	return func(c *Config) { c.CLIPath = path }
}

// WithConfigDir sets an explicit isolated CLAUDE_CONFIG_DIR.
func WithConfigDir(dir string) Option {
	return func(c *Config) { c.ConfigDir = dir }
}

// WithSourceAuthDir overrides the source directory used to sync local CLI
// authentication files into an isolated CLAUDE_CONFIG_DIR.
func WithSourceAuthDir(dir string) Option {
	return func(c *Config) { c.SourceAuthDir = dir }
}

// WithWorkingDir sets the default working directory for spawned CLI processes.
func WithWorkingDir(dir string) Option {
	return func(c *Config) { c.WorkingDir = dir }
}

// WithPermissionMode sets the CLI `--permission-mode` flag.
func WithPermissionMode(mode string) Option {
	return func(c *Config) { c.PermissionMode = mode }
}

// WithAllowedNativeTools enables specific built-in Claude Code tools.
func WithAllowedNativeTools(tools ...string) Option {
	return func(c *Config) {
		c.AllowedNativeTools = append([]string(nil), tools...)
	}
}

// WithIdleTTL sets the idle/parked subprocess expiration duration.
func WithIdleTTL(ttl time.Duration) Option {
	return func(c *Config) { c.IdleTTL = ttl }
}

// WithShutdownTimeout sets the graceful shutdown wait duration before SIGTERM.
func WithShutdownTimeout(d time.Duration) Option {
	return func(c *Config) { c.ShutdownTimeout = d }
}

// WithMaxSnapshots sets the maximum number of retained pre-turn .jsonl snapshots.
func WithMaxSnapshots(n int) Option {
	return func(c *Config) { c.MaxSnapshots = n }
}

// WithEnv appends extra KEY=VALUE entries to the CLI environment.
func WithEnv(env ...string) Option {
	return func(c *Config) { c.Env = append(c.Env, env...) }
}

// WithExtraArgs appends extra CLI flags.
func WithExtraArgs(args ...string) Option {
	return func(c *Config) { c.ExtraArgs = append(c.ExtraArgs, args...) }
}

// WithSyncOnWrite enables or disables strict sync-on-write checks.
func WithSyncOnWrite(enabled bool) Option {
	return func(c *Config) { c.SyncOnWrite = enabled }
}

// WithOnIdleMessage registers a handler for idle [message.Message] notifications.
func WithOnIdleMessage(fn llm.IdleMessageHandler) Option {
	return func(c *Config) { c.OnIdleMessage = fn }
}

// WithTraceWriter sets an io.Writer that receives raw CLI stdin/stdout/stderr
// trace lines for debugging.
func WithTraceWriter(w io.Writer) Option {
	return func(c *Config) { c.TraceWriter = w }
}

func normalizeConfig(cfg *Config) Config {
	var out Config
	if cfg != nil {
		out = *cfg
		out.AllowedNativeTools = append([]string(nil), cfg.AllowedNativeTools...)
		out.Env = append([]string(nil), cfg.Env...)
		out.ExtraArgs = append([]string(nil), cfg.ExtraArgs...)
	}
	if strings.TrimSpace(out.CLIPath) == "" {
		if envPath := strings.TrimSpace(os.Getenv("CLAUDE_CODE_CLI_PATH")); envPath != "" {
			out.CLIPath = envPath
		} else {
			out.CLIPath = DefaultCLIPath
		}
	}
	if strings.TrimSpace(out.PermissionMode) == "" {
		out.PermissionMode = DefaultPermissionMode
	}
	if out.IdleTTL == 0 {
		out.IdleTTL = DefaultIdleTTL
	}
	if out.ShutdownTimeout <= 0 {
		out.ShutdownTimeout = DefaultShutdownTimeout
	}
	if out.MaxSnapshots <= 0 {
		out.MaxSnapshots = DefaultMaxSnapshots
	}
	if out.SourceAuthDir == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			out.SourceAuthDir = home + string(os.PathSeparator) + ".claude"
		}
	}
	return out
}
