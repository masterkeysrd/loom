package loomopenai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/masterkeysrd/loom/profile"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestOpenAIProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			handler(rec, req)
			return rec.Result(), nil
		}),
	}
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL("https://api.openai.test"),
		option.WithHTTPClient(httpClient),
		option.WithMaxRetries(0),
	)
	return NewProvider(&client)
}

func TestListModels_EnrichesStaticAndPreservesCatalog(t *testing.T) {
	p := newTestOpenAIProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":       "gpt-5.4-pro",
					"object":   "model",
					"created":  1740787200,
					"owned_by": "openai",
				},
				{
					"id":       "ft:gpt-4o-mini:my-org:custom:1",
					"object":   "model",
					"created":  1735689600, // 2025-01-01
					"owned_by": "my-org",
				},
				{
					"id":       "custom-embed-v1",
					"object":   "model",
					"created":  1735689600,
					"owned_by": "local",
				},
				{
					"id":       "whisper-1",
					"object":   "model",
					"created":  1677531384,
					"owned_by": "openai-internal",
				},
			},
		})
	})

	// Override one model to ensure OverrideProfile takes precedence
	p.OverrideProfile("custom-embed-v1", profile.Profile{
		ID:   "custom-embed-v1",
		Name: "Custom Embedding Override",
		Capabilities: profile.Capabilities{
			Embedding: true,
		},
		Limits: profile.Limits{Dimensions: 1024},
	})
	beforeCount := len(p.ListProfiles())

	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) != 4 {
		t.Fatalf("expected 4 models, got %d", len(models))
	}

	// 1. Static model keeps pricing, limits, and capabilities from staticProfiles
	staticWant, ok := staticProfiles["gpt-5.4-pro"]
	if !ok {
		t.Fatal("expected gpt-5.4-pro in staticProfiles")
	}
	gotPro := models[0]
	if gotPro.Pricing.Input != staticWant.Pricing.Input || gotPro.Pricing.Output != staticWant.Pricing.Output {
		t.Errorf("expected static pricing preserved (%+v), got %+v", staticWant.Pricing, gotPro.Pricing)
	}
	if gotPro.Limits != staticWant.Limits {
		t.Errorf("expected static limits preserved (%+v), got %+v", staticWant.Limits, gotPro.Limits)
	}
	if gotPro.Capabilities.Temperature != staticWant.Capabilities.Temperature {
		t.Errorf("expected Temperature=%v preserved, got %v", staticWant.Capabilities.Temperature, gotPro.Capabilities.Temperature)
	}

	// 2. Unknown fine-tune model gets synthesized chat profile
	gotFT := models[1]
	if gotFT.ID != "ft:gpt-4o-mini:my-org:custom:1" {
		t.Errorf("unexpected fine-tune ID: %q", gotFT.ID)
	}
	if gotFT.ReleaseDate != "2025-01-01" {
		t.Errorf("expected ReleaseDate 2025-01-01, got %q", gotFT.ReleaseDate)
	}
	if !gotFT.Capabilities.Completion || !gotFT.Capabilities.ToolCall {
		t.Errorf("expected fine-tune model to support completion and tools, got %+v", gotFT.Capabilities)
	}
	if gotFT.Family != "gpt" {
		t.Errorf("expected family 'gpt', got %q", gotFT.Family)
	}

	// 3. Explicit override wins
	gotOverride := models[2]
	if gotOverride.Name != "Custom Embedding Override" || gotOverride.Limits.Dimensions != 1024 {
		t.Errorf("expected override profile, got %+v", gotOverride)
	}

	// 4. Non-chat media model does not claim chat completion
	gotWhisper := models[3]
	if gotWhisper.Capabilities.Completion || gotWhisper.Capabilities.ToolCall {
		t.Errorf("expected whisper-1 not to claim chat completion, got %+v", gotWhisper.Capabilities)
	}

	// ListProfiles must NOT be mutated with unknown models from ListModels
	if afterCount := len(p.ListProfiles()); afterCount != beforeCount {
		t.Errorf("ListModels mutated ListProfiles count: before=%d, after=%d", beforeCount, afterCount)
	}
}

func TestGetModel(t *testing.T) {
	p := newTestOpenAIProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models/gpt-6-luna":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":       "gpt-6-luna",
				"object":   "model",
				"created":  1750000000,
				"owned_by": "openai",
			})
		case "/models/vllm-qwen3":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":       "vllm-qwen3",
				"object":   "model",
				"created":  1735689600,
				"owned_by": "vllm",
			})
		default:
			http.Error(w, `{"error":{"message":"Model not found"}}`, http.StatusNotFound)
		}
	})

	// Empty ID validation
	if _, err := p.GetModel(context.Background(), "   "); err == nil {
		t.Fatal("expected error for empty model ID")
	}

	// Static + staticProfileOverrides (gpt-6-luna has Decision=true in configs.go)
	luna, err := p.GetModel(context.Background(), "gpt-6-luna")
	if err != nil {
		t.Fatalf("GetModel(gpt-6-luna) failed: %v", err)
	}
	if !luna.Capabilities.Decision {
		t.Error("expected staticProfileOverrides (Decision=true) to be preserved for gpt-6-luna")
	}
	if luna.Pricing.IsZero() {
		t.Error("expected static pricing to be preserved for gpt-6-luna")
	}

	// Unknown model served by OpenAI-compatible server
	vllm, err := p.GetModel(context.Background(), "vllm-qwen3")
	if err != nil {
		t.Fatalf("GetModel(vllm-qwen3) failed: %v", err)
	}
	if vllm.ID != "vllm-qwen3" || !vllm.Capabilities.Completion {
		t.Errorf("unexpected vllm profile: %+v", vllm)
	}

	// 404 error propagation
	if _, err := p.GetModel(context.Background(), "does-not-exist"); err == nil {
		t.Fatal("expected error for missing model")
	}
}
