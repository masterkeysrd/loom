package loomantigravity

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"sync"

	"time"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/stream"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var _ llm.Provider = (*Provider)(nil)
var _ llm.QuotaProvider = (*Provider)(nil)
var _ llm.TokenCounter = (*Provider)(nil)

// QuotaProvider is an optional interface implemented by providers that support
// querying model quotas and rate-limit replenishment windows.
// Type alias to [llm.QuotaProvider].
type QuotaProvider = llm.QuotaProvider

const (
	// DefaultProfileCacheTTL is the duration dynamic models are cached before re-fetching.
	DefaultProfileCacheTTL = 1 * time.Hour

	// DefaultQuotaCacheTTL is the duration model quotas are cached before re-fetching.
	// Kept short (30s) because quotas fluctuate rapidly with model usage.
	DefaultQuotaCacheTTL = 30 * time.Second
)

// Config configures an Antigravity Provider instance.
type Config struct {
	TokenProvider   TokenProvider
	BaseURL         string
	ProjectID       string
	HTTPClient      *http.Client
	UserAgent       string
	ProfileCacheTTL time.Duration

	// QuotaCacheTTL controls how long model quotas are cached.
	// Defaults to DefaultQuotaCacheTTL (30 seconds).
	// Set to a negative duration (e.g. -1) to disable caching and always query live.
	QuotaCacheTTL time.Duration

	// RawProfiles, if true, serves raw backend model endpoints and aliases without
	// compacting effort variants into canonical model families.
	// When false (default), ListProfiles() returns a clean, compacted catalog of models
	// with reasoning effort configured dynamically at request time via WithThinkingEffort().
	RawProfiles bool

	// Aliases maps custom model names to backend model IDs (e.g. "fast" -> "gemini-3.8-flash-low").
	Aliases map[string]string

	// Resolver overrides the default heuristic model resolver.
	Resolver ModelResolver
}

// Provider represents the Antigravity (Cloud Code) LLM provider for Loom.
type Provider struct {
	client      *Client
	overrides   sync.Map
	cachedMu    sync.RWMutex
	lastFetched time.Time
	cacheTTL    time.Duration
	cached      map[string]llm.ModelProfile
	rawProfiles bool

	quotaTTL         time.Duration
	cachedQuotasMu   sync.RWMutex
	lastQuotaFetched time.Time
	cachedQuotas     map[string]QuotaInfo
}

// NewDefaultProvider creates an Antigravity provider with default configuration.
// It prioritizes environment variables (ANTIGRAVITY_TOKEN, GOOGLE_ACCESS_TOKEN)
// and falls back to an OAuth2TokenProvider using the cached token file.
func NewDefaultProvider(ctx context.Context) (*Provider, error) {
	var tp TokenProvider = NewEnvTokenProvider()
	if _, err := tp.Token(ctx); err != nil {
		// Fallback to OAuth2 provider with default token cache file
		oauthTP, err := NewOAuth2TokenProvider(OAuth2Config{})
		if err != nil {
			return nil, fmt.Errorf("antigravity: create default oauth2 provider: %w", err)
		}
		tp = oauthTP
	}

	return NewProvider(&Config{
		TokenProvider: tp,
	})
}

// NewProvider creates a Provider with custom configuration.
func NewProvider(cfg *Config) (*Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("antigravity: config cannot be nil")
	}
	if cfg.TokenProvider == nil {
		return nil, fmt.Errorf("antigravity: TokenProvider is required")
	}

	var opts []ClientOption
	if cfg.BaseURL != "" {
		opts = append(opts, WithBaseURL(cfg.BaseURL))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, WithHTTPClient(cfg.HTTPClient))
	} else {
		opts = append(opts, WithHTTPClient(&http.Client{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		}))
	}
	if cfg.ProjectID != "" {
		opts = append(opts, WithProjectID(cfg.ProjectID))
	}
	if cfg.UserAgent != "" {
		opts = append(opts, WithUserAgent(cfg.UserAgent))
	}
	if len(cfg.Aliases) > 0 {
		opts = append(opts, WithAliases(cfg.Aliases))
	}
	if cfg.Resolver != nil {
		opts = append(opts, WithResolver(cfg.Resolver))
	}

	cacheTTL := cfg.ProfileCacheTTL
	if cacheTTL <= 0 {
		cacheTTL = DefaultProfileCacheTTL
	}

	quotaTTL := cfg.QuotaCacheTTL
	if quotaTTL == 0 {
		quotaTTL = DefaultQuotaCacheTTL
	}

	client := NewClient(cfg.TokenProvider, opts...)
	return &Provider{
		client:       client,
		cacheTTL:     cacheTTL,
		cached:       make(map[string]llm.ModelProfile),
		rawProfiles:  cfg.RawProfiles,
		quotaTTL:     quotaTTL,
		cachedQuotas: make(map[string]QuotaInfo),
	}, nil
}

// SetRawProfiles configures whether ListProfiles returns raw endpoints or compacted profiles.
func (p *Provider) SetRawProfiles(raw bool) {
	p.rawProfiles = raw
}

// RawProfiles returns whether raw backend models are exposed.
func (p *Provider) RawProfiles() bool {
	return p.rawProfiles
}

// Name returns "antigravity", uniquely identifying this provider in an [llm.Registry].
func (p *Provider) Name() string {
	return "antigravity"
}

// Client returns the underlying Client instance.
func (p *Provider) Client() *Client {
	return p.client
}

// Stream converts request into the Cloud Code wire format, initiates the streaming
// call, and returns an iterator yielding [message.AssistantChunk] values.
func (p *Provider) Stream(ctx context.Context, request *llm.Request) (llm.StreamResponse, error) {
	wireReq, err := toGenerateContentRequest(request)
	if err != nil {
		return nil, fmt.Errorf("antigravity: build request: %w", err)
	}

	if sw, ok := stream.WriterFromContext(ctx); ok {
		_ = sw.Write(ctx, stream.Event{
			Name: "on_llm_request",
			Data: map[string]any{
				"provider": "antigravity",
				"payload": map[string]any{
					"model":   request.Model,
					"request": wireReq,
				},
			},
		})
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("antigravity.api.type", "stream_generate_content"),
		attribute.String("antigravity.model", request.Model),
	)

	return p.client.StreamGenerateContent(ctx, request.Model, wireReq)
}

// SyncProfiles queries the upstream /v1internal:fetchAvailableModels endpoint
// and updates the cached model catalog immediately.
func (p *Provider) SyncProfiles(ctx context.Context) error {
	models, err := p.client.FetchAvailableModels(ctx)
	if err != nil {
		return err
	}

	p.cachedMu.Lock()
	defer p.cachedMu.Unlock()

	for _, m := range models {
		p.cached[m.ID] = toModelProfile(m)
	}
	p.lastFetched = time.Now()
	return nil
}

func (p *Provider) ensureProfiles() {
	p.cachedMu.RLock()
	expired := time.Since(p.lastFetched) > p.cacheTTL
	p.cachedMu.RUnlock()

	if !expired {
		return
	}

	p.cachedMu.Lock()
	defer p.cachedMu.Unlock()

	// Double check lock
	if time.Since(p.lastFetched) <= p.cacheTTL {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	models, err := p.client.FetchAvailableModels(ctx)
	if err != nil {
		// On error (e.g. offline, expired auth), keep static profiles and continue
		return
	}

	for _, m := range models {
		p.cached[m.ID] = toModelProfile(m)
	}
	p.lastFetched = time.Now()
}

func toModelProfile(m AvailableModel) llm.ModelProfile {
	family := "gemini"
	if strings.Contains(m.ID, "claude") {
		family = "claude"
	} else if strings.Contains(m.ID, "gpt") {
		family = "gpt"
	}

	inputs := []llm.Modality{llm.ModalityText}
	if m.SupportsImages {
		inputs = append(inputs, llm.ModalityImage)
	}
	if m.SupportsVideo {
		inputs = append(inputs, llm.ModalityVideo)
	}
	if m.SupportedMimeTypes != nil && m.SupportedMimeTypes["application/pdf"] {
		inputs = append(inputs, llm.ModalityPDF)
	}
	hasAudio := false
	for mime := range m.SupportedMimeTypes {
		if strings.HasPrefix(mime, "audio/") {
			hasAudio = true
			break
		}
	}
	if hasAudio {
		inputs = append(inputs, llm.ModalityAudio)
	}

	return llm.ModelProfile{
		ID:     m.ID,
		Name:   m.DisplayName,
		Family: family,
		Capabilities: llm.Capabilities{
			Attachment:  m.SupportsImages || m.SupportsVideo || len(m.SupportedMimeTypes) > 0,
			Reasoning:   m.SupportsThinking,
			ToolCall:    true,
			Temperature: true,
		},
		Limits: llm.ProfileLimits{
			Context: m.MaxTokens,
			Output:  m.MaxOutputTokens,
		},
		Modalities: llm.Modalities{
			Inputs:  inputs,
			Outputs: []llm.Modality{llm.ModalityText},
		},
	}
}

// ListProfiles returns all known Antigravity model profiles.
// By default (when RawProfiles is false), it returns clean, compacted profiles
// for each canonical model family with reasoning effort options surfaced.
// When RawProfiles is true, it returns the raw uncompacted endpoints and variants.
func (p *Provider) ListProfiles() []llm.ModelProfile {
	p.ensureProfiles()

	merged := make(map[string]llm.ModelProfile, len(staticProfiles)+len(p.cached))
	maps.Copy(merged, staticProfiles)

	p.cachedMu.RLock()
	for k, v := range p.cached {
		merged[k] = v
	}
	p.cachedMu.RUnlock()

	p.overrides.Range(func(k, v any) bool {
		merged[k.(string)] = v.(llm.ModelProfile)
		return true
	})

	if !p.rawProfiles {
		merged = compactModelProfiles(merged)
	}

	result := make([]llm.ModelProfile, 0, len(merged))
	for _, m := range merged {
		result = append(result, m)
	}
	return result
}

// GetProfile returns the profile for the given model ID, checking overrides,
// cached dynamic models, and canonical aliases before falling back to the static catalog.
func (p *Provider) GetProfile(id string) (llm.ModelProfile, bool) {
	if v, ok := p.overrides.Load(id); ok {
		return v.(llm.ModelProfile), true
	}

	p.ensureProfiles()

	p.cachedMu.RLock()
	prof, ok := p.cached[id]
	p.cachedMu.RUnlock()
	if ok {
		return prof, true
	}

	if m, ok := staticProfiles[id]; ok {
		return m, true
	}

	// Try base model lookup if id was an effort variant (e.g. "gemini-3.8-flash-high" -> "gemini-3.8-flash")
	base, _ := parseEffortSuffix(id)
	if base != id {
		p.cachedMu.RLock()
		prof, ok = p.cached[base]
		p.cachedMu.RUnlock()
		if ok {
			prof.ID = id
			return prof, true
		}
	}

	// Try tiered model lookup if id was canonical (e.g. "gemini-3.8-flash" -> "gemini-3.8-flash-tiered")
	p.cachedMu.RLock()
	prof, ok = p.cached[id+"-tiered"]
	p.cachedMu.RUnlock()
	if ok {
		prof.ID = id
		return prof, true
	}

	return llm.ModelProfile{}, false
}

func compactModelProfiles(raw map[string]llm.ModelProfile) map[string]llm.ModelProfile {
	families := make(map[string]llm.ModelProfile)
	familyEfforts := make(map[string]map[string]bool)

	for id, prof := range raw {
		base, effort := parseEffortSuffix(id)
		isTiered := strings.HasSuffix(id, "-tiered")
		if isTiered {
			base = strings.TrimSuffix(id, "-tiered")
		}

		if isTiered || effort != "" {
			existing, ok := families[base]
			if !ok {
				existing = prof
				existing.ID = base
				existing.Name = cleanDisplayName(prof.Name, id, base)
				existing.Capabilities.Reasoning = true
				families[base] = existing
			}
			if familyEfforts[base] == nil {
				familyEfforts[base] = make(map[string]bool)
			}
			if isTiered {
				familyEfforts[base]["low"] = true
				familyEfforts[base]["medium"] = true
				familyEfforts[base]["high"] = true
			} else if effort != "" {
				familyEfforts[base][strings.ToLower(effort)] = true
			}
		} else {
			families[id] = prof
		}
	}

	standardOrder := []string{"low", "medium", "high"}
	for base, effMap := range familyEfforts {
		prof := families[base]
		var values []string
		for _, e := range standardOrder {
			if effMap[e] {
				values = append(values, e)
			}
		}
		if len(values) > 0 {
			prof.Capabilities.ReasoningOptions = []llm.ReasoningOption{
				{Type: "effort", Values: values},
			}
		}
		families[base] = prof
	}

	return families
}

// SearchProfiles returns profiles whose ID or Name contains query (case-insensitive).
func (p *Provider) SearchProfiles(query string) []llm.ModelProfile {
	return llm.SearchProfiles(p.ListProfiles(), query)
}

// OverrideProfile stores a custom profile for id, shadowing any static entry.
func (p *Provider) OverrideProfile(id string, profile llm.ModelProfile) {
	p.overrides.Store(id, profile)
}

// ListQuotas returns the current quota information for all models exposed by the backend.
// If QuotaCacheTTL is positive, results are cached up to that duration.
// If QuotaCacheTTL is negative (e.g. -1), it always queries the backend directly.
func (p *Provider) ListQuotas(ctx context.Context) (map[string]QuotaInfo, error) {
	p.cachedQuotasMu.RLock()
	isFresh := p.quotaTTL > 0 && time.Since(p.lastQuotaFetched) <= p.quotaTTL && len(p.cachedQuotas) > 0
	if isFresh {
		res := make(map[string]QuotaInfo, len(p.cachedQuotas))
		maps.Copy(res, p.cachedQuotas)
		p.cachedQuotasMu.RUnlock()
		return res, nil
	}
	p.cachedQuotasMu.RUnlock()

	p.cachedQuotasMu.Lock()
	defer p.cachedQuotasMu.Unlock()

	// Double check under write lock
	if p.quotaTTL > 0 && time.Since(p.lastQuotaFetched) <= p.quotaTTL && len(p.cachedQuotas) > 0 {
		res := make(map[string]QuotaInfo, len(p.cachedQuotas))
		maps.Copy(res, p.cachedQuotas)
		return res, nil
	}

	quotas, err := p.client.FetchQuotas(ctx)
	if err != nil {
		// If fetch fails but we have cached quotas, fallback to cached
		if len(p.cachedQuotas) > 0 {
			res := make(map[string]QuotaInfo, len(p.cachedQuotas))
			maps.Copy(res, p.cachedQuotas)
			return res, nil
		}
		return nil, err
	}

	p.cachedQuotas = quotas
	p.lastQuotaFetched = time.Now()

	res := make(map[string]QuotaInfo, len(quotas))
	maps.Copy(res, quotas)
	return res, nil
}

// GetQuota returns the quota for a specific model name or alias.
// If the model name is a virtual alias (such as "gemini-3.8-flash" or "gemini-3.8-flash-high"),
// it resolves the alias to its underlying backend endpoint (e.g. "gemini-3.8-flash-tiered")
// and returns that endpoint's quota.
func (p *Provider) GetQuota(ctx context.Context, model string) (QuotaInfo, bool, error) {
	quotas, err := p.ListQuotas(ctx)
	if err != nil {
		return QuotaInfo{}, false, err
	}

	// 1. Direct match
	if q, ok := quotas[model]; ok {
		return q, true, nil
	}

	// 2. Resolve target model via client's heuristic / alias resolver
	target := p.client.ResolveTargetModel(model)
	if target != model {
		if q, ok := quotas[target]; ok {
			return q, true, nil
		}
	}

	// 3. Fallbacks for base name and tiered suffixes
	base, _ := parseEffortSuffix(model)
	if q, ok := quotas[base]; ok {
		return q, true, nil
	}
	if q, ok := quotas[model+"-tiered"]; ok {
		return q, true, nil
	}
	if q, ok := quotas[base+"-tiered"]; ok {
		return q, true, nil
	}

	return QuotaInfo{}, false, nil
}

// CountTokens estimates or computes the token count for the given message list.
// It queries Cloud Code's internal /v1internal:countTokens endpoint. If the server
// request cannot be completed (e.g. offline/network failure or server error),
// it gracefully falls back to llm.ApproximateTokenCounter.
func (p *Provider) CountTokens(ctx context.Context, messages message.MessageList) (int, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}

	req, err := ToCountTokensRequest(messages)
	if err != nil {
		return llm.ApproximateTokenCounter{}.CountTokens(ctx, messages)
	}

	resp, err := p.client.CountTokens(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return llm.ApproximateTokenCounter{}.CountTokens(ctx, messages)
	}

	return resp.TotalTokens, nil
}

