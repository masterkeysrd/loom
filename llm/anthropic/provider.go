package loomanthropic

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
)

var _ llm.Provider = (*Provider)(nil)

const (
	// MetadataCache is the ID for the prompt caching message extension.
	MetadataCache = "anthropic.message_cache"
)

func init() {
	message.RegisterExtension(func() message.Extension {
		return &MessageCache{}
	})
}

// MessageCache is a message-level extension that signals the provider to
// apply a cache breakpoint at this specific message.
type MessageCache struct {
	// Enabled signals that this message should be a cache breakpoint.
	Enabled bool `json:"enabled"`
}

func (m MessageCache) ExtensionID() string {
	return MetadataCache
}

// PromptCaching is an extension that configures Anthropic's prompt caching behavior.
type PromptCaching struct {
	// CacheHeader signals the provider to cache the "header" of the request
	// (system prompt and tool definitions).
	CacheHeader bool
}

func (p PromptCaching) ExtensionID() string {
	return "anthropic.prompt_caching"
}

// Provider represents the Anthropic backend client for
// handling chat requests for the Loom application.
//
//go:generate loomgen llm-profiles -provider=anthropic -out=profiles.gen.go -pkg=loomanthropic
type Provider struct {
	client    *anthropic.Client
	overrides sync.Map
}

// NewDefaultProvider creates a [Provider] using the Anthropic client configured
// from the ANTHROPIC_API_KEY environment variable.
func NewDefaultProvider() (*Provider, error) {
	httpClient := &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
	client := anthropic.NewClient(option.WithHTTPClient(httpClient))
	return &Provider{
		client: &client,
	}, nil
}

// NewProvider creates a [Provider] using the provided Anthropic client. This can be used to
// share a client across multiple providers, or to use a client with custom configuration.
func NewProvider(client *anthropic.Client) *Provider {
	return &Provider{
		client: client,
	}
}

// Name returns "anthropic", uniquely identifying this provider in a [llm.Registry].
func (p *Provider) Name() string {
	return "anthropic"
}

// Stream converts request into an Anthropic chat request, initiates the streaming
// call, and returns an iterator that yields [message.AssistantChunk] values as
// tokens arrive from the Anthropic server.
func (p *Provider) Stream(ctx context.Context, request *llm.Request) (llm.StreamResponse, error) {
	body, err := toMessageNewParams(request)
	if err != nil {
		return nil, fmt.Errorf("failed to convert chat request: %w", err)
	}

	// Retrieve active span (can be used for provider-specific decoration)
	_ = trace.SpanFromContext(ctx)

	return func(yield func(message.AssistantChunk, error) bool) {
		// Create the streaming request
		stream := p.client.Messages.NewStreaming(ctx, body)

		for stream.Next() {
			event := stream.Current()

			switch e := event.AsAny().(type) {
			case anthropic.ContentBlockStartEvent:
				// Emit the tool call identity chunk so the aggregator can open a pending entry.
				if cb, ok := e.ContentBlock.AsAny().(anthropic.ToolUseBlock); ok {
					chunk := message.AssistantChunk{
						Content: []message.Block{&message.ToolCallChunk{
							Index: int(e.Index),
							ID:    cb.ID,
							Name:  cb.Name,
						}},
					}
					if !yield(chunk, nil) {
						return
					}
				}

			case anthropic.ContentBlockDeltaEvent:
				if delta, ok := e.Delta.AsAny().(anthropic.InputJSONDelta); ok {
					// Emit a partial-args chunk; the aggregator appends it to the right tool call.
					chunk := message.AssistantChunk{
						Content: []message.Block{&message.ToolCallChunk{
							Index:     int(e.Index),
							ArgsChunk: delta.PartialJSON,
						}},
					}
					if !yield(chunk, nil) {
						return
					}
				} else {
					chunk, err := toAssistantChunk(event)
					if err != nil {
						yield(message.AssistantChunk{}, fmt.Errorf("failed to convert event to chunk: %w", err))
						return
					}
					if !yield(chunk, nil) {
						return
					}
				}

			default:
				chunk, err := toAssistantChunk(event)
				if err != nil {
					yield(message.AssistantChunk{}, fmt.Errorf("failed to convert event to chunk: %w", err))
					return
				}
				if !yield(chunk, nil) {
					return
				}
			}
		}

		if stream.Err() != nil {
			yield(message.AssistantChunk{}, fmt.Errorf("failed to start chat stream: %w", stream.Err()))
			return
		}

	}, nil
}

// ListProfiles returns all known Anthropic model profiles, with runtime
// overrides merged on top of the generated static catalog.
func (p *Provider) ListProfiles() []llm.ModelProfile {
	merged := make(map[string]llm.ModelProfile, len(staticProfiles))
	for k, v := range staticProfiles {
		if override, ok := staticProfileOverrides[k]; ok {
			merged[k] = override(v)
		} else {
			merged[k] = v
		}
	}
	p.overrides.Range(func(k, v any) bool {
		merged[k.(string)] = v.(llm.ModelProfile)
		return true
	})
	result := make([]llm.ModelProfile, 0, len(merged))
	for _, m := range merged {
		result = append(result, m)
	}
	return result
}

// GetProfile returns the profile for the given model ID, checking overrides
// before falling back to the generated static catalog.
func (p *Provider) GetProfile(id string) (llm.ModelProfile, bool) {
	if v, ok := p.overrides.Load(id); ok {
		return v.(llm.ModelProfile), true
	}
	m, ok := staticProfiles[id]
	if ok {
		if override, hasOverride := staticProfileOverrides[id]; hasOverride {
			m = override(m)
		}
	}
	return m, ok
}

// SearchProfiles returns profiles whose ID or DisplayName contains query
// (case-insensitive).
func (p *Provider) SearchProfiles(query string) []llm.ModelProfile {
	return llm.SearchProfiles(p.ListProfiles(), query)
}

// OverrideProfile stores a custom profile for id, shadowing any static entry
// with the same ID. Useful for adding unreleased or private model variants at
// runtime without regenerating the static catalog.
func (p *Provider) OverrideProfile(id string, profile llm.ModelProfile) {
	p.overrides.Store(id, profile)
}
