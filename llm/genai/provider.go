package loomgenai

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/stream"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genai"
)

var _ llm.Provider = (*Provider)(nil)

// ContextCache is an extension that specifies a pre-created context cache
// to use for a request.
type ContextCache struct {
	// ID is the name of the cached content resource.
	ID string
}

func (c ContextCache) ExtensionID() string {
	return "google-genai.context_cache"
}

// CacheCreation is an extension that configures the creation of a new
// context cache.
type CacheCreation struct {
	// DisplayName is a human-readable name for the cache.
	DisplayName string
	// TTL is the time-to-live for the cache.
	TTL time.Duration
}

func (c CacheCreation) ExtensionID() string {
	return "google-genai.cache_creation"
}

// Provider is the Google GenAI backend client that implements [llm.Provider].
//
//go:generate loomgen llm-profiles -provider=google -out=profiles.gen.go -pkg=loomgenai
type Provider struct {
	client    *genai.Client
	overrides sync.Map
}

// NewDefaultProvider creates a Google GenAI provider with default configuration, sourcing credentials from the environment.
func NewDefaultProvider(ctx context.Context) (*Provider, error) {
	return NewProvider(ctx, &genai.ClientConfig{
		HTTPClient: &http.Client{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
	})
}

// NewProvider creates a Google GenAI provider with the given configuration. The config can be used to customize credentials,
// timeouts, and other client options.
func NewProvider(ctx context.Context, config *genai.ClientConfig) (*Provider, error) {
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		}
	}
	client, err := genai.NewClient(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("genai: create client: %w", err)
	}

	return &Provider{client: client}, nil
}

// Name returns "google-genai", uniquely identifying this provider in a [llm.Registry].
func (p *Provider) Name() string {
	return "google-genai"
}

// Stream converts request into a Google GenAI GenerateContent request, initiates the
// streaming call, and returns an iterator that yields [message.AssistantChunk]
// values as tokens arrive from the Google GenAI API.
func (p *Provider) Stream(ctx context.Context, request *llm.Request) (llm.StreamResponse, error) {
	contents, config, err := toGenerateContentArgs(request)
	if err != nil {
		return nil, fmt.Errorf("genai: build request: %w", err)
	}

	if sw, ok := stream.WriterFromContext(ctx); ok {
		_ = sw.Write(ctx, stream.Event{
			Name: "on_llm_request",
			Data: map[string]any{
				"provider": "google-genai",
				"payload": map[string]any{
					"model":    request.Model,
					"contents": contents,
					"config":   config,
				},
			},
		})
	}

	// Gemini Specific Span Decoration
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("genai.api.type", "generate_content"),
	)

	return func(yield func(message.AssistantChunk, error) bool) {
		for resp, err := range p.client.Models.GenerateContentStream(ctx, request.Model, contents, config) {
			if err != nil {
				yield(message.AssistantChunk{}, fmt.Errorf("genai: stream: %w", err))
				return
			}

			chunk, err := toAssistantChunk(resp)
			if err != nil {
				yield(message.AssistantChunk{}, err)
				return
			}

			if !yield(chunk, nil) {
				return
			}
		}
	}, nil
}

// ListProfiles returns all known Google GenAI model profiles, with overrides
// merged on top of the generated static catalog.
func (p *Provider) ListProfiles() []llm.ModelProfile {
	merged := make(map[string]llm.ModelProfile, len(staticProfiles))
	maps.Copy(merged, staticProfiles)
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
	return m, ok
}

// SearchProfiles returns profiles whose ID or DisplayName contains query
// (case-insensitive).
func (p *Provider) SearchProfiles(query string) []llm.ModelProfile {
	return llm.SearchProfiles(p.ListProfiles(), query)
}

// OverrideProfile stores a custom profile for id, shadowing any static entry.
func (p *Provider) OverrideProfile(id string, profile llm.ModelProfile) {
	p.overrides.Store(id, profile)
}

// CreateCache creates a new Google GenAI context cache resource.
func (p *Provider) CreateCache(ctx context.Context, req *llm.Request) (string, error) {
	contents, config, err := toGenerateContentArgs(req)
	if err != nil {
		return "", fmt.Errorf("genai: build cache request: %w", err)
	}

	createConfig := &genai.CreateCachedContentConfig{
		Contents:          contents,
		SystemInstruction: config.SystemInstruction,
		Tools:             config.Tools,
		ToolConfig:        config.ToolConfig,
	}

	if ext, ok := req.Extensions[CacheCreation{}.ExtensionID()]; ok {
		if cc, ok := ext.(CacheCreation); ok {
			createConfig.DisplayName = cc.DisplayName
			createConfig.TTL = cc.TTL
		}
	}

	cache, err := p.client.Caches.Create(ctx, req.Model, createConfig)
	if err != nil {
		return "", fmt.Errorf("genai: create cache: %w", err)
	}

	return cache.Name, nil
}

// DeleteCache deletes an existing Google GenAI context cache resource.
func (p *Provider) DeleteCache(ctx context.Context, id string) error {
	_, err := p.client.Caches.Delete(ctx, id, nil)
	if err != nil {
		return fmt.Errorf("genai: delete cache: %w", err)
	}
	return nil
}
