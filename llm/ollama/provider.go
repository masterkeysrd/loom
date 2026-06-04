package loomollama

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/ollama/ollama/api"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var _ llm.Provider = (*Provider)(nil)

const defaultContextLength = 4096

// Provider represents the Ollama backend client for
// handling chat requests for the Loom application.
type Provider struct {
	client    *api.Client
	overrides sync.Map
}

// NewDefaultProvider creates a [Provider] using the Ollama client configured
// from environment variables (OLLAMA_HOST, etc.).
func NewDefaultProvider() (*Provider, error) {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = "http://127.0.0.1:11434"
	}

	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("failed to parse OLLAMA_HOST: %w", err)
	}

	httpClient := &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}

	return &Provider{
		client: api.NewClient(u, httpClient),
	}, nil
}

// Name returns "ollama", uniquely identifying this provider in a [llm.Registry].
func (p *Provider) Name() string {
	return "ollama"
}

// Stream converts request into an Ollama chat request, initiates the streaming
// call, and returns an iterator that yields [message.AssistantChunk] values as
// tokens arrive from the Ollama server.
func (p *Provider) Stream(ctx context.Context, request *llm.Request) (llm.StreamResponse, error) {
	ollamaRequest, err := toChatRequest(request)
	if err != nil {
		return nil, fmt.Errorf("failed to convert chat request: %w", err)
	}

	// Ollama Specific Span Decoration
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("ollama.api.type", "chat"),
	)

	{
		file, err := os.Create(fmt.Sprintf("./logs/ollama_request_%d.json", time.Now().Unix()))
		if err != nil {
			return nil, fmt.Errorf("failed to create debug file: %w", err)
		}
		defer file.Close()

		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(ollamaRequest); err != nil {
			return nil, fmt.Errorf("failed to write debug file: %w", err)
		}
	}

	return func(yield func(message.AssistantChunk, error) bool) {
		callback := func(resp api.ChatResponse) error {
			chunk, err := toAssistantChunk(resp)
			if err != nil {
				return fmt.Errorf("failed to convert chat response: %w", err)
			}
			if !yield(chunk, nil) {
				return fmt.Errorf("streaming stopped by consumer")
			}
			return nil
		}

		if err := p.client.Chat(ctx, ollamaRequest, callback); err != nil {
			yield(message.AssistantChunk{}, fmt.Errorf("failed to start chat stream: %w", err))
		}
	}, nil
}

// ListProfiles returns all known Ollama model profiles. It first queries the
// local Ollama instance for installed models, registering any new ones as
// skeleton profiles in the runtime cache.
func (p *Provider) ListProfiles() []llm.ModelProfile {
	// Discover models via tags
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if tags, err := p.client.List(ctx); err == nil {
		for _, m := range tags.Models {
			id := m.Name
			if _, ok := p.overrides.Load(id); !ok {
				if _, ok := staticProfiles[id]; !ok {
					// Add a skeleton profile so it shows up in searches.
					// Full metadata will be fetched on first GetProfile.
					p.overrides.Store(id, llm.ModelProfile{
						ID:     id,
						Name:   id,
						Family: m.Details.Family,
					})
				}
			}
		}
	}

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

// GetProfile returns the profile for the given model ID. It checks overrides
// and the static catalog first; if not found, it attempts to fetch full
// metadata (context window, capabilities) from the Ollama API and caches the
// result.
func (p *Provider) GetProfile(id string) (llm.ModelProfile, bool) {
	if v, ok := p.overrides.Load(id); ok {
		profile := v.(llm.ModelProfile)
		// If it's just a skeleton (missing limits), try to upgrade it
		if profile.Limits.Context == 0 {
			if upgraded, err := p.fetchAndCacheProfile(id); err == nil {
				return upgraded, true
			}
		}
		return profile, true
	}
	if m, ok := staticProfiles[id]; ok {
		return m, true
	}

	// Lazy fetch full details if not even a skeleton exists
	if profile, err := p.fetchAndCacheProfile(id); err == nil {
		return profile, true
	}

	return llm.ModelProfile{}, false
}

func (p *Provider) fetchAndCacheProfile(id string) (llm.ModelProfile, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := p.client.Show(ctx, &api.ShowRequest{Model: id})
	if err != nil {
		return llm.ModelProfile{}, err
	}

	profile := toModelProfile(id, resp)
	p.overrides.Store(id, profile)
	return profile, nil
}

func toModelProfile(id string, resp *api.ShowResponse) llm.ModelProfile {
	p := llm.ModelProfile{
		ID:     id,
		Name:   id,
		Family: resp.Details.Family,
		Capabilities: llm.Capabilities{
			Temperature: true,
			ToolCall:    true, // Assume true for modern Ollama models
		},
	}

	// Map modalities and capabilities
	for _, cap := range resp.Capabilities {
		switch string(cap) {
		case "vision":
			p.Modalities.Inputs = append(p.Modalities.Inputs, llm.ModalityImage)
			p.Capabilities.Attachment = true
		}
	}

	// Always add text modality
	p.Modalities.Inputs = append(p.Modalities.Inputs, llm.ModalityText)
	p.Modalities.Outputs = append(p.Modalities.Outputs, llm.ModalityText)

	// Context window discovery:
	// 1. Try to parse from Parameters string (Modelfile overrides)
	// 2. Try to find in ModelInfo (Model architecture defaults)
	// 3. Fallback to defaultContextLength (one of Ollama's common base defaults)
	p.Limits.Context = defaultContextLength

	// Parse parameters (e.g., "num_ctx 2048")
	for line := range strings.SplitSeq(resp.Parameters, "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[0] == "num_ctx" {
			if val, err := strconv.Atoi(parts[1]); err == nil {
				p.Limits.Context = val
			}
		}
	}

	// ModelInfo usually has the more accurate hardware-derived limit
	for k, v := range resp.ModelInfo {
		if strings.HasSuffix(k, ".context_length") {
			if f, ok := v.(float64); ok {
				p.Limits.Context = int(f)
				break
			}
			if i, ok := v.(int); ok {
				p.Limits.Context = i
				break
			}
			if i64, ok := v.(int64); ok {
				p.Limits.Context = int(i64)
				break
			}
		}
	}

	return p
}

// SearchProfiles returns profiles whose ID or DisplayName contains query
// (case-insensitive).
func (p *Provider) SearchProfiles(query string) []llm.ModelProfile {
	return llm.SearchProfiles(p.ListProfiles(), query)
}

// OverrideProfile stores a custom profile for id, shadowing any static entry.
// This is also the mechanism for registering locally-pulled Ollama models so
// that profile metadata (context window, tool support) is available at runtime.
func (p *Provider) OverrideProfile(id string, profile llm.ModelProfile) {
	p.overrides.Store(id, profile)
}
