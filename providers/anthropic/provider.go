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
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/stream"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
)

var (
	_ llm.Provider             = (*Provider)(nil)
	_ profile.Provider         = (*Provider)(nil)
	_ llm.ModelLister          = (*Provider)(nil)
	_ llm.DeferredToolsSupport = (*Provider)(nil)
)

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

	// Auto signals the provider to set top-level CacheControl, which automatically
	// applies a cache_control marker to the last cacheable block in the request.
	Auto bool
}

func (p PromptCaching) ExtensionID() string {
	return "anthropic.prompt_caching"
}

// ToolSearchStrategy defines the algorithm used by Anthropic's server-side tool search.
type ToolSearchStrategy string

const (
	ToolSearchRegex ToolSearchStrategy = "regex"
	ToolSearchBM25  ToolSearchStrategy = "bm25"
)

// ToolSearch is a request extension that enables Anthropic's built-in server-side tool search.
type ToolSearch struct {
	Strategy ToolSearchStrategy
}

func (t ToolSearch) ExtensionID() string {
	return "anthropic.tool_search"
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

// SupportsDeferredTools reports whether the Anthropic provider supports deferred tools for the model.
func (p *Provider) SupportsDeferredTools(model string) bool {
	return modelSupportsToolSearch(model)
}

// SupportsInlineTools reports whether the Anthropic provider natively supports mid-conversation inline tool definitions for the model.
func (p *Provider) SupportsInlineTools(model string) bool {
	return modelSupportsInlineTools(model)
}

// Stream converts request into an Anthropic chat request, initiates the streaming
// call, and returns an iterator that yields [message.AssistantChunk] values as
// tokens arrive from the Anthropic server.
func (p *Provider) Stream(ctx context.Context, request *llm.Request) (llm.StreamResponse, error) {
	body, err := toMessageNewParams(request)
	if err != nil {
		return nil, fmt.Errorf("failed to convert chat request: %w", err)
	}

	reqOpts := requestOptions(request)

	if sw, ok := stream.WriterFromContext(ctx); ok {
		_ = sw.Write(ctx, stream.Event{
			Name: "on_llm_request",
			Data: map[string]any{
				"provider": "anthropic",
				"payload":  body,
			},
		})
	}

	// Retrieve active span (can be used for provider-specific decoration)
	_ = trace.SpanFromContext(ctx)

	return func(yield func(message.AssistantChunk, error) bool) {
		serverToolIndices := make(map[int]bool)

		// Create the streaming request
		stream := p.client.Messages.NewStreaming(ctx, body, reqOpts...)

		for stream.Next() {
			event := stream.Current()
			chunk, shouldYield, err := handleStreamEvent(event, serverToolIndices)
			if err != nil {
				yield(message.AssistantChunk{}, err)
				return
			}
			if shouldYield {
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

func handleStreamEvent(
	event anthropic.MessageStreamEventUnion,
	serverToolIndices map[int]bool,
) (message.AssistantChunk, bool, error) {
	switch e := event.AsAny().(type) {
	case anthropic.ContentBlockStartEvent:
		// Emit the tool call identity chunk so the aggregator can open a pending entry.
		if cb, ok := e.ContentBlock.AsAny().(anthropic.ToolUseBlock); ok {
			return message.AssistantChunk{
				Content: []message.Block{&message.ToolCallChunk{
					Index: int(e.Index),
					ID:    cb.ID,
					Name:  cb.Name,
				}},
			}, true, nil
		} else if cb, ok := e.ContentBlock.AsAny().(anthropic.ServerToolUseBlock); ok {
			serverToolIndices[int(e.Index)] = true
			return message.AssistantChunk{
				Content: []message.Block{&message.ServerToolCallChunk{
					Index: int(e.Index),
					ID:    cb.ID,
					Name:  string(cb.Name),
				}},
			}, true, nil
		} else if cb, ok := e.ContentBlock.AsAny().(anthropic.ToolSearchToolResultBlock); ok {
			res := fromToolSearchToolResultBlock(cb)
			return message.AssistantChunk{
				Content: []message.Block{res},
			}, true, nil
		}
		return message.AssistantChunk{}, false, nil

	case anthropic.ContentBlockDeltaEvent:
		if delta, ok := e.Delta.AsAny().(anthropic.InputJSONDelta); ok {
			if serverToolIndices[int(e.Index)] {
				return message.AssistantChunk{
					Content: []message.Block{&message.ServerToolCallChunk{
						Index:     int(e.Index),
						ArgsChunk: delta.PartialJSON,
					}},
				}, true, nil
			}
			return message.AssistantChunk{
				Content: []message.Block{&message.ToolCallChunk{
					Index:     int(e.Index),
					ArgsChunk: delta.PartialJSON,
				}},
			}, true, nil
		}
		chunk, err := toAssistantChunk(event)
		if err != nil {
			return message.AssistantChunk{}, false, fmt.Errorf("failed to convert event to chunk: %w", err)
		}
		return chunk, true, nil

	default:
		chunk, err := toAssistantChunk(event)
		if err != nil {
			return message.AssistantChunk{}, false, fmt.Errorf("failed to convert event to chunk: %w", err)
		}
		return chunk, true, nil
	}
}

// ListProfiles returns all known Anthropic model profiles, with runtime
// overrides merged on top of the generated static catalog.
func (p *Provider) ListProfiles() []profile.Profile {
	merged := make(map[string]profile.Profile, len(staticProfiles))
	for k, v := range staticProfiles {
		if override, ok := staticProfileOverrides[k]; ok {
			merged[k] = override(v)
		} else {
			merged[k] = v
		}
	}
	p.overrides.Range(func(k, v any) bool {
		merged[k.(string)] = v.(profile.Profile)
		return true
	})
	result := make([]profile.Profile, 0, len(merged))
	for _, m := range merged {
		result = append(result, m)
	}
	return result
}

// GetProfile returns the profile for the given model ID, checking overrides
// before falling back to the generated static catalog.
func (p *Provider) GetProfile(id string) (profile.Profile, bool) {
	if v, ok := p.overrides.Load(id); ok {
		return v.(profile.Profile), true
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
func (p *Provider) SearchProfiles(query string) []profile.Profile {
	return profile.Search(p.ListProfiles(), query)
}

// OverrideProfile stores a custom profile for id, shadowing any static entry
// with the same ID. Useful for adding unreleased or private model variants at
// runtime without regenerating the static catalog.
func (p *Provider) OverrideProfile(id string, prof profile.Profile) {
	p.overrides.Store(id, prof)
}
