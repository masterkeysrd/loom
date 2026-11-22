package loomcopilot

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	copilot "github.com/microsoft/copilot-sdk/go/v2"
)

const (
	// SessionIDKey is the metadata/extras key on Assistant messages and chunks
	// storing the Copilot SDK session ID.
	SessionIDKey = "copilot.session_id"

	// TurnTokenKey is the metadata/extras key on Assistant messages and chunks
	// storing the opaque tip token: hash(session_id + turn_nonce + prefix_digest).
	TurnTokenKey = "copilot.turn_token"

	// PrefixDigestKey is the metadata key storing the prefix digest associated
	// with a TurnToken so retries, edits, and forks can verify history integrity.
	PrefixDigestKey = "copilot.prefix_digest"

	// EventIDKey is the metadata key storing the authoritative Copilot
	// assistant.message event ID for the turn when available.
	EventIDKey = "copilot.event_id"

	// CompactedKey is set to true in Assistant metadata when Copilot performed
	// context compaction (background or manual) for the turn.
	CompactedKey = "copilot.compacted"

	// CompactTriggerKey stores the compaction trigger ("auto" or "manual") in
	// Assistant metadata when compaction occurred.
	CompactTriggerKey = "copilot.compact_trigger"

	// CompactTokensRemovedKey stores the number of tokens freed by compaction.
	CompactTokensRemovedKey = "copilot.compact_tokens_removed"

	// CompactMessagesRemovedKey stores the number of messages removed by compaction.
	CompactMessagesRemovedKey = "copilot.compact_messages_removed"

	// SessionExtensionID is the unique identifier for the [Session] request extension.
	SessionExtensionID = "copilot.session"

	// IdleEventExtensionID is the [message.Extension] ID attached to idle
	// notification messages delivered via [llm.IdleMessageNotifier].
	IdleEventExtensionID = "copilot.idle_event"

	// SyncCheckpointExtensionID is the [message.Extension] ID attached to
	// replacement checkpoint messages delivered via OnIdleMessage or
	// [llm.OutOfSyncError.Messages] to authorize optimistic history re-basing.
	SyncCheckpointExtensionID = "copilot.sync_checkpoint"

	// DefaultCLIPath is the executable name looked up on PATH if COPILOT_CLI_PATH
	// and Connection are unset.
	DefaultCLIPath = "copilot"

	// DefaultSystemMessageMode controls how leading System messages are passed
	// to Copilot sessions ("replace" gives Loom full prompt control).
	DefaultSystemMessageMode = "replace"

	// DefaultIdleTTL is how long an idle or parked Copilot session is kept warm
	// before being disconnected by the session manager.
	DefaultIdleTTL = 15 * time.Minute
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
	Kind            string `json:"kind"` // "compacting", "compacted", "truncated", "exited", "error"
	SessionID       string `json:"session_id,omitempty"`
	Trigger         string `json:"trigger,omitempty"` // "auto" or "manual"
	TokensRemoved   int64  `json:"tokens_removed,omitempty"`
	MessagesRemoved int64  `json:"messages_removed,omitempty"`
	Message         string `json:"message,omitempty"`
}

// ExtensionID returns [IdleEventExtensionID].
func (e *IdleEventExtension) ExtensionID() string { return IdleEventExtensionID }

// SyncCheckpointExtension is a typed [message.Extension] attached to
// replacement checkpoint messages emitted via OnIdleMessage or
// [llm.OutOfSyncError.Messages] so resubmitting re-arranged history re-bases
// the live Copilot session safely.
type SyncCheckpointExtension struct {
	SessionID string `json:"session_id"`
	TurnToken string `json:"turn_token"`
	SyncToken string `json:"sync_token"`
}

// ExtensionID returns [SyncCheckpointExtensionID].
func (e *SyncCheckpointExtension) ExtensionID() string { return SyncCheckpointExtensionID }

// Session is an [llm.Extension] that configures per-request Copilot SDK
// behavior such as working directory, opt-in native CLI tools, compaction, or
// bypassing session reuse.
type Session struct {
	// WorkingDir overrides Config.WorkingDir for this request when creating a
	// new Copilot session.
	WorkingDir string

	// NativeTools opts in specific built-in Copilot CLI tools (e.g. "view",
	// "grep", "bash") in addition to Loom's bound tools. Empty disables built-in
	// CLI tools so Loom's bound tools own execution.
	NativeTools []string

	// ForceFresh forces a fresh Copilot session instead of reusing a warm or
	// parked session from message metadata.
	ForceFresh bool

	// Compact triggers Copilot's `session.history.compact` RPC on the active
	// session before sending any new prompt (or as a compaction-only turn if
	// there are no new messages after the last checkpoint).
	Compact bool

	// CompactInstructions supplies optional focus instructions passed to
	// `session.history.compact`.
	CompactInstructions string

	// SyncOnWrite overrides Config.SyncOnWrite for this request when non-nil.
	SyncOnWrite *bool

	// ReasoningSummary overrides the reasoning summary mode ("none", etc.)
	// when supported by the model.
	ReasoningSummary copilot.ReasoningSummary
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

// Config configures a Copilot SDK [Provider].
type Config struct {
	// CLIPath is the path or binary name of the Copilot CLI runtime.
	// Defaults to COPILOT_CLI_PATH, then `copilot` on PATH, then the SDK's
	// embedded runtime (if bundled). Ignored when Connection is set.
	CLIPath string

	// Connection optionally overrides the Copilot SDK transport (SpawnedStdioConnection,
	// SpawnedTCPConnection, TCPConnection, or InProcessConnection).
	Connection copilot.RuntimeConnection

	// BaseDirectory sets COPILOT_HOME for spawned runtimes to isolate Copilot
	// session state and config from ~/.copilot. When empty, the Copilot SDK
	// uses its standard default (~/.copilot).
	BaseDirectory string

	// WorkingDir sets the default working directory for Copilot sessions.
	WorkingDir string

	// GitHubToken authenticates with GitHub Copilot explicitly (instead of
	// stored CLI/gh auth).
	GitHubToken string

	// UseLoggedInUser controls whether stored OAuth/gh CLI credentials are used.
	// When nil, the SDK defaults to true unless GitHubToken is set.
	UseLoggedInUser *bool

	// ClientMode controls the Copilot SDK client mode (ModeCodingAgent or
	// ModeEmpty). Defaults to ModeCodingAgent.
	ClientMode copilot.ClientMode

	// ClientName identifies this application on Copilot API requests.
	ClientName string

	// ClientInfo optionally declares editor/integration attribution on connect.
	ClientInfo *copilot.ClientInfo

	// SystemMessageMode selects "replace" (default) or "append" when a leading
	// Loom System message is present.
	SystemMessageMode string

	// PermissionHandler handles Copilot CLI permission requests for native tools
	// or MCP servers. When nil, requests are rejected unless AllowAllPermissions
	// is true (preventing interactive hangs).
	PermissionHandler copilot.PermissionHandlerFunc

	// AllowAllPermissions uses copilot.PermissionHandler.ApproveAll when
	// PermissionHandler is nil.
	AllowAllPermissions bool

	// AllowedNativeTools lists built-in Copilot CLI tools to expose in addition
	// to Loom's bound tools (e.g. "view", "grep"). When empty (default),
	// built-in tools are disabled so Loom owns all tool execution.
	AllowedNativeTools []string

	// BYOKProvider configures a custom model provider (OpenAI, Azure,
	// Anthropic, or OpenAI-compatible local endpoints such as Ollama).
	BYOKProvider *copilot.ProviderConfig

	// BYOKModels optionally registers named model definitions alongside
	// BYOKProviders.
	BYOKModels []copilot.ProviderModelConfig

	// BYOKProviders optionally configures additive named BYOK provider
	// connections (cannot be combined with BYOKProvider).
	BYOKProviders []copilot.NamedProviderConfig

	// OnListModels overrides runtime `models.list` discovery (useful in BYOK mode).
	OnListModels func(ctx context.Context) ([]copilot.Model, error)

	// LogLevel sets the Copilot runtime log level ("none", "error", "warning",
	// "info", "debug", "all").
	LogLevel string

	// Env appends KEY=VALUE environment entries for child-process runtimes.
	Env []string

	// CLIArgs appends extra CLI flags for child-process runtimes.
	CLIArgs []string

	// IdleTTL controls how long idle or parked Copilot sessions stay warm before
	// being disconnected. Negative disables TTL reaping.
	IdleTTL time.Duration

	// SyncOnWrite enables optimistic sync-on-write checks: if the active
	// session compacted/truncated while idle or history diverges from the
	// session checkpoint, Stream returns *llm.OutOfSyncError instead of writing
	// or silently falling back to a fresh session.
	SyncOnWrite bool

	// OnIdleMessage registers an initial idle notification listener.
	OnIdleMessage llm.IdleMessageHandler

	// TraceWriter optionally receives structured SDK session/protocol trace lines.
	TraceWriter io.Writer
}

// Option customizes [Config] in [NewDefaultProvider].
type Option func(*Config)

// WithCLIPath sets the path to the Copilot CLI runtime executable.
func WithCLIPath(path string) Option {
	return func(c *Config) { c.CLIPath = path }
}

// WithConnection sets an explicit Copilot SDK [copilot.RuntimeConnection].
func WithConnection(conn copilot.RuntimeConnection) Option {
	return func(c *Config) { c.Connection = conn }
}

// WithBaseDirectory sets an explicit COPILOT_HOME directory.
func WithBaseDirectory(dir string) Option {
	return func(c *Config) { c.BaseDirectory = dir }
}

// WithWorkingDir sets the default working directory for Copilot sessions.
func WithWorkingDir(dir string) Option {
	return func(c *Config) { c.WorkingDir = dir }
}

// WithGitHubToken sets an explicit GitHub token for Copilot authentication.
func WithGitHubToken(token string) Option {
	return func(c *Config) { c.GitHubToken = token }
}

// WithUseLoggedInUser controls whether stored OAuth/gh auth is used.
func WithUseLoggedInUser(use bool) Option {
	return func(c *Config) { c.UseLoggedInUser = &use }
}

// WithClientMode sets the Copilot SDK client mode (ModeCodingAgent or ModeEmpty).
func WithClientMode(mode copilot.ClientMode) Option {
	return func(c *Config) { c.ClientMode = mode }
}

// WithClientName sets the application identifier sent on API requests.
func WithClientName(name string) Option {
	return func(c *Config) { c.ClientName = name }
}

// WithSystemMessageMode sets how leading System messages are applied ("replace" or "append").
func WithSystemMessageMode(mode string) Option {
	return func(c *Config) { c.SystemMessageMode = mode }
}

// WithPermissionHandler sets a custom handler for Copilot runtime permission requests.
func WithPermissionHandler(fn copilot.PermissionHandlerFunc) Option {
	return func(c *Config) { c.PermissionHandler = fn }
}

// WithAllowAllPermissions enables auto-approval for Copilot runtime permission requests
// when no custom PermissionHandler is set.
func WithAllowAllPermissions(allow bool) Option {
	return func(c *Config) { c.AllowAllPermissions = allow }
}

// WithAllowedNativeTools enables specific built-in Copilot CLI tools.
func WithAllowedNativeTools(tools ...string) Option {
	return func(c *Config) {
		c.AllowedNativeTools = append([]string(nil), tools...)
	}
}

// WithBYOKProvider configures a custom model provider (OpenAI, Azure, Anthropic, Ollama).
func WithBYOKProvider(p *copilot.ProviderConfig) Option {
	return func(c *Config) { c.BYOKProvider = p }
}

// WithOnListModels sets a custom model catalog handler for ListModels.
func WithOnListModels(fn func(ctx context.Context) ([]copilot.Model, error)) Option {
	return func(c *Config) { c.OnListModels = fn }
}

// WithLogLevel sets the Copilot runtime log level.
func WithLogLevel(level string) Option {
	return func(c *Config) { c.LogLevel = level }
}

// WithEnv appends extra KEY=VALUE entries to child-process runtime environments.
func WithEnv(env ...string) Option {
	return func(c *Config) { c.Env = append(c.Env, env...) }
}

// WithCLIArgs appends extra flags to child-process runtime commands.
func WithCLIArgs(args ...string) Option {
	return func(c *Config) { c.CLIArgs = append(c.CLIArgs, args...) }
}

// WithIdleTTL sets the idle/parked session expiration duration.
func WithIdleTTL(ttl time.Duration) Option {
	return func(c *Config) { c.IdleTTL = ttl }
}

// WithSyncOnWrite enables or disables strict sync-on-write checks.
func WithSyncOnWrite(enabled bool) Option {
	return func(c *Config) { c.SyncOnWrite = enabled }
}

// WithOnIdleMessage registers a handler for idle [message.Message] notifications.
func WithOnIdleMessage(fn llm.IdleMessageHandler) Option {
	return func(c *Config) { c.OnIdleMessage = fn }
}

// WithTraceWriter sets an io.Writer that receives structured SDK trace lines.
func WithTraceWriter(w io.Writer) Option {
	return func(c *Config) { c.TraceWriter = w }
}

func normalizeConfig(cfg *Config) Config {
	var out Config
	if cfg != nil {
		out = *cfg
		out.AllowedNativeTools = append([]string(nil), cfg.AllowedNativeTools...)
		out.Env = append([]string(nil), cfg.Env...)
		out.CLIArgs = append([]string(nil), cfg.CLIArgs...)
		out.BYOKModels = append([]copilot.ProviderModelConfig(nil), cfg.BYOKModels...)
		out.BYOKProviders = append([]copilot.NamedProviderConfig(nil), cfg.BYOKProviders...)
		if cfg.BYOKProvider != nil {
			cp := *cfg.BYOKProvider
			out.BYOKProvider = &cp
		}
	}

	if strings.TrimSpace(out.CLIPath) == "" && out.Connection == nil {
		if envPath := strings.TrimSpace(os.Getenv("COPILOT_CLI_PATH")); envPath != "" {
			out.CLIPath = envPath
		} else if found, err := exec.LookPath(DefaultCLIPath); err == nil {
			out.CLIPath = found
		}
	}

	if strings.TrimSpace(out.SystemMessageMode) == "" {
		out.SystemMessageMode = DefaultSystemMessageMode
	}

	if out.IdleTTL == 0 {
		out.IdleTTL = DefaultIdleTTL
	}

	return out
}
