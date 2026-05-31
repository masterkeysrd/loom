package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/tool"
	"github.com/masterkeysrd/loom/trace"
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
}

type ThinkingConfig struct {
	// Budget is the maximum number of tokens for reasoning (Anthropic, Gemini).
	Budget int
	// Effort sets the reasoning intensity (OpenAI: low, medium, high; Gemini: ThinkingLevel; Anthropic: OutputConfig effort).
	Effort string
	// Adaptive enables adaptive thinking mode (Anthropic).
	Adaptive bool
}

// Model wraps a [Provider] and a specific model name, exposing both a
// blocking [Model.Invoke] method and a streaming [Model.Stream] method.
// It also forwards LLM chunks to any [StreamWriter] stored in the context,
// enabling transparent integration with the graph streaming layer.
type Model struct {
	name     string
	provider Provider
	tools    []tool.Definition
	config   *ModelConfig
}

// NewModel constructs a [Model] that will call provider using the given model
// name (e.g. "qwen3-coder:30b"). It returns an error if the provider has a
// non-empty catalog and name is not found in it, preventing hallucinated or
// cross-provider model IDs from reaching the API.
func NewModel(provider Provider, name string, config *ModelConfig) (*Model, error) {
	if config == nil {
		cfg, found := provider.GetConfig(name)
		if found {
			config = &cfg
		}
	}

	return &Model{
		name:     name,
		provider: provider,
		config:   config,
	}, nil
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

// Invoke sends messages to the model and blocks until the full response is
// assembled. It internally calls [Model.Stream] and aggregates all chunks into
// a single [message.Assistant] before returning.
func (m *Model) Invoke(ctx context.Context, messages []message.Message) (*message.Assistant, error) {
	aggregator := message.NewAssistantAggregator()

	stream, error := m.Stream(ctx, messages)
	if error != nil {
		return nil, error
	}

	for chunk, err := range stream {
		if err != nil {
			return nil, err
		}

		aggregator.Add(&chunk)
		blocks, blocksErr := aggregator.GetBlocks()
		aggregateText := ""
		if blocksErr == nil {
			aggregateText = message.Content(blocks).Text()
		}
		trace.Append(ctx, "model", "invoke_chunk", map[string]any{
			"chunk_text":     message.Content(chunk.Content).Text(),
			"aggregate_text": aggregateText,
			"done":           chunk.Done,
			"done_reason":    chunk.DoneReason,
			"metrics":        chunk.Metrics,
		})
	}

	msg, err := aggregator.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build assistant message: %w", err)
	}

	trace.Append(ctx, "model", "invoke_complete", map[string]any{
		"message_id":   msg.GetID(),
		"message_text": msg.GetContent().Text(),
		"metrics":      msg.Metrics,
	})

	return msg, nil
}

// Stream sends messages to the model and returns an iterator over
// [message.AssistantChunk] values. Each chunk is also forwarded to the
// [StreamWriter] in ctx (if any) so callers upstream can emit real-time
// events without managing two separate data paths.
func (m *Model) Stream(ctx context.Context, messages []message.Message) (StreamResponse, error) {
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
	}

	stream, err := m.provider.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	sw, hasWriter := StreamWriterFromContext(ctx)

	return func(yield func(message.AssistantChunk, error) bool) {
		for chunk, err := range stream {
			if err == nil {
				trace.Append(ctx, "model", "stream_chunk", map[string]any{
					"chunk_text":  message.Content(chunk.Content).Text(),
					"done":        chunk.Done,
					"done_reason": chunk.DoneReason,
					"metrics":     chunk.Metrics,
				})
			}
			if hasWriter && err == nil {
				_ = sw.WriteChunk(ctx, message.CloneAssistantChunk(chunk))
			}
			if !yield(chunk, err) {
				return
			}
		}
	}, nil
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
		cp.config = &cfg
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
