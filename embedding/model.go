package embedding

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/stream"
)

// ModelConfig holds default configuration options and extensions for an embedding model.
type ModelConfig struct {
	// Dimensions specifies the optional output vector dimensionality.
	Dimensions *int `json:"dimensions,omitempty"`

	// TaskType specifies the default downstream task type for embeddings.
	TaskType TaskType `json:"task_type,omitempty"`

	// Extensions captures provider-specific configurations.
	Extensions map[string]Extension `json:"extensions,omitempty"`

	// Options captures arbitrary provider-specific key-value options.
	Options map[string]any `json:"options,omitempty"`
}

// Model wraps a [Provider] and specific model name for generating embeddings.
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
		return nil, fmt.Errorf("embedding: provider cannot be nil")
	}
	if name == "" {
		return nil, fmt.Errorf("embedding: model name cannot be empty")
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
		config = cloneModelConfig(config)
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

// Dimensions returns the configured output vector dimensionality, falling back
// to the model profile's default [profile.Limits.Dimensions] if available.
// Returns 0 if unknown.
func (m *Model) Dimensions() int {
	if m.config != nil && m.config.Dimensions != nil && *m.config.Dimensions > 0 {
		return *m.config.Dimensions
	}
	if m.profile != nil && m.profile.Limits.Dimensions > 0 {
		return m.profile.Limits.Dimensions
	}
	return 0
}

// WithConfig returns a clone of the model with the given [ModelConfig].
func (m *Model) WithConfig(config ModelConfig) *Model {
	clone := m.clone()
	clone.config = cloneModelConfig(&config)
	return clone
}

// WithDimensions returns a clone of the model configured with the given output dimensionality.
func (m *Model) WithDimensions(dims int) *Model {
	clone := m.clone()
	if clone.config == nil {
		clone.config = &ModelConfig{}
	}
	clone.config.Dimensions = &dims
	return clone
}

// WithTaskType returns a clone of the model configured with the given default [TaskType].
func (m *Model) WithTaskType(taskType TaskType) *Model {
	clone := m.clone()
	if clone.config == nil {
		clone.config = &ModelConfig{}
	}
	clone.config.TaskType = taskType
	return clone
}

// WithMiddleware returns a clone of the model with the given middleware appended
// to the existing chain. Middleware is executed in the order it was added.
func (m *Model) WithMiddleware(mw ...Middleware) *Model {
	clone := m.clone()
	clone.middleware = append(clone.middleware, mw...)
	return clone
}

// WithProfile returns a clone of the model that uses p for cost estimation and
// profile-driven defaults. Passing nil clears the profile.
func (m *Model) WithProfile(p *profile.Profile) *Model {
	clone := m.clone()
	clone.profile = p
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

// Embed generates embeddings for one or more [Input] items with optional call options.
func (m *Model) Embed(ctx context.Context, inputs []Input, opts ...CallOption) (*Response, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("embedding: at least one input must be provided")
	}

	req := &Request{
		Model:  m.name,
		Inputs: inputs,
	}

	if m.config != nil {
		if m.config.Dimensions != nil {
			dims := *m.config.Dimensions
			req.Dimensions = &dims
		}
		if m.config.TaskType != TaskUnspecified {
			req.TaskType = m.config.TaskType
		}
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

	var embedder Embedder = func(ctx context.Context, r *Request) (*Response, error) {
		return m.provider.Embed(ctx, r)
	}

	// Wrap with middleware in reverse order so the first added middleware is the outermost
	for _, v := range slices.Backward(m.middleware) {
		embedder = v(embedder)
	}

	resp, err := embedder(ctx, req)
	if err != nil {
		return nil, err
	}

	// Compute token cost if metrics and profile are available
	if resp.Metrics != nil && m.profile != nil {
		est := m.profile.Estimate(resp.Metrics.Tokens)
		resp.Metrics.Cost = est.Details
		resp.Metrics.TotalCost = est.Total
		resp.Metrics.CostTier = est.Tier
		resp.Metrics.Priced = est.Priced
	}

	// Emit on_embedding event if stream writer is present
	if sw, ok := stream.WriterFromContext(ctx); ok {
		sourceName := m.name
		if m.profile != nil && m.profile.Name != "" {
			sourceName = m.profile.Name
		}
		writeCtx := stream.WithMetadata(ctx, stream.Metadata{Source: "embedding:" + sourceName})
		_ = sw.Write(writeCtx, stream.Event{
			Name: "on_embedding",
			Data: resp,
		})
	}

	return resp, nil
}

// EmbedInput generates an embedding vector for a single [Input].
func (m *Model) EmbedInput(ctx context.Context, input Input, opts ...CallOption) (Vector, error) {
	resp, err := m.Embed(ctx, []Input{input}, opts...)
	if err != nil {
		return nil, err
	}
	vec := resp.First()
	if vec == nil {
		return nil, fmt.Errorf("embedding: empty embeddings in response")
	}
	return vec, nil
}

// EmbedQuery generates an embedding vector for a single search or retrieval query string.
// If no TaskType is configured on the model or overridden via CallOption, it defaults to [TaskRetrievalQuery].
func (m *Model) EmbedQuery(ctx context.Context, query string, opts ...CallOption) (Vector, error) {
	callOpts := m.withDefaultTaskType(TaskRetrievalQuery, opts)
	return m.EmbedInput(ctx, Text(query), callOpts...)
}

// EmbedDocuments generates embedding vectors for a batch of document strings.
// If no TaskType is configured on the model or overridden via CallOption, it defaults to [TaskRetrievalDocument].
func (m *Model) EmbedDocuments(ctx context.Context, docs []string, opts ...CallOption) ([]Vector, error) {
	callOpts := m.withDefaultTaskType(TaskRetrievalDocument, opts)
	resp, err := m.Embed(ctx, Texts(docs...), callOpts...)
	if err != nil {
		return nil, err
	}
	return resp.Embeddings, nil
}

func (m *Model) withDefaultTaskType(defaultTask TaskType, opts []CallOption) []CallOption {
	if m.config != nil && m.config.TaskType != TaskUnspecified {
		return opts
	}
	callOpts := make([]CallOption, 0, len(opts)+1)
	callOpts = append(callOpts, WithTaskTypeOption(defaultTask))
	callOpts = append(callOpts, opts...)
	return callOpts
}

func (m *Model) clone() *Model {
	cp := *m
	if m.config != nil {
		cp.config = cloneModelConfig(m.config)
	}
	if len(m.middleware) > 0 {
		cp.middleware = make([]Middleware, len(m.middleware))
		copy(cp.middleware, m.middleware)
	}
	return &cp
}

func cloneModelConfig(config *ModelConfig) *ModelConfig {
	cfg := *config
	if config.Dimensions != nil {
		dims := *config.Dimensions
		cfg.Dimensions = &dims
	}
	if len(config.Extensions) > 0 {
		cfg.Extensions = make(map[string]Extension, len(config.Extensions))
		maps.Copy(cfg.Extensions, config.Extensions)
	}
	if len(config.Options) > 0 {
		cfg.Options = make(map[string]any, len(config.Options))
		maps.Copy(cfg.Options, config.Options)
	}
	return &cfg
}
