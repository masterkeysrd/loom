package decision

import (
	"context"
	"fmt"
	"maps"

	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/stream"
)

// ModelConfig holds configuration options and extensions for a decision model.
type ModelConfig struct {
	// Extensions captures provider-specific configurations.
	Extensions map[string]Extension `json:"extensions,omitempty"`

	// Options captures arbitrary provider-specific key-value options.
	Options map[string]any `json:"options,omitempty"`
}

// Model wraps a [Provider] and specific model name for probabilistic decision evaluation.
type Model struct {
	name       string
	provider   Provider
	profile    *profile.Profile
	config     *ModelConfig
	middleware []Middleware
}

// NewModel constructs a [Model] configured with the specified provider and model name.
// If provider implements [profile.Provider], its catalog is checked for profile information.
// [TelemetryMiddleware] is attached by default.
func NewModel(provider Provider, name string, config *ModelConfig) (*Model, error) {
	if provider == nil {
		return nil, fmt.Errorf("decision: provider cannot be nil")
	}
	if name == "" {
		return nil, fmt.Errorf("decision: model name cannot be empty")
	}

	var prof *profile.Profile
	if pp, ok := provider.(profile.Provider); ok {
		if p, found := pp.GetProfile(name); found {
			prof = &p
		}
	}

	if config == nil {
		config = &ModelConfig{}
	} else {
		cfg := *config
		if len(config.Extensions) > 0 {
			cfg.Extensions = make(map[string]Extension, len(config.Extensions))
			maps.Copy(cfg.Extensions, config.Extensions)
		}
		if len(config.Options) > 0 {
			cfg.Options = make(map[string]any, len(config.Options))
			maps.Copy(cfg.Options, config.Options)
		}
		config = &cfg
	}

	m := &Model{
		name:       name,
		provider:   provider,
		profile:    prof,
		config:     config,
		middleware: make([]Middleware, 0, 1),
	}

	// Attach telemetry middleware by default
	m.middleware = append(m.middleware, TelemetryMiddleware(provider))

	return m, nil
}

// Name returns the configured model identifier.
func (m *Model) Name() string {
	return m.name
}

// Provider returns the underlying [Provider].
func (m *Model) Provider() Provider {
	return m.provider
}

// Profile returns the model profile if available, or nil otherwise.
func (m *Model) Profile() *profile.Profile {
	return m.profile
}

// Config returns the current [ModelConfig].
func (m *Model) Config() *ModelConfig {
	return m.config
}

// WithConfig returns a clone of the model with the given [ModelConfig].
func (m *Model) WithConfig(config ModelConfig) *Model {
	clone := m.clone()
	cfg := config
	if len(config.Extensions) > 0 {
		cfg.Extensions = make(map[string]Extension, len(config.Extensions))
		maps.Copy(cfg.Extensions, config.Extensions)
	}
	if len(config.Options) > 0 {
		cfg.Options = make(map[string]any, len(config.Options))
		maps.Copy(cfg.Options, config.Options)
	}
	clone.config = &cfg
	return clone
}

// WithMiddleware returns a clone of the model with the given middleware appended
// to the existing chain. Middleware is executed in the order it was added.
func (m *Model) WithMiddleware(mw ...Middleware) *Model {
	clone := m.clone()
	clone.middleware = append(clone.middleware, mw...)
	return clone
}

// WithExtension returns a clone of the model with the given provider-specific extension set.
func (m *Model) WithExtension(ext Extension) *Model {
	clone := m.clone()
	if clone.config == nil {
		clone.config = &ModelConfig{}
	}
	if clone.config.Extensions == nil {
		clone.config.Extensions = make(map[string]Extension)
	}
	clone.config.Extensions[ext.ExtensionID()] = ext
	return clone
}

// WithOption returns a clone of the model with the given key-value option set.
func (m *Model) WithOption(key string, value any) *Model {
	clone := m.clone()
	if clone.config == nil {
		clone.config = &ModelConfig{}
	}
	if clone.config.Options == nil {
		clone.config.Options = make(map[string]any)
	}
	clone.config.Options[key] = value
	return clone
}

// Evaluate evaluates one or more questions against the input context.
func (m *Model) Evaluate(ctx context.Context, input Input, questions ...Question) (*Response, error) {
	return m.EvaluateWithOptions(ctx, input, questions)
}

// EvaluateWithOptions evaluates questions against the input context with optional call options.
func (m *Model) EvaluateWithOptions(ctx context.Context, input Input, questions []Question, opts ...CallOption) (*Response, error) {
	if len(questions) == 0 {
		return nil, fmt.Errorf("decision: at least one question must be provided")
	}

	req := &Request{
		Model:     m.name,
		Input:     input,
		Questions: questions,
	}

	if m.config != nil {
		if len(m.config.Extensions) > 0 {
			req.Extensions = make(map[string]Extension, len(m.config.Extensions))
			maps.Copy(req.Extensions, m.config.Extensions)
		}
		if len(m.config.Options) > 0 {
			req.Options = make(map[string]any, len(m.config.Options))
			maps.Copy(req.Options, m.config.Options)
		}
	}

	for _, opt := range opts {
		opt(req)
	}

	var evaluator Evaluator = func(ctx context.Context, r *Request) (*Response, error) {
		return m.provider.Evaluate(ctx, r)
	}

	// Wrap with middleware in reverse order so the first added middleware is the outermost
	for i := len(m.middleware) - 1; i >= 0; i-- {
		evaluator = m.middleware[i](evaluator)
	}

	resp, err := evaluator(ctx, req)
	if err != nil {
		return nil, err
	}

	// Compute token cost if metrics and profile are available
	if resp.Metrics != nil && m.profile != nil {
		costs, total := m.profile.EstimateCost(resp.Metrics.Tokens)
		resp.Metrics.Cost = costs
		resp.Metrics.TotalCost = total
	}

	// Emit on_decision event if stream writer is present
	if sw, ok := stream.WriterFromContext(ctx); ok {
		sourceName := m.name
		if m.profile != nil && m.profile.Name != "" {
			sourceName = m.profile.Name
		}
		writeCtx := stream.WithMetadata(ctx, stream.Metadata{Source: "decision:" + sourceName})
		_ = sw.Write(writeCtx, stream.Event{
			Name: "on_decision",
			Data: resp,
		})
	}

	return resp, nil
}

// Check evaluates a single binary predicate question named "check" and returns its Answer.
func (m *Model) Check(ctx context.Context, input Input, instructions string, opts ...CallOption) (*Answer, error) {
	q := Predicate("check", instructions)
	resp, err := m.EvaluateWithOptions(ctx, input, []Question{q}, opts...)
	if err != nil {
		return nil, err
	}
	ans, ok := resp.Answer("check")
	if !ok {
		return nil, fmt.Errorf("decision: missing answer for question %q", "check")
	}
	return &ans, nil
}

// Choose evaluates a single categorical choice question named "choice" and returns its Answer.
func (m *Model) Choose(ctx context.Context, input Input, instructions string, choices []Option, opts ...CallOption) (*Answer, error) {
	q := Choice("choice", instructions, choices...)
	resp, err := m.EvaluateWithOptions(ctx, input, []Question{q}, opts...)
	if err != nil {
		return nil, err
	}
	ans, ok := resp.Answer("choice")
	if !ok {
		return nil, fmt.Errorf("decision: missing answer for question %q", "choice")
	}
	return &ans, nil
}

// Rate evaluates a single ordinal score question named "score" and returns its Answer.
func (m *Model) Rate(ctx context.Context, input Input, instructions string, levels []Level, opts ...CallOption) (*Answer, error) {
	q := Score("score", instructions, levels...)
	resp, err := m.EvaluateWithOptions(ctx, input, []Question{q}, opts...)
	if err != nil {
		return nil, err
	}
	ans, ok := resp.Answer("score")
	if !ok {
		return nil, fmt.Errorf("decision: missing answer for question %q", "score")
	}
	return &ans, nil
}

func (m *Model) clone() *Model {
	cp := *m
	if m.config != nil {
		cfg := *m.config
		if len(m.config.Extensions) > 0 {
			cfg.Extensions = make(map[string]Extension, len(m.config.Extensions))
			maps.Copy(cfg.Extensions, m.config.Extensions)
		}
		if len(m.config.Options) > 0 {
			cfg.Options = make(map[string]any, len(m.config.Options))
			maps.Copy(cfg.Options, m.config.Options)
		}
		cp.config = &cfg
	}
	if len(m.middleware) > 0 {
		cp.middleware = make([]Middleware, len(m.middleware))
		copy(cp.middleware, m.middleware)
	}
	return &cp
}
