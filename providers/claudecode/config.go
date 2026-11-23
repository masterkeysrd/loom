package loomclaudecode

import (
	"io"
	"os"
	"path/filepath"
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

	// DefaultQuotaCacheTTL is how long `/usage` quota snapshots are cached
	// before spawning another local CLI check.
	DefaultQuotaCacheTTL = 30 * time.Second
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
	// WorkingDir overrides SpawnedStdio.WorkingDirectory for this request when
	// starting a new CLI subprocess.
	WorkingDir string

	// NativeTools opts in specific built-in Claude Code tools (e.g. "Read", "Bash")
	// in addition to Loom's bound tools. Empty keeps built-in tools disabled (`--tools ""`).
	NativeTools []string

	// PermissionMode overrides Config.Session.PermissionMode for this request.
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

	// SyncOnWrite overrides Config.Session.SyncOnWrite for this request when non-nil.
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

// Runtime describes how Claude Code CLI subprocesses are launched.
//
// Construct a [SpawnedStdio] literal and pass it via [Config.Runtime] or
// [WithRuntime]. When nil, a default [SpawnedStdio] is used.
type Runtime interface {
	claudeCodeRuntime()
}

// SpawnedStdio spawns `claude` CLI subprocesses and communicates over their
// stdin/stdout pipes.
type SpawnedStdio struct {
	// Path is the path or binary name of the `claude` CLI executable.
	// When empty, checks CLAUDE_CODE_CLI_PATH (from host env when Env == nil,
	// or from Env when Env != nil) and falls back to [DefaultCLIPath] ("claude").
	Path string

	// Args appends additional raw flags when launching CLI subprocesses.
	Args []string

	// Env specifies environment variables for spawned CLI processes, each in the
	// form "KEY=VALUE", following os/exec.Cmd.Env and copilot.SpawnedStdioConnection:
	//   - nil inherits the current process environment (os.Environ()).
	//   - non-nil (including an empty []string{}) replaces inherited variables.
	// The provider still ensures CLAUDE_CONFIG_DIR and CLAUDE_CODE_ENTRYPOINT
	// are set on spawned subprocesses. Duplicate keys use the last value.
	Env []string

	// WorkingDirectory sets the default working directory for spawned CLI processes.
	WorkingDirectory string

	// ConfigDir sets an isolated CLAUDE_CONFIG_DIR for managed subprocesses so
	// Loom runs do not pollute ~/.claude. When empty:
	//   - if Env != nil and defines a non-empty CLAUDE_CONFIG_DIR, that directory is used;
	//   - otherwise a temporary directory is created and removed on Provider.Close().
	ConfigDir string

	// SourceAuthDir is the directory from which existing Claude CLI auth files
	// (such as .credentials.json) are synced into an isolated ConfigDir when
	// CLAUDE_CODE_OAUTH_TOKEN and ANTHROPIC_API_KEY are not set in the effective
	// child environment.
	// When empty and Env == nil, defaults to ~/.claude when available.
	// When empty and Env != nil, no host auth files are synced.
	SourceAuthDir string
}

func (SpawnedStdio) claudeCodeRuntime() {}

// SessionOptions configures default per-session Claude Code CLI behavior.
type SessionOptions struct {
	// PermissionMode configures `--permission-mode` passed to the CLI.
	// Defaults to [DefaultPermissionMode] ("bypassPermissions").
	PermissionMode string

	// AllowedNativeTools lists built-in Claude Code tools to leave enabled
	// (e.g. "Read", "Grep"). When empty (default), `--tools ""` is passed so
	// Loom's bound tools own all tool execution.
	AllowedNativeTools []string

	// SyncOnWrite enables optimistic sync-on-write checks: if the active CLI
	// session has compacted/drifted while idle or history diverges from the
	// session checkpoint, Stream returns *llm.OutOfSyncError instead of writing
	// or silently falling back to a fresh session.
	SyncOnWrite bool
}

// ProxyOptions configures the in-process mini-proxy lifecycle and caching.
type ProxyOptions struct {
	// IdleTTL controls how long idle or parked CLI subprocesses stay warm in the
	// in-process proxy before being closed. Negative disables TTL reaping.
	IdleTTL time.Duration

	// ShutdownTimeout controls how long graceful shutdown waits after closing
	// stdin before sending SIGTERM. Defaults to [DefaultShutdownTimeout].
	ShutdownTimeout time.Duration

	// MaxSnapshots bounds how many pre-turn .jsonl snapshots are cached for
	// cache-preserving retries and graph forks. Defaults to [DefaultMaxSnapshots].
	MaxSnapshots int

	// QuotaCacheTTL controls how long `/usage` quota snapshots are cached.
	// Defaults to [DefaultQuotaCacheTTL] (30s). Negative disables caching and
	// queries the CLI live on every call.
	QuotaCacheTTL time.Duration
}

// ObservabilityOptions configures idle notifications and protocol tracing.
type ObservabilityOptions struct {
	// OnIdleMessage registers an initial idle notification listener.
	OnIdleMessage llm.IdleMessageHandler

	// TraceWriter optionally receives raw CLI protocol lines (`[stdin]`,
	// `[stdout]`, `[stderr]`) for debugging.
	TraceWriter io.Writer
}

// Config configures a Claude Code [Provider] and its in-process mini-proxy.
type Config struct {
	// Runtime configures how `claude` CLI subprocesses are spawned and isolated.
	// Defaults to [SpawnedStdio]{} when nil.
	Runtime Runtime

	// Session configures default per-session CLI behavior (permission mode,
	// native tools, and sync-on-write checks).
	Session SessionOptions

	// Proxy configures the in-process mini-proxy lifecycle and caching.
	Proxy ProxyOptions

	// Observability configures idle notifications and raw protocol tracing.
	Observability ObservabilityOptions
}

// Option customizes [Config] in [NewDefaultProvider].
type Option func(*Config)

// WithRuntime sets the [Runtime] used to launch Claude Code CLI subprocesses.
func WithRuntime(r Runtime) Option {
	return func(c *Config) { c.Runtime = r }
}

// WithSession sets default per-session Claude Code CLI options.
func WithSession(opts SessionOptions) Option {
	return func(c *Config) { c.Session = opts }
}

// WithProxy sets in-process mini-proxy lifecycle and caching options.
func WithProxy(opts ProxyOptions) Option {
	return func(c *Config) { c.Proxy = opts }
}

// WithObservability sets idle notification and tracing options.
func WithObservability(opts ObservabilityOptions) Option {
	return func(c *Config) { c.Observability = opts }
}

func resolveSpawnedStdio(rt Runtime) SpawnedStdio {
	switch v := rt.(type) {
	case SpawnedStdio:
		return v
	case *SpawnedStdio:
		if v != nil {
			return *v
		}
	}
	return SpawnedStdio{}
}

func (c Config) spawnedStdio() SpawnedStdio {
	return resolveSpawnedStdio(c.Runtime)
}

func cloneEnv(env []string) []string {
	if env == nil {
		return nil
	}
	out := make([]string, len(env))
	copy(out, env)
	return out
}

func envLookup(env []string, key string) (string, bool) {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if val, ok := strings.CutPrefix(env[i], prefix); ok {
			return val, true
		}
	}
	return "", false
}

func normalizeConfig(cfg *Config) Config {
	var out Config
	if cfg != nil {
		out = *cfg
		out.Session.AllowedNativeTools = append([]string(nil), cfg.Session.AllowedNativeTools...)
	}

	rt := resolveSpawnedStdio(out.Runtime)
	rt.Args = append([]string(nil), rt.Args...)
	rt.Env = cloneEnv(rt.Env)

	if strings.TrimSpace(rt.Path) == "" {
		if rt.Env == nil {
			if envPath := strings.TrimSpace(os.Getenv("CLAUDE_CODE_CLI_PATH")); envPath != "" {
				rt.Path = envPath
			} else {
				rt.Path = DefaultCLIPath
			}
		} else {
			if envPath, ok := envLookup(rt.Env, "CLAUDE_CODE_CLI_PATH"); ok && strings.TrimSpace(envPath) != "" {
				rt.Path = strings.TrimSpace(envPath)
			} else {
				rt.Path = DefaultCLIPath
			}
		}
	}

	if rt.SourceAuthDir == "" && rt.Env == nil {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			rt.SourceAuthDir = filepath.Join(home, ".claude")
		}
	}

	out.Runtime = rt

	if strings.TrimSpace(out.Session.PermissionMode) == "" {
		out.Session.PermissionMode = DefaultPermissionMode
	}
	if out.Proxy.IdleTTL == 0 {
		out.Proxy.IdleTTL = DefaultIdleTTL
	}
	if out.Proxy.ShutdownTimeout <= 0 {
		out.Proxy.ShutdownTimeout = DefaultShutdownTimeout
	}
	if out.Proxy.MaxSnapshots <= 0 {
		out.Proxy.MaxSnapshots = DefaultMaxSnapshots
	}
	if out.Proxy.QuotaCacheTTL == 0 {
		out.Proxy.QuotaCacheTTL = DefaultQuotaCacheTTL
	}
	return out
}
