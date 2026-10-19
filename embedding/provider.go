package embedding

import (
	"context"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
)

// Provider is the abstraction over a concrete embedding backend (e.g., Ollama, OpenAI, Gemini).
// Implementations convert one or more [Input] items in a [Request] into dense [Vector] embeddings.
type Provider interface {
	// Name returns an identifier for the provider (e.g., "ollama", "openai", "google-genai").
	Name() string

	// Embed generates embeddings for the given request.
	Embed(ctx context.Context, req *Request) (*Response, error)
}

// ProfileProvider is an optional interface that providers may implement to expose
// model catalog metadata, limits, and pricing information.
type ProfileProvider interface {
	GetProfile(id string) (llm.ModelProfile, bool)
}

// Extension is the interface that all provider-specific configurations must
// implement to be passed in a [Request].
type Extension interface {
	// ExtensionID returns a unique identifier for this extension type.
	ExtensionID() string
}

// Request is the provider-agnostic input to an embedding generation call.
type Request struct {
	Model      string
	Inputs     []Input
	Dimensions *int
	TaskType   TaskType
	Extensions map[string]Extension
	Options    map[string]any
}

// Response encapsulates the generated embedding vectors and token usage metrics.
type Response struct {
	Model      string
	Embeddings []Vector
	Metrics    *message.TokenMetrics
}

// First returns the first embedding vector in the response, or nil if no embeddings are present.
func (r *Response) First() Vector {
	if r == nil || len(r.Embeddings) == 0 {
		return nil
	}
	return r.Embeddings[0]
}

// Embedder is the functional execution unit for processing an embedding [Request].
// It is the core function wrapped by [Middleware].
type Embedder func(context.Context, *Request) (*Response, error)

// Middleware is a function that wraps an [Embedder], allowing for cross-cutting
// concerns such as logging, telemetry, caching, or rate limiting.
type Middleware func(Embedder) Embedder

// CallOption is a functional option that modifies an embedding [Request] for a single call.
type CallOption func(*Request)

// WithDimensionsOption sets the output embedding dimensionality for a single request.
func WithDimensionsOption(dims int) CallOption {
	return func(r *Request) {
		r.Dimensions = &dims
	}
}

// WithTaskTypeOption sets the embedding task type for a single request.
func WithTaskTypeOption(taskType TaskType) CallOption {
	return func(r *Request) {
		r.TaskType = taskType
	}
}

// WithExtensionOption sets a provider-specific extension for a single request.
func WithExtensionOption(ext Extension) CallOption {
	return func(r *Request) {
		if r.Extensions == nil {
			r.Extensions = make(map[string]Extension)
		}
		r.Extensions[ext.ExtensionID()] = ext
	}
}

// WithCallOption sets an arbitrary provider-specific key-value option for a single request.
func WithCallOption(key string, val any) CallOption {
	return func(r *Request) {
		if r.Options == nil {
			r.Options = make(map[string]any)
		}
		r.Options[key] = val
	}
}
