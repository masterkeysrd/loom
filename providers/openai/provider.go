package loomopenai

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/stream"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	_ llm.Provider     = (*Provider)(nil)
	_ profile.Provider = (*Provider)(nil)
)

// PromptCache is an extension that configures OpenAI's prompt caching behavior.
type PromptCache struct {
	// Key is a string hint used to optimize cache bucketing
	// for similar requests (e.g. per-user or per-session).
	Key string

	// Retention specifies the retention policy for the prompt
	// cache. Use "24h" to enable extended caching.
	Retention string
}

func (p PromptCache) ExtensionID() string {
	return "openai.prompt_cache"
}

// Provider represents the OpenAI backend client for
// handling chat requests for the Loom application.
//
//go:generate loomgen llm-profiles -provider=openai -out=profiles.gen.go -pkg=loomopenai
type Provider struct {
	client         *openai.Client
	overrides      sync.Map
	defaultAPIMode APIMode
}

// NewDefaultProvider creates a [Provider] using the OpenAI client configured
// from the OPENAI_API_KEY environment variable.
func NewDefaultProvider(opts ...Option) (*Provider, error) {
	httpClient := &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
	client := openai.NewClient(option.WithHTTPClient(httpClient))
	p := &Provider{
		client: &client,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// NewProvider creates a [Provider] using the provided OpenAI client.
func NewProvider(client *openai.Client, opts ...Option) *Provider {
	p := &Provider{
		client: client,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name returns "openai", uniquely identifying this provider in a [llm.Registry].
func (p *Provider) Name() string {
	return "openai"
}

var responsesAPIOnlyPrefixes = []string{
	"gpt-5-pro",
	"gpt-5.2-pro",
	"gpt-5.4-pro",
	"gpt-5.5-pro",
	"gpt-5.6-sol",
}

func modelPrefersResponsesAPI(modelName string, hasTools bool) bool {
	lower := strings.ToLower(modelName)
	for _, prefix := range responsesAPIOnlyPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if strings.Contains(lower, "codex") ||
		strings.Contains(lower, "deep-research") ||
		strings.HasPrefix(lower, "computer-use") ||
		strings.Contains(lower, "cyber") ||
		strings.Contains(lower, "rosalind") {
		return true
	}
	if strings.HasPrefix(lower, "gpt-6") && hasTools {
		return true
	}
	return false
}

func (p *Provider) shouldUseResponsesAPI(req *llm.Request) (bool, ResponsesOptions) {
	var opts ResponsesOptions
	if ext, ok := req.Extensions[ResponsesExtensionID]; ok {
		if ro, ok := ext.(ResponsesOptions); ok {
			opts = ro
		}
	}

	// 1. Explicit request-level flag
	if opts.Enabled {
		return true, opts
	}

	// 2. Feature-based triggers (built-in tools, state continuation)
	if opts.WebSearch || opts.FileSearch != nil || opts.CodeInterpreter ||
		opts.UsePreviousResponseID || opts.PreviousResponseID != "" {
		return true, opts
	}

	// 3. Explicit provider-level mode
	if p.defaultAPIMode == APIModeResponses {
		return true, opts
	}
	if p.defaultAPIMode == APIModeChatCompletions {
		return false, opts
	}

	// 4. Model-based auto-detection (models that require or prefer Responses API)
	if modelPrefersResponsesAPI(req.Model, len(req.Tools) > 0) {
		return true, opts
	}

	return false, opts
}

// Stream converts request into an OpenAI chat request, initiates the streaming
// call, and returns an iterator that yields [message.AssistantChunk] values as
// tokens arrive from the OpenAI server.
func (p *Provider) Stream(ctx context.Context, request *llm.Request) (llm.StreamResponse, error) {
	if useResponses, opts := p.shouldUseResponsesAPI(request); useResponses {
		return p.streamResponses(ctx, request, opts)
	}

	params, err := toChatCompletionNewParams(request)
	if err != nil {
		return nil, fmt.Errorf("failed to convert chat request: %w", err)
	}

	if sw, ok := stream.WriterFromContext(ctx); ok {
		_ = sw.Write(ctx, stream.Event{
			Name: "on_llm_request",
			Data: map[string]any{
				"provider": "openai",
				"payload":  params,
			},
		})
	}

	// OpenAI Specific Span Decoration
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("openai.api.type", "chat_completions"),
	)
	if params.ServiceTier != "" {
		span.SetAttributes(attribute.String("openai.request.service_tier", string(params.ServiceTier)))
	}

	return func(yield func(message.AssistantChunk, error) bool) {
		stream := p.client.Chat.Completions.NewStreaming(ctx, params)

		for stream.Next() {
			chunk := stream.Current()
			assistantChunk, err := toAssistantChunk(&chunk)
			if err != nil {
				yield(message.AssistantChunk{}, fmt.Errorf("failed to convert assistant chunk: %w", err))
				return
			}
			if !yield(assistantChunk, nil) {
				return
			}
		}

		if err := stream.Err(); err != nil {
			yield(message.AssistantChunk{}, fmt.Errorf("stream error: %w", err))
		}
	}, nil
}

// ListProfiles returns all known OpenAI model profiles.
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

// GetProfile returns the profile for the given model ID.
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

// SearchProfiles returns profiles whose ID or DisplayName contains query.
func (p *Provider) SearchProfiles(query string) []profile.Profile {
	return profile.Search(p.ListProfiles(), query)
}

// OverrideProfile stores a custom profile for id.
func (p *Provider) OverrideProfile(id string, prof profile.Profile) {
	p.overrides.Store(id, prof)
}
