package loomantigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/masterkeysrd/loom/auth"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/tool"
	"golang.org/x/oauth2"
)

func TestNewOAuthConfig(t *testing.T) {
	// Defaults
	cfg := NewOAuthConfig(OAuthOptions{})
	if cfg.ClientID != DefaultAntigravityClientID {
		t.Errorf("expected default client ID %q, got %q", DefaultAntigravityClientID, cfg.ClientID)
	}
	if cfg.ClientSecret != DefaultAntigravityClientSecret() {
		t.Errorf("expected default secret, got %q", cfg.ClientSecret)
	}
	expectedRedirect := fmt.Sprintf("http://localhost:%d/callback", DefaultOAuthRedirectPort)
	if cfg.RedirectURL != expectedRedirect {
		t.Errorf("expected default redirect %q, got %q", expectedRedirect, cfg.RedirectURL)
	}
	if len(cfg.Scopes) != 2 {
		t.Errorf("expected 2 default scopes, got %v", cfg.Scopes)
	}

	// Custom options
	customCfg := NewOAuthConfig(OAuthOptions{
		ClientID:     "custom-client",
		ClientSecret: "custom-secret",
		RedirectPort: 8080,
		Scopes:       []string{"scope-a"},
	})
	if customCfg.ClientID != "custom-client" {
		t.Errorf("expected 'custom-client', got %q", customCfg.ClientID)
	}
	if customCfg.ClientSecret != "custom-secret" {
		t.Errorf("expected 'custom-secret', got %q", customCfg.ClientSecret)
	}
	if customCfg.RedirectURL != "http://localhost:8080/callback" {
		t.Errorf("expected 'http://localhost:8080/callback', got %q", customCfg.RedirectURL)
	}
	if len(customCfg.Scopes) != 1 || customCfg.Scopes[0] != "scope-a" {
		t.Errorf("expected custom scopes, got %v", customCfg.Scopes)
	}

	// Environment variable overrides
	t.Setenv("ANTIGRAVITY_CLIENT_ID", "env-client-id")
	t.Setenv("ANTIGRAVITY_CLIENT_SECRET", "env-client-secret")
	envCfg := NewOAuthConfig(OAuthOptions{})
	if envCfg.ClientID != "env-client-id" {
		t.Errorf("expected env client ID, got %q", envCfg.ClientID)
	}
	if envCfg.ClientSecret != "env-client-secret" {
		t.Errorf("expected env client secret, got %q", envCfg.ClientSecret)
	}
}

func TestNewProvider_Validation(t *testing.T) {
	if _, err := NewProvider(nil); err == nil {
		t.Fatal("expected error for nil config")
	}
	if _, err := NewProvider(&Config{}); err == nil {
		t.Fatal("expected error for missing TokenSource")
	}
}

func TestProvider_Stream(t *testing.T) {
	// 1. Setup mock SSE HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1internal:streamGenerateContent" {
			http.NotFound(w, r)
			return
		}
		// Verify headers
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-bearer-token" {
			t.Errorf("expected Authorization header 'Bearer test-bearer-token', got %q", auth)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", ct)
		}
		if accept := r.Header.Get("Accept"); accept != "text/event-stream" {
			t.Errorf("expected Accept text/event-stream, got %q", accept)
		}

		// Verify body
		var env RequestEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			t.Errorf("failed to decode request envelope: %v", err)
		}
		if env.Model != "gemini-2.5-pro" {
			t.Errorf("expected model 'gemini-2.5-pro', got %q", env.Model)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}

		// Send chunk 1: text part
		chunk1 := `{"response":{"candidates":[{"content":{"parts":[{"text":"Hello, "}]}}]}}`
		fmt.Fprintf(w, "data: %s\n\n", chunk1)
		flusher.Flush()

		// Send chunk 2: thinking block
		chunk2 := `{"response":{"candidates":[{"content":{"parts":[{"text":"Thinking hard...","thought":true}]}}]}}`
		fmt.Fprintf(w, "data: %s\n\n", chunk2)
		flusher.Flush()

		// Send chunk 3: tool call
		chunk3 := `{"response":{"candidates":[{"content":{"parts":[{"functionCall":{"id":"call_123","name":"get_weather","args":{"city":"SF"}}}]}}]}}`
		fmt.Fprintf(w, "data: %s\n\n", chunk3)
		flusher.Flush()

		// Send chunk 4: finish reason + usage metrics
		chunk4 := `{"response":{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"totalTokenCount":30,"cachedContentTokenCount":5,"thoughtsTokenCount":8}}}`
		fmt.Fprintf(w, "data: %s\n\n", chunk4)
		flusher.Flush()
	}))
	defer server.Close()

	// 2. Initialize Provider
	p, err := NewProvider(&Config{
		TokenSource: auth.StaticTokenSource("test-bearer-token"),
		BaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	req := &llm.Request{
		Model: "gemini-2.5-pro",
		Messages: []message.Message{
			message.NewUserText("Hi there"),
		},
	}
	stream, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}

	var chunks []message.AssistantChunk
	for chunk, err := range stream {
		if err != nil {
			t.Fatalf("stream iteration error: %v", err)
		}
		chunks = append(chunks, chunk)
	}

	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks, got %d", len(chunks))
	}

	// Verify Chunk 1: Text
	if len(chunks[0].Content) != 1 {
		t.Fatalf("expected 1 block in chunk 0, got %d", len(chunks[0].Content))
	}
	tb, ok := chunks[0].Content[0].(*message.TextBlock)
	if !ok || tb.Text != "Hello, " {
		t.Errorf("chunk 0: expected TextBlock 'Hello, ', got %#v", chunks[0].Content[0])
	}

	// Verify Chunk 2: Thinking
	th, ok := chunks[1].Content[0].(*message.ThinkingBlock)
	if !ok || th.Thinking != "Thinking hard..." {
		t.Errorf("chunk 1: expected ThinkingBlock 'Thinking hard...', got %#v", chunks[1].Content[0])
	}

	// Verify Chunk 3: Tool Call
	tc, ok := chunks[2].Content[0].(*message.ToolCall)
	if !ok || tc.Name != "get_weather" || tc.ID != "call_123" {
		t.Errorf("chunk 2: expected ToolCall 'get_weather', got %#v", chunks[2].Content[0])
	}

	// Verify Chunk 4: Done and Token Metrics
	if !chunks[3].Done || chunks[3].DoneReason != "STOP" {
		t.Errorf("chunk 3: expected Done=true, DoneReason='STOP', got Done=%v, Reason=%q", chunks[3].Done, chunks[3].DoneReason)
	}
	if chunks[3].Metrics == nil || chunks[3].Metrics.TotalTokens != 30 {
		t.Errorf("chunk 3: expected TotalTokens=30, got %#v", chunks[3].Metrics)
	}
	if chunks[3].Metrics.Tokens.Input != 10 || chunks[3].Metrics.Tokens.Output != 20 {
		t.Errorf("chunk 3: expected Input=10, Output=20, got %#v", chunks[3].Metrics.Tokens)
	}
	if chunks[3].Metrics.Tokens.CacheRead != 5 || chunks[3].Metrics.Tokens.Reasoning != 8 {
		t.Errorf("chunk 3: expected CacheRead=5, Reasoning=8, got %#v", chunks[3].Metrics.Tokens)
	}
}

func newMockModelsServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1internal:fetchAvailableModels" {
			http.NotFound(w, r)
			return
		}
		resp := map[string]any{
			"models": map[string]any{
				"gemini-2.5-pro": map[string]any{
					"displayName":      "Gemini 2.5 Pro",
					"maxTokens":        1048576,
					"maxOutputTokens":  65536,
					"supportsThinking": true,
					"supportsImages":   true,
					"quotaInfo": map[string]any{
						"remainingFraction": 1.0,
						"resetTime":         "2026-09-10T03:00:00Z",
					},
				},
				"gemini-3.8-flash-tiered": map[string]any{
					"displayName":      "Gemini 3.8 Flash",
					"maxTokens":        1048576,
					"maxOutputTokens":  65536,
					"supportsThinking": true,
					"supportsImages":   true,
					"quotaInfo": map[string]any{
						"remainingFraction": 0.804,
						"resetTime":         "2026-09-10T02:00:00Z",
					},
				},
				"gemini-3.6-flash-low": map[string]any{
					"displayName":      "Gemini 3.6 Flash (Low)",
					"maxTokens":        1048576,
					"maxOutputTokens":  65536,
					"supportsThinking": true,
				},
				"claude-sonnet-4-6": map[string]any{
					"displayName":      "Claude Sonnet 4.6",
					"maxTokens":        250000,
					"maxOutputTokens":  64000,
					"supportsThinking": true,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestProfiles(t *testing.T) {
	server := newMockModelsServer(t)
	defer server.Close()

	p, err := NewProvider(&Config{
		TokenSource: auth.StaticTokenSource("dummy"),
		BaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	profiles := p.ListProfiles()
	if len(profiles) == 0 {
		t.Fatal("expected non-empty profiles list")
	}

	prof, ok := p.GetProfile("gemini-2.5-pro")
	if !ok {
		t.Fatal("expected to find gemini-2.5-pro")
	}
	if !prof.Capabilities.Reasoning {
		t.Errorf("expected gemini-2.5-pro to have Reasoning=true")
	}

	// Test Search
	searchRes := p.SearchProfiles("flash")
	if len(searchRes) == 0 {
		t.Fatal("expected search results for 'flash'")
	}

	// Test Override
	custom := prof
	custom.Name = "Overridden Profile"
	p.OverrideProfile("gemini-2.5-pro", custom)

	updated, _ := p.GetProfile("gemini-2.5-pro")
	if updated.Name != "Overridden Profile" {
		t.Errorf("expected overridden name, got %q", updated.Name)
	}
}

func TestDynamicProfilesCache(t *testing.T) {
	fetchCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1internal:fetchAvailableModels" {
			http.NotFound(w, r)
			return
		}
		fetchCount++
		resp := map[string]any{
			"models": map[string]any{
				"gemini-test-dynamic": map[string]any{
					"displayName":     "Gemini Dynamic Test Model",
					"maxTokens":       2000000,
					"maxOutputTokens": 65536,
				},
				"chat_20706": map[string]any{
					"displayName": "Internal Skip Model",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p, err := NewProvider(&Config{
		TokenSource: auth.StaticTokenSource("dummy-token"),
		BaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	// First ListProfiles call triggers dynamic fetch
	profs := p.ListProfiles()
	if fetchCount != 1 {
		t.Fatalf("expected 1 fetch, got %d", fetchCount)
	}

	found := false
	for _, prof := range profs {
		if prof.ID == "gemini-test-dynamic" {
			found = true
			if prof.Name != "Gemini Dynamic Test Model" {
				t.Errorf("expected displayName 'Gemini Dynamic Test Model', got %q", prof.Name)
			}
			if prof.Limits.Context != 2000000 {
				t.Errorf("expected context limit 2000000, got %d", prof.Limits.Context)
			}
		}
		if prof.ID == "chat_20706" {
			t.Errorf("expected chat_20706 to be filtered out")
		}
	}
	if !found {
		t.Errorf("expected to find gemini-test-dynamic in profiles")
	}

	// Second call should use cache and not hit the server again
	_ = p.ListProfiles()
	if fetchCount != 1 {
		t.Fatalf("expected fetchCount to stay 1 due to cache, got %d", fetchCount)
	}

	// GetProfile should also find the dynamic model
	dynProf, ok := p.GetProfile("gemini-test-dynamic")
	if !ok {
		t.Fatal("expected GetProfile to find gemini-test-dynamic")
	}
	if dynProf.Limits.Output != 65536 {
		t.Errorf("expected output limit 65536, got %d", dynProf.Limits.Output)
	}
}

func TestRawVsCompactProfiles(t *testing.T) {
	server := newMockModelsServer(t)
	defer server.Close()

	// Compact view (default)
	pCompact, err := NewProvider(&Config{
		TokenSource: auth.StaticTokenSource("dummy"),
		BaseURL:     server.URL,
		RawProfiles: false,
	})
	if err != nil {
		t.Fatalf("create compact provider: %v", err)
	}

	compactList := pCompact.ListProfiles()
	hasCanonical := false
	hasTiered := false
	for _, prof := range compactList {
		if prof.ID == "gemini-3.8-flash" {
			hasCanonical = true
		}
		if prof.ID == "gemini-3.8-flash-tiered" {
			hasTiered = true
		}
	}
	if !hasCanonical {
		t.Errorf("compact view: expected to find canonical 'gemini-3.8-flash'")
	}
	if hasTiered {
		t.Errorf("compact view: expected 'gemini-3.8-flash-tiered' to be collapsed")
	}

	// Raw view
	pRaw, err := NewProvider(&Config{
		TokenSource: auth.StaticTokenSource("dummy"),
		BaseURL:     server.URL,
		RawProfiles: true,
	})
	if err != nil {
		t.Fatalf("create raw provider: %v", err)
	}

	rawList := pRaw.ListProfiles()
	if len(rawList) <= len(compactList) {
		t.Errorf("expected raw profiles count (%d) to be greater than compact (%d)", len(rawList), len(compactList))
	}

	// GetProfile should work for canonical and alias IDs in both modes
	if _, ok := pCompact.GetProfile("gemini-3.8-flash"); !ok {
		t.Errorf("expected GetProfile('gemini-3.8-flash') to succeed in compact mode")
	}
	if _, ok := pCompact.GetProfile("gemini-3.8-flash-high"); !ok {
		t.Errorf("expected GetProfile('gemini-3.8-flash-high') to succeed in compact mode")
	}
	if _, ok := pRaw.GetProfile("gemini-3.8-flash-high"); !ok {
		t.Errorf("expected GetProfile('gemini-3.8-flash-high') to succeed in raw mode")
	}
}

func TestCustomAliasesAndResolver(t *testing.T) {
	// 1. Test custom Aliases map
	c := NewClient(auth.StaticTokenSource("dummy"), WithAliases(map[string]string{
		"fast": "gemini-3.8-flash-low",
	}))

	target, req := c.resolveModel("fast", GenerateContentRequest{})
	if target != "gemini-3.8-flash-tiered" {
		t.Errorf("expected target 'gemini-3.8-flash-tiered', got %q", target)
	}
	if req.GenerationConfig == nil || req.GenerationConfig.ThinkingConfig == nil || req.GenerationConfig.ThinkingConfig.ThinkingLevel != "LOW" {
		t.Errorf("expected thinkingLevel 'LOW', got %#v", req.GenerationConfig)
	}

	// 2. Test custom Resolver override
	customResolverCalled := false
	cCustom := NewClient(auth.StaticTokenSource("dummy"), WithResolver(func(model string, r GenerateContentRequest, avail map[string]bool) (string, GenerateContentRequest) {
		customResolverCalled = true
		return "my-custom-backend-model", r
	}))

	customTarget, _ := cCustom.resolveModel("anything", GenerateContentRequest{})
	if !customResolverCalled || customTarget != "my-custom-backend-model" {
		t.Errorf("expected custom resolver to be called and return 'my-custom-backend-model', got %q", customTarget)
	}
}

func TestQuotas(t *testing.T) {
	server := newMockModelsServer(t)
	defer server.Close()

	p, err := NewProvider(&Config{
		TokenSource: auth.StaticTokenSource("dummy"),
		BaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	ctx := context.Background()

	// 1. ListQuotas
	quotas, err := p.ListQuotas(ctx)
	if err != nil {
		t.Fatalf("ListQuotas failed: %v", err)
	}
	if len(quotas) == 0 {
		t.Fatal("expected non-empty quotas")
	}

	// 2. Direct model check
	qPro, ok, err := p.GetQuota(ctx, "gemini-2.5-pro")
	if err != nil || !ok {
		t.Fatalf("GetQuota('gemini-2.5-pro') failed: ok=%v, err=%v", ok, err)
	}
	if qPro.RemainingFraction != 1.0 {
		t.Errorf("expected remainingFraction 1.0, got %f", qPro.RemainingFraction)
	}

	// 3. Virtual canonical alias check ("gemini-3.8-flash" -> "gemini-3.8-flash-tiered")
	qFlash, ok, err := p.GetQuota(ctx, "gemini-3.8-flash")
	if err != nil || !ok {
		t.Fatalf("GetQuota('gemini-3.8-flash') failed: ok=%v, err=%v", ok, err)
	}
	if qFlash.RemainingFraction != 0.804 {
		t.Errorf("expected remainingFraction 0.804, got %f", qFlash.RemainingFraction)
	}

	// 4. Virtual effort alias check ("gemini-3.8-flash-high" -> "gemini-3.8-flash-tiered")
	qFlashHigh, ok, err := p.GetQuota(ctx, "gemini-3.8-flash-high")
	if err != nil || !ok {
		t.Fatalf("GetQuota('gemini-3.8-flash-high') failed: ok=%v, err=%v", ok, err)
	}
	if qFlashHigh.RemainingFraction != 0.804 {
		t.Errorf("expected remainingFraction 0.804, got %f", qFlashHigh.RemainingFraction)
	}

	// 5. Unknown model
	_, ok, _ = p.GetQuota(ctx, "nonexistent-model")
	if ok {
		t.Errorf("expected ok=false for unknown model")
	}
}

func TestQuotaCache(t *testing.T) {
	fetchCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1internal:fetchAvailableModels" {
			http.NotFound(w, r)
			return
		}
		fetchCount++
		resp := map[string]any{
			"models": map[string]any{
				"test-model": map[string]any{
					"quotaInfo": map[string]any{
						"remainingFraction": 0.5,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ctx := context.Background()

	// Provider with default short TTL (cached)
	pCached, err := NewProvider(&Config{
		TokenSource:   auth.StaticTokenSource("dummy"),
		BaseURL:       server.URL,
		QuotaCacheTTL: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	_, _ = pCached.ListQuotas(ctx)
	_, _ = pCached.ListQuotas(ctx)
	if fetchCount != 1 {
		t.Errorf("expected 1 fetch with positive QuotaCacheTTL, got %d", fetchCount)
	}

	// Provider with QuotaCacheTTL = -1 (always direct live fetch)
	fetchCount = 0
	pDirect, err := NewProvider(&Config{
		TokenSource:   auth.StaticTokenSource("dummy"),
		BaseURL:       server.URL,
		QuotaCacheTTL: -1,
	})
	if err != nil {
		t.Fatalf("create direct provider: %v", err)
	}

	_, _ = pDirect.ListQuotas(ctx)
	_, _ = pDirect.ListQuotas(ctx)
	if fetchCount != 2 {
		t.Errorf("expected 2 fetches with negative QuotaCacheTTL, got %d", fetchCount)
	}
}

func TestSanitizeSchema(t *testing.T) {
	input := map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$defs": map[string]any{
			"something": "val",
		},
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type": "string",
			},
			"optionalField": map[string]any{
				"type": []any{"null", "string"},
			},
			"reversedNullable": map[string]any{
				"type": []any{"integer", "null"},
			},
			"anyOfNullable": map[string]any{
				"anyOf": []any{
					map[string]any{"type": "boolean"},
					map[string]any{"type": "null"},
				},
			},
		},
	}

	sanitized, err := sanitizeSchema(input)
	if err != nil {
		t.Fatalf("sanitizeSchema failed: %v", err)
	}

	// 1. Verify stripped keys
	if _, exists := sanitized["$schema"]; exists {
		t.Errorf("expected $schema to be stripped")
	}
	if _, exists := sanitized["$defs"]; exists {
		t.Errorf("expected $defs to be stripped")
	}

	// 2. Verify root type normalized
	if sanitized["type"] != "OBJECT" {
		t.Errorf("expected root type OBJECT, got %v", sanitized["type"])
	}

	props, ok := sanitized["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties map")
	}

	// 3. Verify single string type
	nameProp := props["name"].(map[string]any)
	if nameProp["type"] != "STRING" {
		t.Errorf("expected STRING, got %v", nameProp["type"])
	}

	// 4. Verify optionalField converted from ["null", "string"] to type="STRING", nullable=true
	optProp := props["optionalField"].(map[string]any)
	if optProp["type"] != "STRING" {
		t.Errorf("expected type STRING, got %v", optProp["type"])
	}
	if optProp["nullable"] != true {
		t.Errorf("expected nullable=true, got %v", optProp["nullable"])
	}

	// 5. Verify reversedNullable converted from ["integer", "null"] to type="INTEGER", nullable=true
	revProp := props["reversedNullable"].(map[string]any)
	if revProp["type"] != "INTEGER" {
		t.Errorf("expected type INTEGER, got %v", revProp["type"])
	}
	if revProp["nullable"] != true {
		t.Errorf("expected nullable=true, got %v", revProp["nullable"])
	}

	// 6. Verify anyOf with null is flattened
	anyOfProp := props["anyOfNullable"].(map[string]any)
	if anyOfProp["type"] != "BOOLEAN" {
		t.Errorf("expected type BOOLEAN, got %v", anyOfProp["type"])
	}
	if anyOfProp["nullable"] != true {
		t.Errorf("expected nullable=true, got %v", anyOfProp["nullable"])
	}
}

func TestToTools_NullableFieldSanitization(t *testing.T) {
	type FilterInput struct {
		Name     string  `json:"name"`
		Category *string `json:"category,omitempty"`
		Limit    *int    `json:"limit,omitempty"`
	}

	myTool, err := tool.New("filter_items", "Filter", "Filter items", func(ctx context.Context, in FilterInput) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("tool.New failed: %v", err)
	}

	converted, err := toTools([]tool.Definition{myTool.Definition})
	if err != nil {
		t.Fatalf("toTools failed: %v", err)
	}

	if len(converted.FunctionDeclarations) != 1 {
		t.Fatalf("expected 1 function declaration")
	}

	decl := converted.FunctionDeclarations[0]
	params, ok := decl.Parameters.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any parameters, got %T", decl.Parameters)
	}

	props := params["properties"].(map[string]any)
	cat := props["category"].(map[string]any)

	// In raw jsonschema-go, category has type: ["null", "string"]
	// Verify it was sanitized to type: "STRING" and nullable: true
	if cat["type"] != "STRING" {
		t.Errorf("expected category type STRING, got %v", cat["type"])
	}
	if cat["nullable"] != true {
		t.Errorf("expected category nullable true, got %v", cat["nullable"])
	}

	limit := props["limit"].(map[string]any)
	if limit["type"] != "INTEGER" {
		t.Errorf("expected limit type INTEGER, got %v", limit["type"])
	}
	if limit["nullable"] != true {
		t.Errorf("expected limit nullable true, got %v", limit["nullable"])
	}
}

func TestThoughtSignatureMapping(t *testing.T) {
	// 1. Verify toAssistantChunk preserves ThoughtSignature in Extras
	sig := "test-signature-12345"
	resp := &GenerateContentResponse{
		Candidates: []*Candidate{
			{
				Content: &Content{
					Role: "model",
					Parts: []Part{
						{
							Thought:          true,
							Text:             "thinking...",
							ThoughtSignature: sig,
						},
						{
							Text:             "hello",
							ThoughtSignature: sig,
						},
						{
							FunctionCall: &FunctionCall{
								ID:   "call_1",
								Name: "my_tool",
								Args: map[string]any{"a": "b"},
							},
							ThoughtSignature: sig,
						},
					},
				},
			},
		},
	}

	chunk, err := toAssistantChunk(resp, "gemini-3.8-flash")
	if err != nil {
		t.Fatalf("toAssistantChunk failed: %v", err)
	}

	tb, ok := chunk.Content[0].(*message.ThinkingBlock)
	if !ok || tb.Extras[ThoughtSignatureKey] != sig {
		t.Errorf("ThinkingBlock: expected signature %q, got %v", sig, tb.Extras[ThoughtSignatureKey])
	}

	txt, ok := chunk.Content[1].(*message.TextBlock)
	if !ok || txt.Extras[ThoughtSignatureKey] != sig {
		t.Errorf("TextBlock: expected signature %q, got %v", sig, txt.Extras[ThoughtSignatureKey])
	}

	tc, ok := chunk.Content[2].(*message.ToolCall)
	if !ok || tc.Extras[ThoughtSignatureKey] != sig {
		t.Errorf("ToolCall: expected signature %q, got %v", sig, tc.Extras[ThoughtSignatureKey])
	}

	// 2. Verify toAssistantParts copies signature back to Part
	asstContent := message.Content{tb, txt, tc}
	parts, err := toAssistantParts(asstContent)
	if err != nil {
		t.Fatalf("toAssistantParts failed: %v", err)
	}

	for i, p := range parts {
		if p.ThoughtSignature != sig {
			t.Errorf("part %d: expected ThoughtSignature %q, got %q", i, sig, p.ThoughtSignature)
		}
	}

	// 3. Verify toAssistantParts injects DummyThoughtSignature when tool call lacks signature
	tcWithoutSig := &message.ToolCall{
		ID:   "call_no_sig",
		Name: "some_tool",
	}
	partsNoSig, err := toAssistantParts(message.Content{tcWithoutSig})
	if err != nil {
		t.Fatalf("toAssistantParts failed: %v", err)
	}
	if partsNoSig[0].ThoughtSignature != DummyThoughtSignature {
		t.Errorf("expected DummyThoughtSignature for tool call without signature, got %q", partsNoSig[0].ThoughtSignature)
	}

	// 4. Verify Part.UnmarshalJSON handles snake_case thought_signature
	var p Part
	if err := json.Unmarshal([]byte(`{"text":"hi","thought_signature":"snake-sig"}`), &p); err != nil {
		t.Fatalf("UnmarshalJSON failed: %v", err)
	}
	if p.ThoughtSignature != "snake-sig" {
		t.Errorf("expected snake_case thought_signature to be unmarshaled, got %q", p.ThoughtSignature)
	}
}

func TestCountTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1internal:countTokens" {
			http.NotFound(w, r)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer token-123" {
			t.Errorf("expected Authorization 'Bearer token-123', got %q", auth)
		}
		var env countTokensEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			t.Errorf("failed to decode countTokensEnvelope: %v", err)
		}
		if len(env.Request.Contents) != 2 {
			t.Errorf("expected 2 contents, got %d", len(env.Request.Contents))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"totalTokens": 42}`)
	}))
	defer server.Close()

	p, err := NewProvider(&Config{
		TokenSource: auth.StaticTokenSource("token-123"),
		BaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	msgs := message.MessageList{
		message.NewSystemText("You are helpful."),
		message.NewUserText("Hello world!"),
	}

	count, err := p.CountTokens(context.Background(), msgs)
	if err != nil {
		t.Fatalf("CountTokens failed: %v", err)
	}
	if count != 42 {
		t.Errorf("expected 42 tokens, got %d", count)
	}
}

func TestCountTokens_Fallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	p, err := NewProvider(&Config{
		TokenSource: auth.StaticTokenSource("token-123"),
		BaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	// 16 chars ≈ 4 tokens via ApproximateTokenCounter
	msgs := message.MessageList{
		message.NewUserText("1234567890123456"),
	}

	count, err := p.CountTokens(context.Background(), msgs)
	if err != nil {
		t.Fatalf("CountTokens failed: %v", err)
	}
	if count != 4 {
		t.Errorf("expected 4 tokens from fallback, got %d", count)
	}
}

func TestToGenerateContentRequest_GroupsConsecutiveToolResults(t *testing.T) {
	req := &llm.Request{
		Model: "gemini-2.5-flash",
		Messages: message.MessageList{
			&message.User{
				Content: message.Content{&message.TextBlock{Text: "Fetch info"}},
			},
			&message.Assistant{
				Content: message.Content{
					&message.ToolCall{ID: "call_1", Name: "tool_1"},
					&message.ToolCall{ID: "call_2", Name: "tool_2"},
				},
			},
			&message.Tool{
				ToolCallID: "call_1",
				Name:       "tool_1",
				Content:    message.Content{&message.TextBlock{Text: "result 1"}},
			},
			&message.Tool{
				ToolCallID: "call_2",
				Name:       "tool_2",
				Content:    message.Content{&message.TextBlock{Text: "result 2"}},
			},
			&message.User{
				Content: message.Content{&message.TextBlock{Text: "Thank you"}},
			},
		},
	}

	result, err := toGenerateContentRequest(req)
	if err != nil {
		t.Fatalf("toGenerateContentRequest failed: %v", err)
	}

	if len(result.Contents) != 4 {
		t.Fatalf("expected 4 contents, got %d", len(result.Contents))
	}

	toolTurn := result.Contents[2]
	if toolTurn.Role != "user" {
		t.Errorf("expected role user, got %q", toolTurn.Role)
	}
	if len(toolTurn.Parts) != 2 {
		t.Fatalf("expected 2 parts in tool turn, got %d", len(toolTurn.Parts))
	}
	if toolTurn.Parts[0].FunctionResponse == nil || toolTurn.Parts[0].FunctionResponse.Name != "tool_1" {
		t.Errorf("expected first part to be FunctionResponse for tool_1, got %#v", toolTurn.Parts[0])
	}
	if toolTurn.Parts[1].FunctionResponse == nil || toolTurn.Parts[1].FunctionResponse.Name != "tool_2" {
		t.Errorf("expected second part to be FunctionResponse for tool_2, got %#v", toolTurn.Parts[1])
	}
}

func TestToGenerateContentRequest_DeferredToolsFailFast(t *testing.T) {
	// 1. Tool with DeferLoading: true
	reqDeferred := &llm.Request{
		Model: "gemini-2.5-flash",
		Tools: []tool.Definition{
			{Name: "search_tool", DeferLoading: true},
		},
	}
	_, err := toGenerateContentRequest(reqDeferred)
	if err == nil {
		t.Errorf("expected error for deferred tool, got nil")
	}

	// 2. Message with ToolReferenceBlock
	reqRef := &llm.Request{
		Model: "gemini-2.5-flash",
		Messages: []message.Message{
			message.NewUser(&message.ToolReferenceBlock{ToolName: "search_tool"}),
		},
	}
	_, err = toGenerateContentRequest(reqRef)
	if err == nil {
		t.Errorf("expected error for ToolReferenceBlock, got nil")
	}

	// 3. Message with nested ToolReferenceBlock in ServerToolResult
	reqNested := &llm.Request{
		Model: "gemini-2.5-flash",
		Messages: []message.Message{
			&message.Assistant{
				Content: message.Content{
					&message.ServerToolResult{
						ToolCallID: "srv-1",
						Content: message.Content{
							&message.ToolReferenceBlock{ToolName: "search_tool"},
						},
					},
				},
			},
		},
	}
	_, err = toGenerateContentRequest(reqNested)
	if err == nil {
		t.Errorf("expected error for nested ToolReferenceBlock, got nil")
	}

	// 4. Message with ToolDefinitionBlock
	reqDef := &llm.Request{
		Model: "gemini-2.5-flash",
		Messages: []message.Message{
			message.NewUser(&message.ToolDefinitionBlock{Name: "custom_tool"}),
		},
	}
	_, err = toGenerateContentRequest(reqDef)
	if err == nil {
		t.Errorf("expected error for ToolDefinitionBlock, got nil")
	}
}

func TestToTools_DeferredToolFailFast(t *testing.T) {
	_, err := toTools([]tool.Definition{{Name: "deferred_tool", DeferLoading: true}})
	if err == nil {
		t.Errorf("expected error for deferred tool in toTools, got nil")
	}
}

func TestUserAgent_DefaultAndOverrides(t *testing.T) {
	expectedPlatform := fmt.Sprintf("(%s/%s)", runtime.GOOS, runtime.GOARCH)
	if !strings.HasPrefix(DefaultUserAgent, "antigravity/cli/") || !strings.Contains(DefaultUserAgent, expectedPlatform) {
		t.Fatalf("expected versioned CLI DefaultUserAgent containing %q, got %q", expectedPlatform, DefaultUserAgent)
	}

	// Default User-Agent sent in both HTTP header and RequestEnvelope
	var gotHeaderUA, gotBodyUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1internal:streamGenerateContent" {
			http.NotFound(w, r)
			return
		}
		gotHeaderUA = r.Header.Get("User-Agent")
		var env RequestEnvelope
		_ = json.NewDecoder(r.Body).Decode(&env)
		gotBodyUA = env.UserAgent
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"response\":{\"candidates\":[{\"finishReason\":\"STOP\"}]}}\n\n")
	}))
	defer server.Close()

	cDefault := NewClient(auth.StaticTokenSource("tok"), WithBaseURL(server.URL))
	stream, err := cDefault.StreamGenerateContent(context.Background(), "gemini-2.5-pro", GenerateContentRequest{})
	if err != nil {
		t.Fatalf("StreamGenerateContent failed: %v", err)
	}
	for range stream {
	}
	if gotHeaderUA != DefaultUserAgent || gotBodyUA != DefaultUserAgent {
		t.Errorf("expected header/body UA %q, got header=%q body=%q", DefaultUserAgent, gotHeaderUA, gotBodyUA)
	}

	// ANTIGRAVITY_USER_AGENT env override
	t.Setenv("ANTIGRAVITY_USER_AGENT", "antigravity/cli/9.9.9 (test/arch)")
	cEnv := NewClient(auth.StaticTokenSource("tok"), WithBaseURL(server.URL))
	if cEnv.userAgent != "antigravity/cli/9.9.9 (test/arch)" {
		t.Errorf("expected env UA override, got %q", cEnv.userAgent)
	}

	// Explicit WithUserAgent overrides ANTIGRAVITY_USER_AGENT
	cExplicit := NewClient(auth.StaticTokenSource("tok"), WithBaseURL(server.URL), WithUserAgent("custom-ua/1.0"))
	if cExplicit.userAgent != "custom-ua/1.0" {
		t.Errorf("expected explicit UA override, got %q", cExplicit.userAgent)
	}
}

type dynamicTokenSource struct {
	token atomic.Value
}

func (d *dynamicTokenSource) Token() (*oauth2.Token, error) {
	v, _ := d.token.Load().(string)
	if v == "" {
		return nil, fmt.Errorf("no token")
	}
	return &oauth2.Token{AccessToken: v}, nil
}

func TestLoadCodeAssist_CompanionProjectAndHeaders(t *testing.T) {
	var loadCalls atomic.Int32
	var gotModelsProject, gotStreamProject string
	var gotLoadHeaderProj, gotModelsHeaderProj, gotStreamHeaderProj string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			loadCalls.Add(1)
			gotLoadHeaderProj = r.Header.Get("X-Goog-User-Project")

			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode loadCodeAssist body: %v", err)
			}
			meta, _ := body["metadata"].(map[string]any)
			if meta["ideType"] != "ANTIGRAVITY" {
				t.Errorf("expected metadata.ideType='ANTIGRAVITY', got %#v", body)
			}

			w.Header().Set("Content-Type", "application/json")
			if r.Header.Get("Authorization") == "Bearer token-acct-2" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"cloudaicompanionProject": map[string]any{"id": "companion-acct-2"},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"cloudaicompanionProject": "companion-acct-1",
			})

		case "/v1internal:fetchAvailableModels":
			gotModelsHeaderProj = r.Header.Get("X-Goog-User-Project")
			var body struct {
				Project string `json:"project"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotModelsProject = body.Project

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": map[string]any{
					"gemini-4-carbon-high": map[string]any{"displayName": "Gemini 4 Carbon (High)"},
				},
			})

		case "/v1internal:streamGenerateContent":
			gotStreamHeaderProj = r.Header.Get("X-Goog-User-Project")
			var env RequestEnvelope
			_ = json.NewDecoder(r.Body).Decode(&env)
			gotStreamProject = env.Project

			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"response\":{\"candidates\":[{\"finishReason\":\"STOP\"}]}}\n\n")

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ts := &dynamicTokenSource{}
	ts.token.Store("token-acct-1")

	// 1. Consumer account: auto-discovers companion project, omits X-Goog-User-Project
	p, err := NewProvider(&Config{
		TokenSource: ts,
		BaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	ctx := context.Background()
	stream, err := p.Stream(ctx, &llm.Request{
		Model:    "gemini-4-carbon-high",
		Messages: []message.Message{message.NewUserText("hello")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	for range stream {
	}

	// Second stream with same token must NOT call loadCodeAssist again
	stream2, err := p.Stream(ctx, &llm.Request{
		Model:    "gemini-4-carbon-high",
		Messages: []message.Message{message.NewUserText("again")},
	})
	if err != nil {
		t.Fatalf("Stream 2 failed: %v", err)
	}
	for range stream2 {
	}

	if got := loadCalls.Load(); got != 1 {
		t.Errorf("expected loadCodeAssist to be called once for token-acct-1, got %d", got)
	}
	if gotModelsProject != "companion-acct-1" {
		t.Errorf("expected fetchAvailableModels body project 'companion-acct-1', got %q", gotModelsProject)
	}
	if gotStreamProject != "companion-acct-1" {
		t.Errorf("expected streamGenerateContent body project 'companion-acct-1', got %q", gotStreamProject)
	}
	if gotLoadHeaderProj != "" || gotModelsHeaderProj != "" || gotStreamHeaderProj != "" {
		t.Errorf("expected X-Goog-User-Project header to be omitted for companion project, got load=%q models=%q stream=%q",
			gotLoadHeaderProj, gotModelsHeaderProj, gotStreamHeaderProj)
	}

	// 2. Account switch (token-acct-2) resolves object-shaped cloudaicompanionProject once
	ts.token.Store("token-acct-2")
	stream3, err := p.Stream(ctx, &llm.Request{
		Model:    "gemini-4-carbon-high",
		Messages: []message.Message{message.NewUserText("acct 2")},
	})
	if err != nil {
		t.Fatalf("Stream 3 failed: %v", err)
	}
	for range stream3 {
	}
	if got := loadCalls.Load(); got != 2 {
		t.Errorf("expected loadCodeAssist to be called once more on token change, got %d", got)
	}
	if gotStreamProject != "companion-acct-2" {
		t.Errorf("expected stream project 'companion-acct-2', got %q", gotStreamProject)
	}

	// 3. Explicit billing ProjectID + CompanionProject separation
	pBilling, err := NewProvider(&Config{
		TokenSource:      auth.StaticTokenSource("token-billing"),
		BaseURL:          server.URL,
		ProjectID:        "my-billing-project",
		CompanionProject: "explicit-companion-proj",
	})
	if err != nil {
		t.Fatalf("NewProvider billing: %v", err)
	}
	stream4, err := pBilling.Stream(ctx, &llm.Request{
		Model:    "gemini-4-carbon-high",
		Messages: []message.Message{message.NewUserText("billing")},
	})
	if err != nil {
		t.Fatalf("Stream 4 failed: %v", err)
	}
	for range stream4 {
	}
	if got := loadCalls.Load(); got != 2 {
		t.Errorf("expected explicit CompanionProject to skip loadCodeAssist, got %d total calls", got)
	}
	if gotStreamProject != "explicit-companion-proj" {
		t.Errorf("expected body project 'explicit-companion-proj', got %q", gotStreamProject)
	}
	if gotStreamHeaderProj != "my-billing-project" {
		t.Errorf("expected X-Goog-User-Project 'my-billing-project', got %q", gotStreamHeaderProj)
	}
}

func TestResolveModel_ColdCacheAndNoEffortFallback(t *testing.T) {
	var modelsCalls atomic.Int32
	var seenModels []string
	var seenThinkingLevels []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"cloudaicompanionProject": "proj-1"})

		case "/v1internal:fetchAvailableModels":
			if r.Header.Get("Authorization") != "Bearer valid-token" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			modelsCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": map[string]any{
					"gemini-4-carbon-low":     map[string]any{"displayName": "Gemini 4 Carbon (Low)", "supportsThinking": true},
					"gemini-4-carbon-medium":  map[string]any{"displayName": "Gemini 4 Carbon (Medium)", "supportsThinking": true},
					"gemini-4-carbon-high":    map[string]any{"displayName": "Gemini 4 Carbon (High)", "supportsThinking": true},
					"claude-sonnet-5-5-low":   map[string]any{"displayName": "Claude Sonnet 5.5 (Low)", "supportsThinking": true},
					"claude-sonnet-5-5-high":  map[string]any{"displayName": "Claude Sonnet 5.5 (High)", "supportsThinking": true},
					"gemini-4-argon-high":     map[string]any{"displayName": "Gemini 4 Argon (High)", "supportsThinking": true},
					"gemini-3.8-flash-tiered": map[string]any{"displayName": "Gemini 3.8 Flash", "supportsThinking": true},
				},
			})

		case "/v1internal:streamGenerateContent":
			var env RequestEnvelope
			_ = json.NewDecoder(r.Body).Decode(&env)
			seenModels = append(seenModels, env.Model)
			lvl := ""
			if env.Request.GenerationConfig != nil && env.Request.GenerationConfig.ThinkingConfig != nil {
				lvl = env.Request.GenerationConfig.ThinkingConfig.ThinkingLevel
			}
			seenThinkingLevels = append(seenThinkingLevels, lvl)

			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"response\":{\"candidates\":[{\"finishReason\":\"STOP\"}]}}\n\n")

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ts := &dynamicTokenSource{}
	ts.token.Store("expired-token")

	p, err := NewProvider(&Config{
		TokenSource: ts,
		BaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	// Simulate an initial unauthenticated profile fetch leaving availableModels empty.
	if err := p.SyncProfiles(context.Background()); err == nil {
		t.Fatal("expected SyncProfiles with expired token to fail")
	}

	// Authenticate and invoke Stream directly with cold availableModels cache.
	ts.token.Store("valid-token")
	ctx := context.Background()

	cases := []struct {
		name          string
		req           *llm.Request
		expectedModel string
		expectedLevel string
	}{
		{
			name: "cold cache effort-configured model routes to per-effort endpoint with uppercase HIGH",
			req: &llm.Request{
				Model:    "gemini-4-carbon",
				Thinking: &llm.ThinkingConfig{Effort: "high"},
				Messages: []message.Message{message.NewUserText("hi")},
			},
			expectedModel: "gemini-4-carbon-high",
			expectedLevel: "HIGH",
		},
		{
			name: "effort-suffixed model routes directly without rewriting to -tiered",
			req: &llm.Request{
				Model:    "claude-sonnet-5-5-high",
				Messages: []message.Message{message.NewUserText("hi")},
			},
			expectedModel: "claude-sonnet-5-5-high",
			expectedLevel: "",
		},
		{
			name: "no-effort canonical family prefers -medium variant",
			req: &llm.Request{
				Model:    "gemini-4-carbon",
				Messages: []message.Message{message.NewUserText("title")},
			},
			expectedModel: "gemini-4-carbon-medium",
			expectedLevel: "",
		},
		{
			name: "no-effort canonical family without -medium falls back to -low",
			req: &llm.Request{
				Model:    "claude-sonnet-5-5",
				Messages: []message.Message{message.NewUserText("title")},
			},
			expectedModel: "claude-sonnet-5-5-low",
			expectedLevel: "",
		},
		{
			name: "no-effort canonical family with only -high falls back to -high",
			req: &llm.Request{
				Model:    "gemini-4-argon",
				Messages: []message.Message{message.NewUserText("title")},
			},
			expectedModel: "gemini-4-argon-high",
			expectedLevel: "",
		},
		{
			name: "tiered model with lowercase effort normalizes ThinkingLevel to MEDIUM",
			req: &llm.Request{
				Model:    "gemini-3.8-flash",
				Thinking: &llm.ThinkingConfig{Effort: "medium"},
				Messages: []message.Message{message.NewUserText("hi")},
			},
			expectedModel: "gemini-3.8-flash-tiered",
			expectedLevel: "MEDIUM",
		},
	}

	for i, tc := range cases {
		stream, err := p.Stream(ctx, tc.req)
		if err != nil {
			t.Fatalf("%s: Stream failed: %v", tc.name, err)
		}
		for range stream {
		}
		if seenModels[i] != tc.expectedModel {
			t.Errorf("%s: expected resolved model %q, got %q", tc.name, tc.expectedModel, seenModels[i])
		}
		if seenThinkingLevels[i] != tc.expectedLevel {
			t.Errorf("%s: expected thinkingLevel %q, got %q", tc.name, tc.expectedLevel, seenThinkingLevels[i])
		}
	}

	if got := modelsCalls.Load(); got != 1 {
		t.Errorf("expected availableModels to be lazily fetched once across Stream calls, got %d", got)
	}
}

func TestThinkingLevel_NormalizedUppercase(t *testing.T) {
	for _, eff := range []struct {
		in   string
		want string
	}{
		{"low", "LOW"},
		{"medium", "MEDIUM"},
		{"high", "HIGH"},
	} {
		wire, err := toGenerateContentRequest(&llm.Request{
			Model:    "gemini-4-carbon",
			Thinking: &llm.ThinkingConfig{Effort: eff.in},
			Messages: []message.Message{message.NewUserText("test")},
		})
		if err != nil {
			t.Fatalf("toGenerateContentRequest(%q): %v", eff.in, err)
		}
		if wire.GenerationConfig == nil || wire.GenerationConfig.ThinkingConfig == nil {
			t.Fatalf("expected ThinkingConfig for %q", eff.in)
		}
		if got := wire.GenerationConfig.ThinkingConfig.ThinkingLevel; got != eff.want {
			t.Errorf("toGenerateContentRequest(%q): expected ThinkingLevel %q, got %q", eff.in, eff.want, got)
		}
	}

	// Verify withThinkingLevel normalizes pre-populated lowercase ThinkingLevel without mutating input pointer
	origTC := &ThinkingConfig{IncludeThoughts: true, ThinkingLevel: "high"}
	origReq := GenerateContentRequest{GenerationConfig: &GenerationConfig{ThinkingConfig: origTC}}
	updated := withThinkingLevel(origReq, "LOW")
	if got := updated.GenerationConfig.ThinkingConfig.ThinkingLevel; got != "HIGH" {
		t.Errorf("expected withThinkingLevel to normalize existing 'high' to 'HIGH', got %q", got)
	}
	if origTC.ThinkingLevel != "high" {
		t.Errorf("expected original ThinkingConfig not to be mutated, got %q", origTC.ThinkingLevel)
	}
}

func TestCompactedPerEffortProfilesAndQuotas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"cloudaicompanionProject": "proj-1"})

		case "/v1internal:fetchAvailableModels":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": map[string]any{
					"gemini-4-carbon-low": map[string]any{
						"displayName":      "Gemini 4 Carbon (Low)",
						"maxTokens":        1048576,
						"maxOutputTokens":  32768,
						"supportsThinking": true,
						"supportsImages":   true,
						"quotaInfo": map[string]any{
							"remainingFraction": 0.75,
						},
					},
					"gemini-4-carbon-medium": map[string]any{
						"displayName":      "Gemini 4 Carbon (Medium)",
						"maxTokens":        1048576,
						"maxOutputTokens":  65536,
						"supportsThinking": true,
						"supportsImages":   true,
						"quotaInfo": map[string]any{
							"remainingFraction": 0.65,
						},
					},
					"gemini-4-carbon-high": map[string]any{
						"displayName":      "Gemini 4 Carbon (High)",
						"maxTokens":        2097152,
						"maxOutputTokens":  65536,
						"supportsThinking": true,
						"supportsImages":   true,
						"quotaInfo": map[string]any{
							"remainingFraction": 0.55,
						},
					},
					"claude-sonnet-5-5-low": map[string]any{
						"displayName":      "Claude Sonnet 5.5 (Low)",
						"maxTokens":        200000,
						"maxOutputTokens":  64000,
						"supportsThinking": true,
					},
					"claude-sonnet-5-5-high": map[string]any{
						"displayName":      "Claude Sonnet 5.5 (High)",
						"maxTokens":        200000,
						"maxOutputTokens":  64000,
						"supportsThinking": true,
						"quotaInfo": map[string]any{
							"remainingFraction": 0.42,
						},
					},
					"gemini-3.8-flash-tiered": map[string]any{
						"displayName":      "Gemini 3.8 Flash",
						"maxTokens":        1048576,
						"maxOutputTokens":  65536,
						"supportsThinking": true,
					},
				},
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p, err := NewProvider(&Config{
		TokenSource: auth.StaticTokenSource("tok"),
		BaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	// 1. GetProfile for per-effort canonical family ("gemini-4-carbon")
	carbon, ok := p.GetProfile("gemini-4-carbon")
	if !ok {
		t.Fatal("expected GetProfile('gemini-4-carbon') to succeed")
	}
	if carbon.ID != "gemini-4-carbon" || carbon.Name != "Gemini 4 Carbon" {
		t.Errorf("unexpected carbon ID/Name: %q / %q", carbon.ID, carbon.Name)
	}
	if carbon.Limits.Context != 2097152 || carbon.Limits.Output != 65536 {
		t.Errorf("expected max family limits (2097152 / 65536), got %+v", carbon.Limits)
	}
	if len(carbon.Capabilities.ReasoningOptions) != 1 || !slices.Equal(carbon.Capabilities.ReasoningOptions[0].Values, []string{"low", "medium", "high"}) {
		t.Errorf("expected ReasoningOptions [low medium high], got %+v", carbon.Capabilities.ReasoningOptions)
	}

	// 2. GetProfile for partial per-effort family ("claude-sonnet-5-5")
	claude, ok := p.GetProfile("claude-sonnet-5-5")
	if !ok {
		t.Fatal("expected GetProfile('claude-sonnet-5-5') to succeed")
	}
	if len(claude.Capabilities.ReasoningOptions) != 1 || !slices.Equal(claude.Capabilities.ReasoningOptions[0].Values, []string{"low", "high"}) {
		t.Errorf("expected ReasoningOptions [low high], got %+v", claude.Capabilities.ReasoningOptions)
	}

	// 3. GetProfile for tiered canonical model also includes ReasoningOptions
	flash, ok := p.GetProfile("gemini-3.8-flash")
	if !ok {
		t.Fatal("expected GetProfile('gemini-3.8-flash') to succeed")
	}
	if len(flash.Capabilities.ReasoningOptions) != 1 || !slices.Equal(flash.Capabilities.ReasoningOptions[0].Values, []string{"low", "medium", "high"}) {
		t.Errorf("expected ReasoningOptions [low medium high] on gemini-3.8-flash, got %+v", flash.Capabilities.ReasoningOptions)
	}

	// 4. Every profile returned by ListProfiles() must be resolvable via GetProfile()
	for _, listed := range p.ListProfiles() {
		got, found := p.GetProfile(listed.ID)
		if !found {
			t.Errorf("ListProfiles returned %q, but GetProfile(%q) returned false", listed.ID, listed.ID)
		}
		if len(listed.Capabilities.ReasoningOptions) != len(got.Capabilities.ReasoningOptions) {
			t.Errorf("profile %q ReasoningOptions mismatch between ListProfiles and GetProfile", listed.ID)
		}
	}

	// 5. GetQuota resolves canonical IDs across per-effort quota keys
	ctx := context.Background()
	qCarbon, ok, err := p.GetQuota(ctx, "gemini-4-carbon")
	if err != nil || !ok {
		t.Fatalf("expected GetQuota('gemini-4-carbon') to succeed, ok=%v err=%v", ok, err)
	}
	if qCarbon.RemainingFraction != 0.65 {
		t.Errorf("expected remainingFraction 0.65 (default medium target), got %f", qCarbon.RemainingFraction)
	}

	// claude-sonnet-5-5 only has quotaInfo on -high (and resolves no-effort to -low which lacks quotaInfo,
	// so GetQuota's per-effort fallback finds -high!)
	qClaude, ok, err := p.GetQuota(ctx, "claude-sonnet-5-5")
	if err != nil || !ok {
		t.Fatalf("expected GetQuota('claude-sonnet-5-5') to fall back to -high quota, ok=%v err=%v", ok, err)
	}
	if qClaude.RemainingFraction != 0.42 {
		t.Errorf("expected remainingFraction 0.42, got %f", qClaude.RemainingFraction)
	}
}
