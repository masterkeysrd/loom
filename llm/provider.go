package llm

import (
	"context"
	"fmt"
	"iter"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/tool"
)

// Provider is the abstraction over a concrete LLM backend (e.g. Ollama, OpenAI).
// Implementations are responsible for translating a generic [Request] into the
// provider-specific wire format and returning a streaming response iterator.
// Each provider also acts as a scoped catalog: it knows exactly which models it
// owns, preventing cross-provider model misuse.
type Provider interface {
	// Name returns an identifier for the provider (e.g. "ollama").
	Name() string

	// Stream initiates a streaming chat completion request and returns an
	// iterator over [message.AssistantChunk] values.
	Stream(context.Context, *Request) (StreamResponse, error)

	// ListProfiles returns all known [profile.Profile] entries for this provider.
	// An empty slice means the provider has no static catalog (e.g. Ollama,
	// where models are user-installed), and profile validation is skipped.
	ListProfiles() []profile.Profile

	// GetProfile looks up a model by ID, checking runtime overrides first and
	// falling back to the static generated catalog.
	GetProfile(id string) (profile.Profile, bool)

	// SearchProfiles returns profiles whose ID or DisplayName contains query
	// (case-insensitive).
	SearchProfiles(query string) []profile.Profile

	// OverrideProfile registers a custom profile for id, shadowing any
	// entry with the same ID in the static catalog.
	OverrideProfile(id string, p profile.Profile)
}

// Extension is the interface that all provider-specific configurations must
// implement to be passed in a [Request].
type Extension interface {
	// ExtensionID returns a unique identifier for this extension type.
	ExtensionID() string
}

// Request is the provider-agnostic input to a chat completion call.
type Request struct {
	Model    string
	Messages []message.Message
	// Tools lists the tool definitions advertised to the model.
	// Providers should translate these into their wire format.
	Tools []tool.Definition

	// MaxTokens is an optional limit on the number of tokens in the response.
	// Providers that do not support this parameter may ignore it.
	MaxTokens int

	// ContextWindow is the optional size of the context window (in tokens) to request.
	// Providers that support dynamic context windows (e.g. Ollama) should use this.
	ContextWindow int

	// Temperature controls randomness. Higher values make the output more random.
	Temperature *float32

	// TopP controls diversity via nucleus sampling.
	TopP *float32

	// TopK limits the vocabulary to the top K tokens.
	TopK *int

	// PresencePenalty penalizes tokens based on whether they have appeared in the text so far.
	PresencePenalty *float32

	// FrequencyPenalty penalizes tokens based on their existing frequency in the text so far.
	FrequencyPenalty *float32

	// Stop is a list of sequences where the model will stop generating further tokens.
	Stop []string

	// ResponseFormat specifies the format of the output (e.g. "json_object").
	ResponseFormat string

	// ResponseSchema is an optional JSON schema that the model's response
	// must adhere to.
	ResponseSchema *jsonschema.Schema

	// Thinking specifies the thinking/reasoning configuration for the request.
	Thinking *ThinkingConfig

	// Extensions captures provider-specific configurations that
	// are not part of the common LLM parameters.
	Extensions map[string]Extension

	// Options captures arbitrary provider-specific key-value options
	// (e.g. Ollama's "keep_alive", "seed", "num_ctx"). Providers that
	// support such knobs should read from this map and apply them to
	// their wire-format request.
	Options map[string]any
}

// CacheManager is an optional interface that providers can implement if they
// support explicit management of long-lived context caches (e.g. Gemini).
type CacheManager interface {
	// CreateCache creates a new context cache resource from the given request
	// (contents, tools, etc.) and returns its identifier.
	CreateCache(context.Context, *Request) (string, error)

	// DeleteCache removes a previously created context cache resource.
	DeleteCache(context.Context, string) error
}

// QuotaUnit identifies what a quota bucket counts.
type QuotaUnit string

const (
	// QuotaUnitPercent represents a percentage/fraction-only limit without discrete counts.
	QuotaUnitPercent QuotaUnit = "percent"
	// QuotaUnitRequests represents discrete requests or premium interactions.
	QuotaUnitRequests QuotaUnit = "requests"
	// QuotaUnitCredits represents server-billed credits or AI units.
	QuotaUnitCredits QuotaUnit = "credits"
	// QuotaUnitTokens represents token-based quota budgets.
	QuotaUnitTokens QuotaUnit = "tokens"
)

// QuotaBucket describes a single rate-limit or billing quota bucket
// (for example, a rolling burst window, a weekly limit, a monthly
// premium-request pool, or a per-model shared pool).
type QuotaBucket struct {
	// ID uniquely identifies this bucket within a snapshot (e.g. "5h", "7d",
	// "premium_interactions", or a model/pool ID).
	ID string `json:"id"`

	// Scope describes the breadth of the bucket (e.g. "account", "plan",
	// "model", "session").
	Scope string `json:"scope,omitempty"`

	// Window is the replenishment or rolling duration of this bucket (for example,
	// 5*time.Hour or 7*24*time.Hour). Zero means the window is fixed/monthly or
	// unspecified by the provider.
	Window time.Duration `json:"window,omitempty"`

	// Unit identifies what Limit, Used, and Overage measure.
	Unit QuotaUnit `json:"unit,omitempty"`

	// Limit is the total allocated capacity in Unit (0 when the provider only
	// reports percentages or Unlimited is true).
	Limit float64 `json:"limit,omitempty"`

	// Used is the consumed amount in Unit within the current window.
	Used float64 `json:"used,omitempty"`

	// RemainingFraction is the available capacity between 0.0 and 1.0
	// (e.g. 0.85 = 85%). When Unlimited is true, RemainingFraction is typically 1.0.
	RemainingFraction float64 `json:"remainingFraction"`

	// Unlimited reports whether this bucket has no metered ceiling.
	Unlimited bool `json:"unlimited,omitempty"`

	// Overage is the overage consumption beyond Limit (if permitted by the plan).
	Overage float64 `json:"overage,omitempty"`

	// ResetAt is the time when this quota window resets, if known.
	ResetAt *time.Time `json:"resetAt,omitempty"`
}

// Percentage returns RemainingFraction multiplied by 100.
func (b QuotaBucket) Percentage() float64 {
	return b.RemainingFraction * 100
}

// Remaining returns Limit - Used clamped at zero, or zero when Limit <= 0.
func (b QuotaBucket) Remaining() float64 {
	if b.Limit <= 0 {
		return 0
	}
	if rem := b.Limit - b.Used; rem > 0 {
		return rem
	}
	return 0
}

// Exhausted reports whether this bucket has no remaining capacity.
func (b QuotaBucket) Exhausted() bool {
	return !b.Unlimited && b.RemainingFraction <= 0
}

// QuotaBuckets is an ordered list of buckets constraining a model or request.
type QuotaBuckets []QuotaBucket

// Bottleneck returns the most constraining bucket in the set (preferring
// metered buckets over unlimited ones, then selecting the lowest RemainingFraction).
func (bs QuotaBuckets) Bottleneck() (QuotaBucket, bool) {
	if len(bs) == 0 {
		return QuotaBucket{}, false
	}
	best := bs[0]
	for _, b := range bs[1:] {
		if best.Unlimited && !b.Unlimited {
			best = b
			continue
		}
		if b.Unlimited && !best.Unlimited {
			continue
		}
		if b.RemainingFraction < best.RemainingFraction {
			best = b
		}
	}
	return best, true
}

// Get looks up a bucket in the slice by its ID.
func (bs QuotaBuckets) Get(id string) (QuotaBucket, bool) {
	for _, b := range bs {
		if b.ID == id {
			return b, true
		}
	}
	return QuotaBucket{}, false
}

// QuotaSnapshot represents a point-in-time view of a provider's quota buckets
// and how models map onto those shared or dedicated buckets.
type QuotaSnapshot struct {
	// CollectedAt is when this snapshot was retrieved from the provider.
	CollectedAt time.Time `json:"collectedAt"`

	// Buckets maps canonical bucket IDs to their quota state. Multiple models
	// sharing the same underlying pool reference the same bucket ID here.
	Buckets map[string]QuotaBucket `json:"buckets,omitempty"`

	// Models maps model IDs to the ordered list of bucket IDs that constrain
	// that model (e.g. ["5h", "7d"] or ["premium_interactions"]).
	Models map[string][]string `json:"models,omitempty"`
}

// ForModel returns the QuotaBuckets referenced by model in declared order.
func (s QuotaSnapshot) ForModel(model string) (QuotaBuckets, bool) {
	ids, ok := s.Models[model]
	if !ok || len(ids) == 0 {
		return nil, false
	}
	out := make(QuotaBuckets, 0, len(ids))
	for _, id := range ids {
		if b, exists := s.Buckets[id]; exists {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// ModelLister is an optional interface that providers can implement if they
// support live model discovery and metadata lookup from the backend API.
//
// Providers with a static catalog (such as OpenAI, Anthropic, and Google GenAI)
// enrich live API responses with static profile metadata (such as pricing,
// capabilities, and limits) without mutating their [Provider.ListProfiles] catalog.
// Dynamic providers (such as Ollama and Antigravity) update their in-memory
// discovered model cache while preserving explicit [Provider.OverrideProfile] entries.
type ModelLister interface {
	// ListModels queries the provider's backend for available models, enriching
	// entries with static or overridden profile metadata when available.
	ListModels(ctx context.Context) ([]profile.Profile, error)

	// GetModel queries the provider's backend for a single model by ID, enriching
	// the result with static or overridden profile metadata when available.
	GetModel(ctx context.Context, id string) (profile.Profile, error)
}

// QuotaProvider is an optional interface that providers can implement if they
// support querying model quotas, usage limits, or rate-limit replenishment windows.
type QuotaProvider interface {
	// GetQuota returns all quota buckets constraining the given model ID,
	// resolving aliases if applicable. Returns false if no quota information
	// is available for the model.
	GetQuota(ctx context.Context, model string) (QuotaBuckets, bool, error)

	// GetQuotaSnapshot returns the full point-in-time quota snapshot, separating
	// shared buckets from per-model bucket bindings.
	GetQuotaSnapshot(ctx context.Context) (QuotaSnapshot, error)
}

// DeferredToolsSupport is an optional interface that providers can implement
// if they natively support deferred tool loading for specific models.
type DeferredToolsSupport interface {
	SupportsDeferredTools(model string) bool
}

// InlineToolsSupport is an optional interface that a [Provider] can implement
// to indicate whether a given model natively supports mid-conversation inline
// tool definitions ([message.ToolDefinitionBlock]).
type InlineToolsSupport interface {
	SupportsInlineTools(model string) bool
}

// IdleMessageHandler receives unsolicited [message.Message] notifications
// emitted by a stateful [Provider] while idle between active [Provider.Stream]
// calls (such as background compaction or session lifecycle events).
//
// Implementations must return promptly without blocking so underlying provider
// transports are not stalled.
type IdleMessageHandler func(msg message.Message)

// IdleMessageNotifier is an optional interface that stateful providers can
// implement to push idle [message.Message] notifications to callers.
type IdleMessageNotifier interface {
	// OnIdleMessage registers fn to receive idle messages and returns an
	// unsubscribe function that removes the listener.
	OnIdleMessage(fn IdleMessageHandler) (unsubscribe func())
}

// OutOfSyncError is returned by a stateful [Provider] (when sync-on-write is
// enabled) if the provider's active session state has diverged from the
// caller's [Request.Messages] (for example, due to background compaction or
// out-of-band history edits).
//
// Callers may inspect Reason and apply Messages to rearrange their local
// conversation history before resubmitting the request.
type OutOfSyncError struct {
	Provider string
	Reason   string
	Messages message.MessageList
	Err      error
}

func (e *OutOfSyncError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: session out of sync (%s): %v", e.Provider, e.Reason, e.Err)
	}
	return fmt.Sprintf("%s: session out of sync (%s)", e.Provider, e.Reason)
}

func (e *OutOfSyncError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// StreamResponse is an iterator over streaming chunks from an LLM provider.
// It follows the standard Go iter.Seq2 convention: iteration stops when the
// yield function returns false or when the provider signals completion.
type StreamResponse iter.Seq2[message.AssistantChunk, error]
