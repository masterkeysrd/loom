package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/tool"
	"github.com/masterkeysrd/loom/trace"
)

type ModelConfig struct {
	// MaxTokens is the maximum number of tokens that the model is allowed to use in the response, this is used for context window management and to avoid out of memory errors.
	MaxTokens int
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
