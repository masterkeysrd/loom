package llm

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/stream"
	"github.com/masterkeysrd/loom/tool"
)

type ModelConfig struct {
	// MaxTokens is the maximum number of tokens that the model is allowed to use in the response, this is used for context window management and to avoid out of memory errors.
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
	// must adhere to. When set, providers that support structured outputs
	// will enforce this schema at the API level.
	ResponseSchema *jsonschema.Schema

	// Thinking specifies the thinking/reasoning configuration for the model.
	Thinking *ThinkingConfig

	// Extensions captures provider-specific configurations.
	Extensions map[string]Extension
}

type ThinkingConfig struct {
	// Budget is the maximum number of tokens for reasoning (Anthropic, Gemini).
	Budget int
	// Effort sets the reasoning intensity (OpenAI: low, medium, high; Gemini: ThinkingLevel; Anthropic: OutputConfig effort).
	Effort string
	// Adaptive enables adaptive thinking mode (Anthropic).
	Adaptive bool
}

// Streamer is a function that executes a chat completion request and returns
// a streaming response. It is the core functional unit wrapped by middleware.
type Streamer func(context.Context, *Request) (StreamResponse, error)

// Middleware is a function that wraps a [Streamer], allowing for cross-cutting
// concerns like logging, tracing, or request modification.
type Middleware func(Streamer) Streamer

// CallOption is a functional option that modifies an LLM [Request]
// for a single [Model.Invoke] or [Model.Stream] call.
type CallOption func(*Request)

// Model wraps a [Provider] and a specific model name, exposing both a
// blocking [Model.Invoke] method and a streaming [Model.Stream] method.
// It also forwards LLM chunks to any [stream.Writer] stored in the context,
// enabling transparent integration with the graph streaming layer.
type Model struct {
	name       string
	provider   Provider
	profile    *ModelProfile
	tools      []tool.Definition
	config     *ModelConfig
	middleware []Middleware
}

// NewModel constructs a [Model] that will call provider using the given model
// name (e.g. "qwen3-coder:30b"). It returns an error if the provider has a
// non-empty catalog and name is not found in it, preventing hallucinated or
// cross-provider model IDs from reaching the API.
func NewModel(provider Provider, name string, config *ModelConfig) (*Model, error) {
	var profile *ModelProfile
	if p, found := provider.GetProfile(name); found {
		profile = &p
	}

	if config == nil {
		config = &ModelConfig{}
		if profile != nil {
			config.MaxTokens = profile.Limits.Output
		}
	}

	m := &Model{
		name:       name,
		provider:   provider,
		profile:    profile,
		config:     config,
		middleware: make([]Middleware, 0),
	}

	// Apply telemetry middleware by default
	m.middleware = append(m.middleware, TelemetryMiddleware(provider))

	return m, nil
}

// BindTools registers tool definitions with the model. On every subsequent
// call the tool descriptors are advertised to the LLM so it can emit tool-call
// blocks. Multiple calls to BindTools accumulate definitions.
func (m *Model) BindTools(tools ...*tool.Tool) *Model {
	clone := m.clone()
	for _, t := range tools {
		clone.tools = append(clone.tools, t.Definition)
	}

	return clone
}

// BindToolDefs is a variant of BindTools that accepts tool definitions directly.
func (m *Model) BindToolDefs(defs ...tool.Definition) *Model {
	clone := m.clone()
	clone.tools = append(clone.tools, defs...)

	return clone
}

// WithConfig returns a clone of the model with the given [ModelConfig].
func (m *Model) WithConfig(config ModelConfig) *Model {
	clone := m.clone()
	clone.config = &config
	return clone
}

// WithMaxTokens returns a clone of the model with MaxTokens set.
func (m *Model) WithMaxTokens(max int) *Model {
	clone := m.clone()
	if clone.config == nil {
		clone.config = &ModelConfig{}
	}
	clone.config.MaxTokens = max
	return clone
}

// WithTemperature returns a clone of the model with Temperature set.
func (m *Model) WithTemperature(t float32) *Model {
	clone := m.clone()
	if clone.config == nil {
		clone.config = &ModelConfig{}
	}
	clone.config.Temperature = &t
	return clone
}

// WithTopP returns a clone of the model with TopP set.
func (m *Model) WithTopP(p float32) *Model {
	clone := m.clone()
	if clone.config == nil {
		clone.config = &ModelConfig{}
	}
	clone.config.TopP = &p
	return clone
}

// WithTopK returns a clone of the model with TopK set.
func (m *Model) WithTopK(k int) *Model {
	clone := m.clone()
	if clone.config == nil {
		clone.config = &ModelConfig{}
	}
	clone.config.TopK = &k
	return clone
}

// WithStop returns a clone of the model with Stop sequences set.
func (m *Model) WithStop(stop ...string) *Model {
	clone := m.clone()
	if clone.config == nil {
		clone.config = &ModelConfig{}
	}
	clone.config.Stop = stop
	return clone
}

// WithJSON returns a clone of the model with ResponseFormat set to "json_object".
func (m *Model) WithJSON() *Model {
	clone := m.clone()
	if clone.config == nil {
		clone.config = &ModelConfig{}
	}
	clone.config.ResponseFormat = "json_object"
	return clone
}

// WithStructuredOutput returns a clone of the model with a specific JSON schema
// for the response. Providers that support structured outputs will use this
// schema to constrain the LLM's output.
func (m *Model) WithStructuredOutput(schema *jsonschema.Schema) *Model {
	clone := m.clone()
	if clone.config == nil {
		clone.config = &ModelConfig{}
	}
	clone.config.ResponseSchema = schema
	return clone
}

// WithThinking returns a clone of the model with thinking enabled and a specific budget.
func (m *Model) WithThinking(budget int) *Model {
	clone := m.clone()
	if clone.config == nil {
		clone.config = &ModelConfig{}
	}
	if clone.config.Thinking == nil {
		clone.config.Thinking = &ThinkingConfig{}
	}
	clone.config.Thinking.Budget = budget
	return clone
}

// WithThinkingEffort returns a clone of the model with a specific thinking effort.
func (m *Model) WithThinkingEffort(effort string) *Model {
	clone := m.clone()
	if clone.config == nil {
		clone.config = &ModelConfig{}
	}
	if clone.config.Thinking == nil {
		clone.config.Thinking = &ThinkingConfig{}
	}
	clone.config.Thinking.Effort = effort
	return clone
}

// WithAdaptiveThinking returns a clone of the model with adaptive thinking enabled.
func (m *Model) WithAdaptiveThinking() *Model {
	clone := m.clone()
	if clone.config == nil {
		clone.config = &ModelConfig{}
	}
	if clone.config.Thinking == nil {
		clone.config.Thinking = &ThinkingConfig{}
	}
	clone.config.Thinking.Adaptive = true
	return clone
}

// WithMiddleware returns a clone of the model with the given middleware
// appended to the existing chain. Middleware is executed in the order it was
// added.
func (m *Model) WithMiddleware(mw ...Middleware) *Model {
	clone := m.clone()
	clone.middleware = append(clone.middleware, mw...)
	return clone
}

// WithExtension returns a clone of the model with the given provider-specific
// extension set.
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

// WithExtensionOption returns a CallOption that sets an extension for a
// single call.
func WithExtensionOption(ext Extension) CallOption {
	return func(r *Request) {
		if r.Extensions == nil {
			r.Extensions = make(map[string]Extension)
		}
		r.Extensions[ext.ExtensionID()] = ext
	}
}

// CreateCache creates a long-lived context cache resource using the model's
// configuration and the given messages. The provider must implement the
// [CacheManager] interface.
func (m *Model) CreateCache(ctx context.Context, messages []message.Message, opts ...CallOption) (string, error) {
	cm, ok := m.provider.(CacheManager)
	if !ok {
		return "", fmt.Errorf("llm: provider %q does not support explicit cache creation", m.provider.Name())
	}

	req := m.newRequest(messages, opts...)
	return cm.CreateCache(ctx, req)
}

// DeleteCache removes a previously created context cache resource. The provider
// must implement the [CacheManager] interface.
func (m *Model) DeleteCache(ctx context.Context, id string) error {
	cm, ok := m.provider.(CacheManager)
	if !ok {
		return fmt.Errorf("llm: provider %q does not support explicit cache management", m.provider.Name())
	}

	return cm.DeleteCache(ctx, id)
}

// Invoke sends messages to the model and blocks until the full response is
// assembled. It internally calls [Model.Stream] and aggregates all chunks into
// a single [message.Assistant] before returning.
func (m *Model) Invoke(ctx context.Context, messages []message.Message, opts ...CallOption) (*message.Assistant, error) {
	aggregator := message.NewAssistantAggregator()

	stream, error := m.Stream(ctx, messages, opts...)
	if error != nil {
		return nil, error
	}

	for chunk, err := range stream {
		if err != nil {
			return nil, err
		}

		aggregator.Add(&chunk)
	}

	msg, err := aggregator.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build assistant message: %w", err)
	}

	return msg, nil
}

// Stream sends messages to the model and returns an iterator over
// [message.AssistantChunk] values. Each chunk is also forwarded to the
// [stream.Writer] in ctx (if any) so callers upstream can emit real-time
// events without managing two separate data paths.
func (m *Model) Stream(ctx context.Context, messages []message.Message, opts ...CallOption) (StreamResponse, error) {
	req := m.newRequest(messages, opts...)

	// Create the base streamer that calls the provider
	var streamer Streamer = func(ctx context.Context, r *Request) (StreamResponse, error) {
		return m.provider.Stream(ctx, r)
	}

	// Wrap with middleware in reverse order so the first added middleware is the outermost
	for i := len(m.middleware) - 1; i >= 0; i-- {
		streamer = m.middleware[i](streamer)
	}

	sw, hasWriter := stream.WriterFromContext(ctx)
	if hasWriter {
		sourceName := m.name
		if m.profile != nil {
			sourceName = m.profile.Name
		}
		ctx = stream.WithMetadata(ctx, stream.Metadata{Source: "llm:" + sourceName})
	}

	resp, err := streamer(ctx, req)
	if err != nil {
		return nil, err
	}

	return func(yield func(message.AssistantChunk, error) bool) {
		for chunk, err := range resp {
			if err == nil && chunk.Metrics != nil && m.profile != nil {
				costs, total := m.profile.EstimateCost(chunk.Metrics.Tokens)
				chunk.Metrics.Cost = costs
				chunk.Metrics.TotalCost = total
			}

			if hasWriter && err == nil {
				_ = sw.Write(ctx, message.CloneAssistantChunk(chunk))
			}
			if !yield(chunk, err) {
				return
			}
		}
	}, nil
}

func (m *Model) newRequest(messages []message.Message, opts ...CallOption) *Request {
	req := &Request{
		Model:    m.name,
		Messages: messages,
		Tools:    m.tools,
	}

	if m.config != nil {
		req.MaxTokens = m.config.MaxTokens
		req.Temperature = m.config.Temperature
		req.TopP = m.config.TopP
		req.TopK = m.config.TopK
		req.PresencePenalty = m.config.PresencePenalty
		req.FrequencyPenalty = m.config.FrequencyPenalty
		req.Stop = m.config.Stop
		req.ResponseFormat = m.config.ResponseFormat
		req.ResponseSchema = m.config.ResponseSchema
		req.Thinking = m.config.Thinking

		if len(m.config.Extensions) > 0 {
			req.Extensions = make(map[string]Extension, len(m.config.Extensions))
			maps.Copy(req.Extensions, m.config.Extensions)
		}
	}

	for _, opt := range opts {
		opt(req)
	}

	return req
}

// clone creates a shallow copy of the model. The provider is shared between
// the original and the clone, but the tools slice is copied so that subsequent
// calls to BindTools on one model do not affect the other.
func (m *Model) clone() *Model {
	cp := *m
	if len(m.tools) > 0 {
		cp.tools = make([]tool.Definition, len(m.tools))
		copy(cp.tools, m.tools)
	}

	if m.config != nil {
		cfg := *m.config
		if m.config.Thinking != nil {
			th := *m.config.Thinking
			cfg.Thinking = &th
		}
		if len(m.config.Extensions) > 0 {
			cfg.Extensions = make(map[string]Extension, len(m.config.Extensions))
			maps.Copy(cfg.Extensions, m.config.Extensions)
		}
		cp.config = &cfg
	}

	if len(m.middleware) > 0 {
		cp.middleware = make([]Middleware, len(m.middleware))
		copy(cp.middleware, m.middleware)
	}

	return &cp
}

type Invoker interface {
	Invoke(ctx context.Context, messages []message.Message) (*message.Assistant, error)
}

// ParseModelName splits a model name into provider and model components. If no provider is
// specified, it defaults to "default". For example, "azure/gpt-4o" returns ("azure", "gpt-4o"),
// while "gpt-3.5" returns ("default", "gpt-3.5").
func ParseModelName(modelName string) (string, string, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return "", "", fmt.Errorf("model name cannot be empty")
	}

	parts := strings.SplitN(modelName, "/", 2)

	// Case: No provider specified (e.g., "gpt-4o")
	if len(parts) != 2 {
		return "default", modelName, nil
	}

	provider := strings.TrimSpace(parts[0])
	model := strings.TrimSpace(parts[1])

	if provider == "" || model == "" {
		return "", "", fmt.Errorf("invalid model name format: %q (expected 'provider/model')", modelName)
	}

	return provider, model, nil
}
