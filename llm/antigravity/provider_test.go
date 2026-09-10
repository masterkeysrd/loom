package loomantigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/tool"
)

func TestStaticTokenProvider(t *testing.T) {
	tp := StaticToken("my-secret-token")
	token, err := tp.Token(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if token != "my-secret-token" {
		t.Fatalf("expected %q, got %q", "my-secret-token", token)
	}

	emptyTP := StaticToken("")
	if _, err := emptyTP.Token(context.Background()); err == nil {
		t.Fatal("expected error for empty static token, got nil")
	}
}

func TestEnvTokenProvider(t *testing.T) {
	t.Setenv("ANTIGRAVITY_TOKEN", "env-test-token")
	tp := NewEnvTokenProvider()
	token, err := tp.Token(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if token != "env-test-token" {
		t.Fatalf("expected %q, got %q", "env-test-token", token)
	}
}

func TestProvider_Stream(t *testing.T) {
	// 1. Setup mock SSE HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		TokenProvider: StaticToken("test-bearer-token"),
		BaseURL:       server.URL,
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
		TokenProvider: StaticToken("dummy"),
		BaseURL:       server.URL,
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
		TokenProvider: StaticToken("dummy-token"),
		BaseURL:       server.URL,
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
		TokenProvider: StaticToken("dummy"),
		BaseURL:       server.URL,
		RawProfiles:   false,
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
		TokenProvider: StaticToken("dummy"),
		BaseURL:       server.URL,
		RawProfiles:   true,
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
	c := NewClient(StaticToken("dummy"), WithAliases(map[string]string{
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
	cCustom := NewClient(StaticToken("dummy"), WithResolver(func(model string, r GenerateContentRequest, avail map[string]bool) (string, GenerateContentRequest) {
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
		TokenProvider: StaticToken("dummy"),
		BaseURL:       server.URL,
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
		TokenProvider: StaticToken("dummy"),
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
		TokenProvider: StaticToken("dummy"),
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
		TokenProvider: StaticToken("token-123"),
		BaseURL:       server.URL,
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
		TokenProvider: StaticToken("token-123"),
		BaseURL:       server.URL,
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

