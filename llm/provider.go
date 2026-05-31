package llm

import (
	"context"
	"iter"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/message"
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

	// ListProfiles returns all known [ModelProfile] entries for this provider.
	// An empty slice means the provider has no static catalog (e.g. Ollama,
	// where models are user-installed), and profile validation is skipped.
	ListProfiles() []ModelProfile

	// GetProfile looks up a model by ID, checking runtime overrides first and
	// falling back to the static generated catalog.
	GetProfile(id string) (ModelProfile, bool)

	// SearchProfiles returns profiles whose ID or DisplayName contains query
	// (case-insensitive).
	SearchProfiles(query string) []ModelProfile

	// OverrideProfile registers a custom profile for id, shadowing any
	// entry with the same ID in the static catalog.
	OverrideProfile(id string, profile ModelProfile)

	// GetConfig returns the provider's current configuration, which may include
	// global defaults or model-specific overrides. The boolean indicates whether
	// a config was found for the given model ID.
	GetConfig(modelID string) (ModelConfig, bool)
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
}

// StreamResponse is an iterator over streaming chunks from an LLM provider.
// It follows the standard Go iter.Seq2 convention: iteration stops when the
// yield function returns false or when the provider signals completion.
type StreamResponse iter.Seq2[message.AssistantChunk, error]
