package loomantigravity

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"

	"time"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/providers/antigravity/sdk"
	"github.com/masterkeysrd/loom/stream"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/oauth2"
)

var _ llm.Provider = (*Provider)(nil)
var _ profile.Provider = (*Provider)(nil)
var _ llm.ModelLister = (*Provider)(nil)
var _ llm.QuotaProvider = (*Provider)(nil)
var _ llm.TokenCounter = (*Provider)(nil)
var _ llm.Extension = Session{}

// SessionExtensionID is the unique identifier for the Session extension.
const SessionExtensionID = "antigravity.session"

// Session configures sticky session affinity and prompt correlation for Cloud Code.
type Session struct {
	// ID sets request.session_id so sequential turns route to the same serving
	// replica and hit its in-memory implicit KV prefix cache.
	ID string

	// UserPromptID sets user_prompt_id on the outer RequestEnvelope.
	UserPromptID string
}

// ExtensionID returns SessionExtensionID.
func (Session) ExtensionID() string { return SessionExtensionID }

func extractSession(req *llm.Request) (Session, bool) {
	if req == nil || req.Extensions == nil {
		return Session{}, false
	}
	ext, ok := req.Extensions[SessionExtensionID]
	if !ok || ext == nil {
		return Session{}, false
	}
	switch v := ext.(type) {
	case Session:
		return v, true
	case *Session:
		if v == nil {
			return Session{}, false
		}
		return *v, true
	default:
		return Session{}, false
	}
}

const (
	// DefaultBaseURL is the production Google Cloud Code / Antigravity API base URL.
	DefaultBaseURL = sdk.DefaultBaseURL

	// DailyBaseURL is the daily staging / canary environment URL.
	DailyBaseURL = sdk.DailyBaseURL

	// DefaultClientVersion is the emulated Antigravity CLI version reported in DefaultUserAgent.
	DefaultClientVersion = sdk.DefaultClientVersion

	// DefaultUserAgent emulates a modern versioned Antigravity CLI client identifier.
	DefaultUserAgent = sdk.DefaultUserAgent

	// DefaultProfileCacheTTL is the duration dynamic models are cached before re-fetching.
	DefaultProfileCacheTTL = 1 * time.Hour

	// DefaultQuotaCacheTTL is the duration model quotas are cached before re-fetching.
	// Kept short (30s) because quotas fluctuate rapidly with model usage.
	DefaultQuotaCacheTTL = 30 * time.Second
)

// Config configures an Antigravity Provider instance.
type Config struct {
	TokenSource oauth2.TokenSource
	BaseURL     string

	// ProjectID sets an explicit Google Cloud user billing project sent via the
	// X-Goog-User-Project HTTP header. Leave empty for standard Antigravity accounts,
	// where CompanionProject is auto-discovered via /v1internal:loadCodeAssist.
	ProjectID string

	// CompanionProject overrides the Cloud AI Companion project sent in the JSON
	// request body ("project" field). When empty, the client automatically discovers
	// it once per token via POST /v1internal:loadCodeAssist without setting
	// X-Goog-User-Project.
	CompanionProject string

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
	client      *sdk.Client
	tokenSource oauth2.TokenSource
	aliases     map[string]string
	resolver    ModelResolver

	availableInitMu sync.Mutex
	availableMu     sync.RWMutex
	availableModels map[string]bool
	availableToken  string

	overrides   sync.Map
	cachedMu    sync.RWMutex
	lastFetched time.Time
	cacheTTL    time.Duration
	cached      map[string]profile.Profile
	rawProfiles bool

	quotaTTL         time.Duration
	cachedQuotasMu   sync.RWMutex
	lastQuotaFetched time.Time
	cachedQuotas     QuotaSnapshot
}

// NewProvider creates a Provider with custom configuration.
func NewProvider(cfg *Config) (*Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("antigravity: config cannot be nil")
	}
	if cfg.TokenSource == nil {
		return nil, fmt.Errorf("antigravity: TokenSource is required")
	}

	var opts []sdk.ClientOption
	if cfg.BaseURL != "" {
		opts = append(opts, sdk.WithBaseURL(cfg.BaseURL))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, sdk.WithHTTPClient(cfg.HTTPClient))
	} else {
		opts = append(opts, sdk.WithHTTPClient(&http.Client{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		}))
	}
	if cfg.ProjectID != "" {
		opts = append(opts, sdk.WithProjectID(cfg.ProjectID))
	}
	if cfg.CompanionProject != "" {
		opts = append(opts, sdk.WithCompanionProject(cfg.CompanionProject))
	}
	if cfg.UserAgent != "" {
		opts = append(opts, sdk.WithUserAgent(cfg.UserAgent))
	}

	cacheTTL := cfg.ProfileCacheTTL
	if cacheTTL <= 0 {
		cacheTTL = DefaultProfileCacheTTL
	}

	quotaTTL := cfg.QuotaCacheTTL
	if quotaTTL == 0 {
		quotaTTL = DefaultQuotaCacheTTL
	}

	var aliases map[string]string
	if len(cfg.Aliases) > 0 {
		aliases = make(map[string]string, len(cfg.Aliases))
		maps.Copy(aliases, cfg.Aliases)
	}

	client := sdk.NewClient(cfg.TokenSource, opts...)
	return &Provider{
		client:          client,
		tokenSource:     cfg.TokenSource,
		aliases:         aliases,
		resolver:        cfg.Resolver,
		availableModels: make(map[string]bool),
		cacheTTL:        cacheTTL,
		cached:          make(map[string]profile.Profile),
		rawProfiles:     cfg.RawProfiles,
		quotaTTL:        quotaTTL,
	}, nil
}

func (p *Provider) currentAccessToken() string {
	if p.tokenSource == nil {
		return ""
	}
	tok, err := p.tokenSource.Token()
	if err != nil {
		return ""
	}
	return tok.AccessToken
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

// Client returns the underlying SDK client instance.
func (p *Provider) Client() *sdk.Client {
	return p.client
}

// Stream converts request into the Cloud Code wire format, resolves aliases and
// target endpoints, initiates the streaming call, and yields [message.AssistantChunk] values.
func (p *Provider) Stream(ctx context.Context, request *llm.Request) (llm.StreamResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("antigravity: request cannot be nil")
	}

	if err := p.validateRequestProfile(request); err != nil {
		return nil, err
	}

	wireReq, err := toGenerateContentRequest(request)
	if err != nil {
		return nil, fmt.Errorf("antigravity: build request: %w", err)
	}

	p.ensureAvailableModels(ctx)
	targetModel, resolvedReq := p.resolveModel(request.Model, wireReq)

	var userPromptID string
	if sess, ok := extractSession(request); ok {
		userPromptID = sess.UserPromptID
	}

	if sw, ok := stream.WriterFromContext(ctx); ok {
		payload := map[string]any{
			"model":   targetModel,
			"request": resolvedReq,
		}
		if userPromptID != "" {
			payload["user_prompt_id"] = userPromptID
		}
		_ = sw.Write(ctx, stream.Event{
			Name: "on_llm_request",
			Data: map[string]any{
				"provider": "antigravity",
				"payload":  payload,
			},
		})
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("antigravity.api.type", "stream_generate_content"),
		attribute.String("antigravity.model", targetModel),
	)

	var streamOpts []sdk.StreamOption
	if userPromptID != "" {
		streamOpts = append(streamOpts, sdk.WithUserPromptID(userPromptID))
	}
	rawStream, err := p.client.StreamGenerateContent(ctx, targetModel, resolvedReq, streamOpts...)
	if err != nil {
		return nil, err
	}

	return func(yield func(message.AssistantChunk, error) bool) {
		for env, err := range rawStream {
			if err != nil {
				if !yield(message.AssistantChunk{}, err) {
					return
				}
				continue
			}
			result := env.Result()
			if result == nil {
				continue
			}
			chunk, err := toAssistantChunk(result, targetModel)
			if err != nil {
				if !yield(message.AssistantChunk{}, fmt.Errorf("antigravity: convert chunk: %w", err)) {
					return
				}
				continue
			}
			if !yield(chunk, nil) {
				return
			}
		}
	}, nil
}

// SyncProfiles queries the upstream /v1internal:fetchAvailableModels endpoint
// and updates the cached model catalog immediately.
func (p *Provider) SyncProfiles(ctx context.Context) error {
	rawModels, err := p.client.FetchAvailableModels(ctx)
	if err != nil {
		return err
	}
	p.recordAvailableModels(rawModels, p.currentAccessToken())
	models := expandAvailableModels(rawModels)

	p.cachedMu.Lock()
	defer p.cachedMu.Unlock()

	p.cached = make(map[string]profile.Profile, len(models))
	for _, m := range models {
		p.cached[m.ID] = toModelProfile(m)
	}
	p.lastFetched = time.Now()
	return nil
}

// ListModels queries the upstream /v1internal:fetchAvailableModels endpoint,
// refreshes the in-memory model cache, and returns the resulting model profiles
// (compacted by canonical family unless RawProfiles is true).
func (p *Provider) ListModels(ctx context.Context) ([]profile.Profile, error) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("antigravity.api.type", "fetch_available_models"))

	if err := p.SyncProfiles(ctx); err != nil {
		return nil, fmt.Errorf("antigravity: list models: %w", err)
	}
	return p.collectCachedProfiles(), nil
}

// GetModel refreshes available models from /v1internal:fetchAvailableModels and
// resolves id (including canonical families, effort variants, and overrides).
func (p *Provider) GetModel(ctx context.Context, id string) (profile.Profile, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return profile.Profile{}, errors.New("antigravity: model id is required")
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("antigravity.api.type", "fetch_available_models"),
		attribute.String("antigravity.model", id),
	)

	if err := p.SyncProfiles(ctx); err != nil {
		return profile.Profile{}, fmt.Errorf("antigravity: get model %q: %w", id, err)
	}

	prof, ok := p.lookupCachedProfile(id)
	if !ok {
		return profile.Profile{}, fmt.Errorf("antigravity: model %q not found", id)
	}
	return prof, nil
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

	rawModels, err := p.client.FetchAvailableModels(ctx)
	if err != nil {
		// On error (e.g. offline, expired auth), keep static profiles and continue
		return
	}
	p.recordAvailableModels(rawModels, p.currentAccessToken())

	for _, m := range expandAvailableModels(rawModels) {
		p.cached[m.ID] = toModelProfile(m)
	}
	p.lastFetched = time.Now()
}

func toModelProfile(m sdk.AvailableModel) profile.Profile {
	family := "gemini"
	if strings.Contains(m.ID, "claude") {
		family = "claude"
	} else if strings.Contains(m.ID, "gpt") {
		family = "gpt"
	}

	inputs := []profile.Modality{profile.ModalityText}
	if m.SupportsImages {
		inputs = append(inputs, profile.ModalityImage)
	}
	if m.SupportsVideo {
		inputs = append(inputs, profile.ModalityVideo)
	}
	if m.SupportedMimeTypes != nil && m.SupportedMimeTypes["application/pdf"] {
		inputs = append(inputs, profile.ModalityPDF)
	}
	hasAudio := false
	for mime := range m.SupportedMimeTypes {
		if strings.HasPrefix(mime, "audio/") {
			hasAudio = true
			break
		}
	}
	if hasAudio {
		inputs = append(inputs, profile.ModalityAudio)
	}

	return profile.Profile{
		ID:          m.ID,
		Name:        m.DisplayName,
		Description: m.Description,
		Family:      family,
		Capabilities: profile.Capabilities{
			Completion:  true,
			Attachment:  m.SupportsImages || m.SupportsVideo || len(m.SupportedMimeTypes) > 0,
			Reasoning:   m.SupportsThinking,
			ToolCall:    true,
			Temperature: true,
		},
		Limits: profile.Limits{
			Context: m.MaxTokens,
			Output:  m.MaxOutputTokens,
		},
		Modalities: profile.Modalities{
			Inputs:  inputs,
			Outputs: []profile.Modality{profile.ModalityText},
		},
	}
}

func (p *Provider) mergedRawProfiles() map[string]profile.Profile {
	p.cachedMu.RLock()
	merged := make(map[string]profile.Profile, len(staticProfiles)+len(p.cached))
	maps.Copy(merged, staticProfiles)
	for id, live := range p.cached {
		if base, ok := merged[id]; ok {
			merged[id] = profile.Merge(base, live)
		} else {
			merged[id] = live
		}
	}
	p.cachedMu.RUnlock()

	p.overrides.Range(func(k, v any) bool {
		merged[k.(string)] = v.(profile.Profile)
		return true
	})
	return merged
}

func (p *Provider) collectCachedProfiles() []profile.Profile {
	merged := p.mergedRawProfiles()
	if !p.rawProfiles {
		merged = compactModelProfiles(merged)
	}

	result := make([]profile.Profile, 0, len(merged))
	for _, m := range merged {
		result = append(result, m)
	}
	slices.SortFunc(result, func(a, b profile.Profile) int {
		return cmp.Compare(a.ID, b.ID)
	})
	return result
}

// ListProfiles returns all known Antigravity model profiles.
// By default (when RawProfiles is false), it returns clean, compacted profiles
// for each canonical model family with reasoning effort options surfaced.
// When RawProfiles is true, it returns the raw uncompacted endpoints and variants.
func (p *Provider) ListProfiles() []profile.Profile {
	p.ensureProfiles()
	return p.collectCachedProfiles()
}

func (p *Provider) validateRequestProfile(request *llm.Request) error {
	prof, ok := p.lookupProfileForRequest(request.Model)
	if !ok {
		return nil
	}
	if !prof.Capabilities.Completion && (prof.Capabilities.Embedding || prof.Capabilities.Decision) {
		return profile.NewUnsupportedCapabilityError(p.Name(), request.Model, "completion", "model profile does not support chat completion")
	}
	if err := prof.ValidateMessagesModalities(p.Name(), request.Model, request.Messages); err != nil {
		return err
	}
	if err := prof.ValidateToolCallCapability(p.Name(), request.Model, len(request.Tools) > 0); err != nil {
		return err
	}
	return nil
}

func (p *Provider) lookupProfileForRequest(model string) (profile.Profile, bool) {
	if prof, ok := p.lookupCachedProfile(model); ok {
		return prof, true
	}
	if p.aliases != nil {
		if target, ok := p.aliases[model]; ok && target != model {
			return p.lookupCachedProfile(target)
		}
	}
	return profile.Profile{}, false
}

// GetProfile returns the profile for the given model ID, checking overrides,
// compacted canonical families (when RawProfiles is false), cached dynamic models,
// and canonical/effort aliases before falling back to the static catalog.
func (p *Provider) GetProfile(id string) (profile.Profile, bool) {
	if v, ok := p.overrides.Load(id); ok {
		return v.(profile.Profile), true
	}

	p.ensureProfiles()
	return p.lookupCachedProfile(id)
}

func (p *Provider) lookupCachedProfile(id string) (profile.Profile, bool) {
	if v, ok := p.overrides.Load(id); ok {
		return v.(profile.Profile), true
	}

	merged := p.mergedRawProfiles()

	if !p.rawProfiles {
		compacted := compactModelProfiles(merged)
		if prof, ok := compacted[id]; ok {
			return prof, true
		}
		if prof, ok := merged[id]; ok {
			return prof, true
		}
		base, _ := parseEffortSuffix(id)
		if base != id {
			if prof, ok := compacted[base]; ok {
				prof.ID = id
				return prof, true
			}
		}
		if before, ok := strings.CutSuffix(id, "-tiered"); ok {
			if prof, ok := compacted[before]; ok {
				prof.ID = id
				return prof, true
			}
		}
		return profile.Profile{}, false
	}

	if prof, ok := merged[id]; ok {
		return prof, true
	}

	base, _ := parseEffortSuffix(id)
	if base != id {
		if prof, ok := merged[base]; ok {
			prof.ID = id
			return prof, true
		}
		if prof, ok := merged[base+"-tiered"]; ok {
			prof.ID = id
			return prof, true
		}
	}

	if prof, ok := merged[id+"-tiered"]; ok {
		prof.ID = id
		return prof, true
	}

	compacted := compactModelProfiles(merged)
	if prof, ok := compacted[id]; ok {
		return prof, true
	}

	return profile.Profile{}, false
}

func compactModelProfiles(raw map[string]profile.Profile) map[string]profile.Profile {
	families := make(map[string]profile.Profile)
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
			} else {
				existing.Capabilities.Reasoning = true
				existing.Capabilities.Attachment = existing.Capabilities.Attachment || prof.Capabilities.Attachment
				if prof.Limits.Context > existing.Limits.Context {
					existing.Limits.Context = prof.Limits.Context
				}
				if prof.Limits.Output > existing.Limits.Output {
					existing.Limits.Output = prof.Limits.Output
				}
				if existing.Description == "" && prof.Description != "" {
					existing.Description = prof.Description
				}
				if len(prof.Modalities.Inputs) > len(existing.Modalities.Inputs) {
					existing.Modalities.Inputs = prof.Modalities.Inputs
				}
			}
			families[base] = existing
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
			if existing, ok := families[id]; ok {
				prof.ID = id
				prof.Name = cleanDisplayName(prof.Name, id, id)
				prof.Capabilities.Reasoning = prof.Capabilities.Reasoning || existing.Capabilities.Reasoning
				prof.Capabilities.Attachment = prof.Capabilities.Attachment || existing.Capabilities.Attachment
				if existing.Limits.Context > prof.Limits.Context {
					prof.Limits.Context = existing.Limits.Context
				}
				if existing.Limits.Output > prof.Limits.Output {
					prof.Limits.Output = existing.Limits.Output
				}
				if len(existing.Modalities.Inputs) > len(prof.Modalities.Inputs) {
					prof.Modalities.Inputs = existing.Modalities.Inputs
				}
			}
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
			prof.Capabilities.ReasoningOptions = []profile.ReasoningOption{
				{Type: "effort", Values: values},
			}
		}
		families[base] = prof
	}

	return families
}

// SearchProfiles returns profiles whose ID or Name contains query (case-insensitive).
func (p *Provider) SearchProfiles(query string) []profile.Profile {
	return profile.Search(p.ListProfiles(), query)
}

// OverrideProfile stores a custom profile for id, shadowing any static entry.
func (p *Provider) OverrideProfile(id string, prof profile.Profile) {
	p.overrides.Store(id, prof)
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
