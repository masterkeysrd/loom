package decision

import "context"

// Provider is the abstraction over a concrete decision backend (e.g., Gemini, OpenAI, Claude).
// Implementations evaluate a structured [Request] against one or more questions and return
// calibrated probabilities and answers in a [Response].
type Provider interface {
	// Name returns an identifier for the provider (e.g., "gemini", "openai").
	Name() string

	// Evaluate executes a decision evaluation request for the given questions.
	Evaluate(ctx context.Context, req *Request) (*Response, error)
}

// Extension is the interface that all provider-specific configurations must
// implement to be passed in a [Request].
type Extension interface {
	// ExtensionID returns a unique identifier for this extension type.
	ExtensionID() string
}

// Request is the provider-agnostic input to a decision evaluation call.
type Request struct {
	Model      string
	Input      Input
	Questions  []Question
	Extensions map[string]Extension
	Options    map[string]any
}

// Evaluator is the functional execution unit for evaluating a decision [Request].
// It is the core function wrapped by [Middleware].
type Evaluator func(context.Context, *Request) (*Response, error)

// Middleware is a function that wraps an [Evaluator], allowing for cross-cutting
// concerns such as logging, telemetry, caching, or rate limiting.
type Middleware func(Evaluator) Evaluator

// CallOption is a functional option that modifies a decision [Request] for a single call.
type CallOption func(*Request)

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
