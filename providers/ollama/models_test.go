package loomollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/masterkeysrd/loom/profile"
	"github.com/ollama/ollama/api"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestOllamaProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	u, err := url.Parse("http://ollama.test:11434")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			handler(rec, req)
			return rec.Result(), nil
		}),
	}
	return (&Provider{}).NewProvider(api.NewClient(u, httpClient), u)
}

func TestListModelsAndGetModel(t *testing.T) {
	p := newTestOllamaProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/tags":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{
					{
						"name":         "qwen3:32b",
						"model":        "qwen3:32b",
						"modified_at":  "2026-05-10T12:00:00Z",
						"details":      map[string]any{"family": "qwen3"},
						"capabilities": []string{"completion", "tools", "thinking"},
					},
					{
						"name":         "nomic-embed-text:latest",
						"model":        "nomic-embed-text:latest",
						"modified_at":  "2026-04-01T00:00:00Z",
						"details":      map[string]any{"family": "nomic-bert"},
						"capabilities": []string{"embedding"},
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/show":
			var req api.ShowRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			switch req.Model {
			case "qwen3:32b":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"details":      map[string]any{"family": "qwen3"},
					"capabilities": []string{"completion", "tools", "thinking", "vision"},
					"model_info": map[string]any{
						"qwen3.context_length": float64(131072),
					},
				})
			default:
				http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
			}
		default:
			http.NotFound(w, r)
		}
	})

	// Explicit override must never be clobbered by ListModels or GetProfile
	p.OverrideProfile("nomic-embed-text:latest", profile.Profile{
		ID:   "nomic-embed-text:latest",
		Name: "Custom Nomic Override",
		Capabilities: profile.Capabilities{
			Embedding: true,
		},
		Limits: profile.Limits{Dimensions: 768},
	})

	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}

	qwenTag := models[0]
	if qwenTag.ID != "qwen3:32b" || qwenTag.Family != "qwen3" || !qwenTag.OpenWeights {
		t.Errorf("unexpected qwen tag profile: %+v", qwenTag)
	}
	if !qwenTag.Capabilities.Completion || !qwenTag.Capabilities.ToolCall || !qwenTag.Capabilities.Reasoning {
		t.Errorf("expected capabilities mapped from /api/tags, got %+v", qwenTag.Capabilities)
	}

	overrideEntry := models[1]
	if overrideEntry.Name != "Custom Nomic Override" || overrideEntry.Limits.Dimensions != 768 {
		t.Errorf("expected OverrideProfile preserved, got %+v", overrideEntry)
	}

	// GetModel calls /api/show and populates context limits & vision modality
	if _, err := p.GetModel(context.Background(), " "); err == nil {
		t.Fatal("expected error for empty model ID")
	}

	qwenFull, err := p.GetModel(context.Background(), "qwen3:32b")
	if err != nil {
		t.Fatalf("GetModel(qwen3:32b) failed: %v", err)
	}
	if qwenFull.Limits.Context != 131072 {
		t.Errorf("expected context limit 131072 from /api/show, got %d", qwenFull.Limits.Context)
	}
	if !slices.Contains(qwenFull.Modalities.Inputs, profile.ModalityImage) {
		t.Errorf("expected vision modality from /api/show, got %+v", qwenFull.Modalities.Inputs)
	}

	// Subsequent ListModels preserves the upgraded context limit from discovered cache
	modelsAfter, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("second ListModels failed: %v", err)
	}
	if modelsAfter[0].Limits.Context != 131072 {
		t.Errorf("expected ListModels to preserve cached Context=131072, got %d", modelsAfter[0].Limits.Context)
	}

	// Missing model returns error
	if _, err := p.GetModel(context.Background(), "unknown:latest"); err == nil {
		t.Fatal("expected error for unknown model")
	}
}
