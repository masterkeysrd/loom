package loomgenai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/genai"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestGenAIProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			handler(rec, req)
			return rec.Result(), nil
		}),
	}
	p, err := NewProvider(context.Background(), &genai.ClientConfig{
		APIKey:     "test-api-key",
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}
	return p
}

func TestListModels_PaginationAndEnrichment(t *testing.T) {
	p := newTestGenAIProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1beta/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		pageToken := r.URL.Query().Get("pageToken")
		if pageToken == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{
					{
						"name":                       "models/gemini-2.5-pro",
						"displayName":                "Gemini 2.5 Pro",
						"description":                "Mid-size multimodal model",
						"inputTokenLimit":            1048576,
						"outputTokenLimit":           65536,
						"supportedGenerationMethods": []string{"generateContent", "countTokens"},
						"thinking":                   true,
					},
					{
						"name":                       "models/gemini-embedding-001",
						"displayName":                "Gemini Embedding 001",
						"inputTokenLimit":            2048,
						"outputTokenLimit":           1,
						"supportedGenerationMethods": []string{"embedContent"},
					},
				},
				"nextPageToken": "page-2",
			})
			return
		}
		if pageToken == "page-2" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{
					{
						"name":                       "models/gemini-9-experimental",
						"displayName":                "Gemini 9 Experimental",
						"description":                "Next-gen experimental Gemini",
						"inputTokenLimit":            2097152,
						"outputTokenLimit":           131072,
						"supportedGenerationMethods": []string{"generateContent"},
						"thinking":                   true,
						"maxTemperature":             2.0,
					},
					{
						"name":                       "tunedModels/custom-classifier-1",
						"displayName":                "Custom Classifier",
						"inputTokenLimit":            32768,
						"outputTokenLimit":           8192,
						"supportedGenerationMethods": []string{"generateContent"},
					},
				},
			})
			return
		}
		http.NotFound(w, r)
	})

	beforeCount := len(p.ListProfiles())

	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) != 4 {
		t.Fatalf("expected 4 models across 2 pages, got %d", len(models))
	}

	// 1. Known Gemini model strips "models/" prefix and preserves static pricing + tiered limits
	pro := models[0]
	if pro.ID != "gemini-2.5-pro" {
		t.Errorf("expected normalized ID 'gemini-2.5-pro', got %q", pro.ID)
	}
	if pro.Pricing.IsZero() {
		t.Error("expected static pricing preserved for gemini-2.5-pro")
	}

	// 2. Known embedding model preserves staticProfileOverrides (Dimensions=3072)
	embed := models[1]
	if embed.ID != "gemini-embedding-001" {
		t.Errorf("expected ID 'gemini-embedding-001', got %q", embed.ID)
	}
	if embed.Limits.Dimensions != 3072 {
		t.Errorf("expected staticProfileOverrides Dimensions=3072 preserved, got %d", embed.Limits.Dimensions)
	}

	// 3. Unknown future Gemini model gets live limits, reasoning, and multimodal capabilities
	exp := models[2]
	if exp.ID != "gemini-9-experimental" || exp.Name != "Gemini 9 Experimental" {
		t.Errorf("unexpected experimental model identity: %+v", exp)
	}
	if exp.Limits.Context != 2097152 || exp.Limits.Output != 131072 {
		t.Errorf("unexpected experimental limits: %+v", exp.Limits)
	}
	if !exp.Capabilities.Completion || !exp.Capabilities.Reasoning || !exp.Capabilities.Attachment || !exp.Capabilities.ToolCall {
		t.Errorf("unexpected experimental capabilities: %+v", exp.Capabilities)
	}

	// 4. Tuned model preserves "tunedModels/" prefix
	tuned := models[3]
	if tuned.ID != "tunedModels/custom-classifier-1" {
		t.Errorf("expected tunedModels prefix preserved, got %q", tuned.ID)
	}
	if !tuned.Capabilities.Completion {
		t.Error("expected tuned model to have Completion=true")
	}

	// ListProfiles must remain untouched
	if afterCount := len(p.ListProfiles()); afterCount != beforeCount {
		t.Errorf("ListProfiles count mutated: before=%d, after=%d", beforeCount, afterCount)
	}
}

func TestGetModel(t *testing.T) {
	p := newTestGenAIProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1beta/models/gemini-2.5-pro" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":                       "models/gemini-2.5-pro",
				"displayName":                "Gemini 2.5 Pro",
				"inputTokenLimit":            1048576,
				"outputTokenLimit":           65536,
				"supportedGenerationMethods": []string{"generateContent"},
				"thinking":                   true,
			})
			return
		}
		http.Error(w, `{"error":{"code":404,"message":"not found"}}`, http.StatusNotFound)
	})

	if _, err := p.GetModel(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty model ID")
	}

	// Both bare ID and "models/..." resource name should work via the GenAI SDK
	for _, queryID := range []string{"gemini-2.5-pro", "models/gemini-2.5-pro"} {
		prof, err := p.GetModel(context.Background(), queryID)
		if err != nil {
			t.Fatalf("GetModel(%q) failed: %v", queryID, err)
		}
		if prof.ID != "gemini-2.5-pro" {
			t.Errorf("GetModel(%q): expected ID 'gemini-2.5-pro', got %q", queryID, prof.ID)
		}
		if prof.Pricing.IsZero() {
			t.Errorf("GetModel(%q): expected static pricing preserved", queryID)
		}
	}

	if _, err := p.GetModel(context.Background(), "non-existent"); err == nil {
		t.Fatal("expected error for non-existent model")
	}
}
