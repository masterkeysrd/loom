package loomanthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/masterkeysrd/loom/profile"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestAnthropicProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			handler(rec, req)
			return rec.Result(), nil
		}),
	}
	client := anthropic.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL("https://api.anthropic.test"),
		option.WithHTTPClient(httpClient),
		option.WithMaxRetries(0),
	)
	return NewProvider(&client)
}

func TestListModels_PaginationAndEnrichment(t *testing.T) {
	p := newTestAnthropicProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		afterID := r.URL.Query().Get("after_id")
		if afterID == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{
						"id":               "claude-sonnet-4-6",
						"type":             "model",
						"display_name":     "Claude Sonnet 4.6",
						"created_at":       "2026-02-17T00:00:00Z",
						"lifecycle":        "active",
						"line":             "sonnet",
						"max_input_tokens": 200000,
						"max_tokens":       64000,
						"capabilities": map[string]any{
							"image_input": map[string]any{"supported": true},
							"pdf_input":   map[string]any{"supported": true},
							"thinking":    map[string]any{"supported": true},
							"effort": map[string]any{
								"supported": true,
								"low":       map[string]any{"supported": true},
								"medium":    map[string]any{"supported": true},
								"high":      map[string]any{"supported": true},
							},
						},
					},
				},
				"first_id": "claude-sonnet-4-6",
				"last_id":  "claude-sonnet-4-6",
				"has_more": true,
			})
			return
		}

		if afterID == "claude-sonnet-4-6" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{
						"id":               "claude-sonnet-6-preview",
						"type":             "model",
						"display_name":     "Claude Sonnet 6 Preview",
						"created_at":       "2026-11-01T00:00:00Z",
						"lifecycle":        "active",
						"line":             "sonnet",
						"max_input_tokens": 1000000,
						"max_tokens":       128000,
						"capabilities": map[string]any{
							"image_input": map[string]any{"supported": true},
							"pdf_input":   map[string]any{"supported": true},
							"thinking":    map[string]any{"supported": true},
							"effort": map[string]any{
								"supported": true,
								"low":       map[string]any{"supported": true},
								"medium":    map[string]any{"supported": true},
								"high":      map[string]any{"supported": true},
								"max":       map[string]any{"supported": true},
							},
						},
					},
					{
						"id":               "claude-2.1",
						"type":             "model",
						"display_name":     "Claude 2.1",
						"created_at":       "1970-01-01T00:00:00Z",
						"lifecycle":        "retired",
						"max_input_tokens": 200000,
						"max_tokens":       4096,
						"capabilities":     map[string]any{},
					},
				},
				"first_id": "claude-sonnet-6-preview",
				"last_id":  "claude-2.1",
				"has_more": false,
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
	if len(models) != 3 {
		t.Fatalf("expected 3 models across 2 pages, got %d", len(models))
	}

	// 1. Known model preserves static pricing and staticProfileOverrides (Output=128000)
	sonnet := models[0]
	if sonnet.Pricing.IsZero() {
		t.Error("expected static pricing preserved for claude-sonnet-4-6")
	}
	if sonnet.Limits.Output != 128000 {
		t.Errorf("expected staticProfileOverrides Output=128000 preserved, got %d", sonnet.Limits.Output)
	}

	// 2. Brand-new unknown model maps rich live capabilities, modalities, and limits
	preview := models[1]
	if preview.ID != "claude-sonnet-6-preview" || preview.Name != "Claude Sonnet 6 Preview" {
		t.Errorf("unexpected preview identity: %+v", preview)
	}
	if preview.Family != "claude-sonnet" {
		t.Errorf("expected family claude-sonnet, got %q", preview.Family)
	}
	if preview.ReleaseDate != "2026-11-01" {
		t.Errorf("expected ReleaseDate 2026-11-01, got %q", preview.ReleaseDate)
	}
	if preview.Limits.Context != 1000000 || preview.Limits.Output != 128000 {
		t.Errorf("unexpected limits: %+v", preview.Limits)
	}
	if !preview.Capabilities.Completion || !preview.Capabilities.Attachment || !preview.Capabilities.Reasoning || !preview.Capabilities.ToolCall {
		t.Errorf("unexpected capabilities: %+v", preview.Capabilities)
	}
	wantEfforts := []profile.ReasoningOption{{Type: "effort", Values: []string{"low", "medium", "high", "max"}}}
	if !reflect.DeepEqual(preview.Capabilities.ReasoningOptions, wantEfforts) {
		t.Errorf("ReasoningOptions = %+v, want %+v", preview.Capabilities.ReasoningOptions, wantEfforts)
	}
	if !slices.Contains(preview.Modalities.Inputs, profile.ModalityImage) || !slices.Contains(preview.Modalities.Inputs, profile.ModalityPDF) {
		t.Errorf("expected image and pdf input modalities, got %+v", preview.Modalities.Inputs)
	}

	// 3. Retired model with epoch CreatedAt marks Completion=false and omits bogus 1970 release date
	retired := models[2]
	if retired.Capabilities.Completion || retired.Capabilities.ToolCall {
		t.Errorf("expected retired model to have Completion=false and ToolCall=false, got %+v", retired.Capabilities)
	}
	if retired.ReleaseDate != "" {
		t.Errorf("expected epoch CreatedAt to be ignored, got ReleaseDate=%q", retired.ReleaseDate)
	}

	// ListProfiles must remain unchanged
	if afterCount := len(p.ListProfiles()); afterCount != beforeCount {
		t.Errorf("ListProfiles count mutated: before=%d, after=%d", beforeCount, afterCount)
	}
}

func TestGetModel(t *testing.T) {
	p := newTestAnthropicProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models/claude-fable-5" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":               "claude-fable-5",
				"type":             "model",
				"display_name":     "Claude Fable 5",
				"created_at":       "2026-06-07T00:00:00Z",
				"lifecycle":        "active",
				"line":             "fable",
				"max_input_tokens": 1000000,
				"max_tokens":       128000,
				"capabilities":     map[string]any{},
			})
			return
		}
		http.Error(w, `{"type":"error","error":{"type":"not_found_error","message":"model not found"}}`, http.StatusNotFound)
	})

	if _, err := p.GetModel(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty model ID")
	}

	prof, err := p.GetModel(context.Background(), "claude-fable-5")
	if err != nil {
		t.Fatalf("GetModel failed: %v", err)
	}
	// claude-fable-5 has Temperature=false and Pricing in staticProfiles
	if prof.Capabilities.Temperature {
		t.Error("expected claude-fable-5 static Temperature=false preserved")
	}
	if prof.Pricing.IsZero() {
		t.Error("expected claude-fable-5 static pricing preserved")
	}

	if _, err := p.GetModel(context.Background(), "missing-model"); err == nil {
		t.Fatal("expected error for missing model")
	}
}
