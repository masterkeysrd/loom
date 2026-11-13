package loomantigravity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/masterkeysrd/loom/auth"
	"github.com/masterkeysrd/loom/profile"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestModelLister_ListAndGetModel(t *testing.T) {
	var fetchCalls int
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			switch req.URL.Path {
			case "/v1internal:loadCodeAssist":
				rec.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(rec).Encode(map[string]any{
					"cloudaicompanionProject": "test-companion-proj",
				})
			case "/v1internal:fetchAvailableModels":
				fetchCalls++
				rec.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(rec).Encode(map[string]any{
					"models": map[string]any{
						"gemini-3.8-flash-tiered": map[string]any{
							"displayName":      "Gemini 3.8 Flash",
							"maxTokens":        1048576,
							"maxOutputTokens":  65536,
							"supportsImages":   true,
							"supportsThinking": true,
						},
						"claude-sonnet-4-6": map[string]any{
							"displayName":      "Claude Sonnet 4.6",
							"maxTokens":        200000,
							"maxOutputTokens":  64000,
							"supportsImages":   true,
							"supportsThinking": true,
						},
					},
				})
			default:
				http.NotFound(rec, req)
			}
			return rec.Result(), nil
		}),
	}

	p, err := NewProvider(&Config{
		TokenSource: auth.StaticTokenSource("test-token"),
		BaseURL:     "https://antigravity.test",
		HTTPClient:  httpClient,
	})
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}

	p.OverrideProfile("custom-local", profile.Profile{
		ID:   "custom-local",
		Name: "Custom Override",
	})

	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if fetchCalls != 1 {
		t.Fatalf("expected 1 live fetch, got %d", fetchCalls)
	}
	// Sorted deterministically: claude-sonnet-4-6, custom-local, gemini-3.8-flash
	if len(models) != 3 {
		t.Fatalf("expected 3 models (2 compacted + 1 override), got %d: %+v", len(models), models)
	}
	if models[0].ID != "claude-sonnet-4-6" || models[1].ID != "custom-local" || models[2].ID != "gemini-3.8-flash" {
		t.Errorf("unexpected sorted model IDs: %q, %q, %q", models[0].ID, models[1].ID, models[2].ID)
	}

	// GetModel resolves canonical family, effort suffix, and raw endpoint
	for _, id := range []string{"gemini-3.8-flash", "gemini-3.8-flash-high", "gemini-3.8-flash-tiered"} {
		prof, err := p.GetModel(context.Background(), id)
		if err != nil {
			t.Fatalf("GetModel(%q) failed: %v", id, err)
		}
		if prof.ID != id {
			t.Errorf("GetModel(%q): expected ID %q, got %q", id, id, prof.ID)
		}
		if prof.Limits.Context != 1048576 {
			t.Errorf("GetModel(%q): expected Context=1048576, got %d", id, prof.Limits.Context)
		}
	}

	if _, err := p.GetModel(context.Background(), " "); err == nil {
		t.Fatal("expected error for empty model ID")
	}
	if _, err := p.GetModel(context.Background(), "non-existent-model"); err == nil {
		t.Fatal("expected error for unknown model")
	}
}
