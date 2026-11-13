package loomollama

import (
	"context"
	"errors"
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
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/stream"
	"github.com/ollama/ollama/api"
	"github.com/ollama/ollama/types/model"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	_ llm.Provider     = (*Provider)(nil)
	_ profile.Provider = (*Provider)(nil)
	_ llm.ModelLister  = (*Provider)(nil)
)

const defaultContextLength = 4096

// Provider represents the Ollama backend client for
// handling chat requests for the Loom application.
type Provider struct {
	client     *api.Client
	baseURL    *url.URL
	discovered sync.Map
	overrides  sync.Map
}

func (p *Provider) NewProvider(client *api.Client, baseURL *url.URL) *Provider {
	return &Provider{
		client:  client,
		baseURL: baseURL,
	}
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
		client:  api.NewClient(u, httpClient),
		baseURL: u,
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

	if _, hasCtx := ollamaRequest.Options["num_ctx"]; !hasCtx {
		if prof, ok := p.GetProfile(request.Model); ok && prof.Limits.Context > 0 {
			ollamaRequest.Options["num_ctx"] = prof.Limits.Context
		}
	}

	if sw, ok := stream.WriterFromContext(ctx); ok {
		_ = sw.Write(ctx, stream.Event{
			Name: "on_llm_request",
			Data: map[string]any{
				"provider": "ollama",
				"payload":  ollamaRequest,
			},
		})
	}

	// Ollama Specific Span Decoration
	span := trace.SpanFromContext(ctx)
	attrs := []attribute.KeyValue{
		attribute.String("ollama.api.type", "chat"),
	}
	if p.baseURL != nil {
		attrs = append(attrs, attribute.String("server.address", p.baseURL.Hostname()))
		if portStr := p.baseURL.Port(); portStr != "" {
			if port, err := strconv.Atoi(portStr); err == nil {
				attrs = append(attrs, attribute.Int("server.port", port))
			}
		} else if p.baseURL.Scheme == "https" {
			attrs = append(attrs, attribute.Int("server.port", 443))
		} else if p.baseURL.Scheme == "http" {
			attrs = append(attrs, attribute.Int("server.port", 80))
		}
	}
	span.SetAttributes(attrs...)

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

// ListModels queries the Ollama /api/tags endpoint for installed models, updates
// the runtime discovered cache, and returns the resulting model profiles (with
// any static or explicit OverrideProfile entries applied).
func (p *Provider) ListModels(ctx context.Context) ([]profile.Profile, error) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("ollama.api.type", "tags"))

	tags, err := p.client.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("ollama: list models: %w", err)
	}

	result := make([]profile.Profile, 0, len(tags.Models))
	for _, m := range tags.Models {
		id := m.Name
		live := toListModelProfile(m)
		if prev, ok := p.discovered.Load(id); ok {
			// Preserve previously fetched Show metadata (limits, reasoning options).
			live = profile.Merge(prev.(profile.Profile), live)
		}
		p.discovered.Store(id, live)

		result = append(result, p.resolveCatalogEntry(id, live))
	}
	return result, nil
}

// GetModel queries the Ollama /api/show endpoint for detailed model metadata
// (capabilities, context length, embedding dimensions, and thinking options),
// updates the runtime discovered cache, and returns the resolved profile.
func (p *Provider) GetModel(ctx context.Context, id string) (profile.Profile, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return profile.Profile{}, errors.New("ollama: model id is required")
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("ollama.api.type", "show"),
		attribute.String("ollama.model", id),
	)

	prof, err := p.fetchAndCacheProfile(ctx, id)
	if err != nil {
		return profile.Profile{}, fmt.Errorf("ollama: get model %q: %w", id, err)
	}
	return p.resolveCatalogEntry(id, prof), nil
}

func (p *Provider) resolveCatalogEntry(id string, live profile.Profile) profile.Profile {
	if v, ok := p.overrides.Load(id); ok {
		return v.(profile.Profile)
	}
	if base, ok := staticProfiles[id]; ok {
		return profile.Merge(base, live)
	}
	return live
}

// ListProfiles returns all known Ollama model profiles. It first queries the
// local Ollama instance for installed models, registering any new ones in the
// runtime discovered cache.
func (p *Provider) ListProfiles() []profile.Profile {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _ = p.ListModels(ctx)

	merged := make(map[string]profile.Profile, len(staticProfiles))
	maps.Copy(merged, staticProfiles)
	p.discovered.Range(func(k, v any) bool {
		id := k.(string)
		live := v.(profile.Profile)
		if base, ok := merged[id]; ok {
			merged[id] = profile.Merge(base, live)
		} else {
			merged[id] = live
		}
		return true
	})
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

// GetProfile returns the profile for the given model ID. It checks overrides
// and the static catalog first; if not found (or only a tag skeleton exists),
// it attempts to fetch full metadata (context window, capabilities) from the
// Ollama API and caches the result.
func (p *Provider) GetProfile(id string) (profile.Profile, bool) {
	if v, ok := p.overrides.Load(id); ok {
		return v.(profile.Profile), true
	}
	if m, ok := staticProfiles[id]; ok {
		return m, true
	}
	if v, ok := p.discovered.Load(id); ok {
		prof := v.(profile.Profile)
		if prof.Limits.Context == 0 {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if upgraded, err := p.fetchAndCacheProfile(ctx, id); err == nil {
				return upgraded, true
			}
		}
		return prof, true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if prof, err := p.fetchAndCacheProfile(ctx, id); err == nil {
		return prof, true
	}

	return profile.Profile{}, false
}

func (p *Provider) fetchAndCacheProfile(ctx context.Context, id string) (profile.Profile, error) {
	resp, err := p.client.Show(ctx, &api.ShowRequest{Model: id})
	if err != nil {
		return profile.Profile{}, err
	}

	prof := toModelProfile(id, resp)
	p.discovered.Store(id, prof)
	return prof, nil
}

func toListModelProfile(m api.ListModelResponse) profile.Profile {
	id := m.Name
	prof := profile.Profile{
		ID:          id,
		Name:        id,
		Family:      m.Details.Family,
		OpenWeights: true,
		Capabilities: profile.Capabilities{
			Completion:  true,
			Temperature: true,
			ToolCall:    true,
		},
	}
	if !m.ModifiedAt.IsZero() {
		prof.LastUpdated = m.ModifiedAt.Format(time.RFC3339)
	}
	applyCapabilities(&prof, m.Capabilities)
	return prof
}

func toModelProfile(id string, resp *api.ShowResponse) profile.Profile {
	p := profile.Profile{
		ID:          id,
		Name:        id,
		Family:      resp.Details.Family,
		OpenWeights: true, // Ollama runs open-weights models
		Capabilities: profile.Capabilities{
			Completion:  true, // Assume true for modern Ollama models by default
			Temperature: true,
			ToolCall:    true, // Assume true for modern Ollama models by default
		},
	}

	if !resp.ModifiedAt.IsZero() {
		p.LastUpdated = resp.ModifiedAt.Format(time.RFC3339)
	}

	applyCapabilities(&p, resp.Capabilities)

	if opts := toReasoningOptions(resp.Thinking); len(opts) > 0 {
		p.Capabilities.Reasoning = true
		p.Capabilities.ReasoningOptions = opts
	}

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
			if val, ok := toInt(v); ok {
				p.Limits.Context = val
			}
		}
		if p.Capabilities.Embedding && strings.HasSuffix(k, ".embedding_length") {
			if val, ok := toInt(v); ok {
				p.Limits.Dimensions = val
			}
		}
	}

	return p
}

func applyCapabilities(p *profile.Profile, caps []model.Capability) {
	if len(caps) > 0 {
		p.Capabilities.Completion = false
		p.Capabilities.ToolCall = false

		for _, cap := range caps {
			switch string(cap) {
			case "completion":
				p.Capabilities.Completion = true
			case "decision":
				p.Capabilities.Decision = true
			case "embedding":
				p.Capabilities.Embedding = true
			case "tools":
				p.Capabilities.ToolCall = true
			case "vision":
				p.Modalities.Inputs = append(p.Modalities.Inputs, profile.ModalityImage)
				p.Capabilities.Attachment = true
			case "thinking":
				p.Capabilities.Reasoning = true
			}
		}
	}

	// Always add text modality (or embedding output for embedding-only models)
	p.Modalities.Inputs = append(p.Modalities.Inputs, profile.ModalityText)
	if p.Capabilities.Embedding && !p.Capabilities.Completion {
		p.Modalities.Outputs = append(p.Modalities.Outputs, profile.ModalityEmbedding)
	} else {
		p.Modalities.Outputs = append(p.Modalities.Outputs, profile.ModalityText)
	}
}

func toReasoningOptions(t *model.Thinking) []profile.ReasoningOption {
	if t == nil || len(t.Values) == 0 {
		return nil
	}

	var (
		hasFalse bool
		hasTrue  bool
		efforts  []string
	)

	for _, v := range t.Values {
		switch val := v.(type) {
		case bool:
			if val {
				hasTrue = true
			} else {
				hasFalse = true
			}
		case string:
			if val != "" {
				efforts = append(efforts, val)
			}
		}
	}

	var opts []profile.ReasoningOption

	if len(efforts) == 0 {
		if hasTrue {
			def := "false"
			if b, ok := t.Default.(bool); ok && b {
				def = "true"
			}
			opts = append(opts, profile.ReasoningOption{
				Type:    "toggle",
				Default: def,
			})
		}
		return opts
	}

	// If booleans are present alongside named effort levels, the model supports
	// toggling thinking on/off in addition to selecting an effort level.
	if hasFalse || hasTrue {
		toggleDef := "true"
		if b, ok := t.Default.(bool); ok && !b {
			toggleDef = "false"
		}
		opts = append(opts, profile.ReasoningOption{
			Type:    "toggle",
			Default: toggleDef,
		})
	}

	var effortDef string
	if s, ok := t.Default.(string); ok {
		effortDef = s
	}
	opts = append(opts, profile.ReasoningOption{
		Type:    "effort",
		Values:  efforts,
		Default: effortDef,
	})

	return opts
}

func toInt(v any) (int, bool) {
	switch val := v.(type) {
	case float64:
		return int(val), true
	case int:
		return val, true
	case int64:
		return int(val), true
	default:
		return 0, false
	}
}

// SearchProfiles returns profiles whose ID or DisplayName contains query
// (case-insensitive).
func (p *Provider) SearchProfiles(query string) []profile.Profile {
	return profile.Search(p.ListProfiles(), query)
}

// OverrideProfile stores a custom profile for id, shadowing any static entry.
// This is also the mechanism for registering locally-pulled Ollama models so
// that profile metadata (context window, tool support) is available at runtime.
func (p *Provider) OverrideProfile(id string, prof profile.Profile) {
	p.overrides.Store(id, prof)
}
