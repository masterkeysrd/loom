package llm_test

import (
	"context"
	"testing"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/stream"
)

type modelMockProvider struct {
	llm.Provider
	lastRequest *llm.Request
	chunks      []message.AssistantChunk
}

func (m *modelMockProvider) Name() string {
	return "mock"
}

func (m *modelMockProvider) Stream(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
	m.lastRequest = req
	return func(yield func(message.AssistantChunk, error) bool) {
		for _, c := range m.chunks {
			if c.Metrics != nil {
				// Copy so Model.Stream's cost attachment doesn't leak across calls.
				metrics := *c.Metrics
				c.Metrics = &metrics
			}
			if !yield(c, nil) {
				return
			}
		}
	}, nil
}

func (m *modelMockProvider) GetProfile(id string) (profile.Profile, bool) {
	if id == "test-model" {
		return profile.Profile{
			Limits: profile.Limits{
				Output:  4096,
				Context: 8192,
			},
		}, true
	}
	return profile.Profile{}, false
}

func TestNewModel_DefaultsMaxTokensFromProfile(t *testing.T) {
	provider := &modelMockProvider{}
	model, err := llm.NewModel(provider, "test-model", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _ = model.Stream(context.Background(), nil)
	if provider.lastRequest == nil {
		t.Fatal("expected request to be captured")
	}

	if provider.lastRequest.MaxTokens != 4096 {
		t.Errorf("expected MaxTokens to default to 4096, got %d", provider.lastRequest.MaxTokens)
	}

	if provider.lastRequest.ContextWindow != 8192 {
		t.Errorf("expected ContextWindow to default to 8192, got %d", provider.lastRequest.ContextWindow)
	}
}

func TestModel_FluentConfig(t *testing.T) {
	provider := &modelMockProvider{}
	model, _ := llm.NewModel(provider, "test-model", nil)

	temp := float32(0.7)
	topP := float32(0.9)
	topK := 50
	stop := []string{"STOP"}

	configuredModel := model.
		WithTemperature(temp).
		WithTopP(topP).
		WithTopK(topK).
		WithStop(stop...).
		WithMaxTokens(100).
		WithJSON()

	// Verify original model is unchanged
	if model.WithTemperature(0.5) == model {
		t.Error("expected clone, got same model instance")
	}

	_, _ = configuredModel.Stream(context.Background(), nil)

	req := provider.lastRequest
	if req == nil {
		t.Fatal("expected request to be captured")
	}

	if req.Temperature == nil || *req.Temperature != temp {
		t.Errorf("expected Temperature %v, got %v", temp, req.Temperature)
	}
	if req.TopP == nil || *req.TopP != topP {
		t.Errorf("expected TopP %v, got %v", topP, req.TopP)
	}
	if req.TopK == nil || *req.TopK != topK {
		t.Errorf("expected TopK %v, got %v", topK, req.TopK)
	}
	if len(req.Stop) != 1 || req.Stop[0] != "STOP" {
		t.Errorf("expected Stop %v, got %v", stop, req.Stop)
	}
	if req.MaxTokens != 100 {
		t.Errorf("expected MaxTokens 100, got %d", req.MaxTokens)
	}
	if req.ResponseFormat != "json_object" {
		t.Errorf("expected ResponseFormat json_object, got %q", req.ResponseFormat)
	}
}

func TestModel_ThinkingConfig(t *testing.T) {
	provider := &modelMockProvider{}
	model, _ := llm.NewModel(provider, "test-model", nil)

	configuredModel := model.
		WithThinking(2048).
		WithThinkingEffort("high").
		WithAdaptiveThinking()

	_, _ = configuredModel.Stream(context.Background(), nil)

	req := provider.lastRequest
	if req == nil {
		t.Fatal("expected request to be captured")
	}

	if req.Thinking == nil {
		t.Fatal("expected Thinking config to be set")
	}
	if req.Thinking.Budget != 2048 {
		t.Errorf("expected Budget 2048, got %d", req.Thinking.Budget)
	}
	if req.Thinking.Effort != "high" {
		t.Errorf("expected Effort high, got %q", req.Thinking.Effort)
	}
	if !req.Thinking.Adaptive {
		t.Error("expected Adaptive to be true")
	}
}

func TestModel_CloneDeepCopiesConfig(t *testing.T) {
	provider := &modelMockProvider{}
	model, _ := llm.NewModel(provider, "test-model", &llm.ModelConfig{MaxTokens: 10})

	m1 := model.WithMaxTokens(20)
	m2 := model.WithMaxTokens(30)

	if m1.WithMaxTokens(20) == m1 {
		t.Error("expected new clone on every configuration call")
	}

	_, _ = m1.Stream(context.Background(), nil)
	if provider.lastRequest.MaxTokens != 20 {
		t.Errorf("m1 should have 20 tokens, got %d", provider.lastRequest.MaxTokens)
	}

	_, _ = m2.Stream(context.Background(), nil)
	if provider.lastRequest.MaxTokens != 30 {
		t.Errorf("m2 should have 30 tokens, got %d", provider.lastRequest.MaxTokens)
	}
}

type mockWriter struct{}

func (w *mockWriter) Write(ctx context.Context, data any) error {
	return nil
}

func TestModel_NilProfileStreamDoesNotPanic(t *testing.T) {
	provider := &modelMockProvider{}
	model, err := llm.NewModel(provider, "unknown-model", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	w := &mockWriter{}
	ctx := stream.WithWriter(context.Background(), w)

	// This should not panic
	_, err = model.Stream(ctx, nil)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
}

func TestModel_WithContextWindow(t *testing.T) {
	provider := &modelMockProvider{}
	model, _ := llm.NewModel(provider, "test-model", nil)

	m1 := model.WithContextWindow(16384)

	_, _ = m1.Stream(context.Background(), nil)
	if provider.lastRequest.ContextWindow != 16384 {
		t.Errorf("expected ContextWindow to be 16384, got %d", provider.lastRequest.ContextWindow)
	}
}

func TestModel_WithProfile(t *testing.T) {
	provider := &modelMockProvider{chunks: []message.AssistantChunk{{Metrics: &message.TokenMetrics{
		Tokens: message.TokenDetails{Input: 310, CacheRead: 300, Output: 5},
	}}}}
	model, err := llm.NewModel(provider, "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	p := &profile.Profile{Pricing: profile.Pricing{
		Input: 1, Output: 2, CacheRead: 0.5,
		TieredLimits: []profile.TierPricing{{
			Label:     "Context over 300",
			Condition: profile.TierCondition{Type: profile.TierConditionContext, Size: 300},
			Input:     2, Output: 4, CacheRead: 1,
		}},
	}}

	withP := model.WithProfile(p)
	if model.Profile() != nil {
		t.Fatal("original model profile must be unchanged")
	}
	if withP.Profile() != p {
		t.Fatal("clone must use injected profile")
	}

	var rtProfile *profile.Profile
	withP = withP.WithMiddleware(func(next llm.Streamer) llm.Streamer {
		return func(ctx context.Context, r *llm.Request) (llm.StreamResponse, error) {
			rt, _ := llm.RuntimeFromContext(ctx)
			rtProfile = rt.Profile
			return next(ctx, r)
		}
	})

	resp, err := withP.Stream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got *message.TokenMetrics
	for chunk, err := range resp {
		if err != nil {
			t.Fatal(err)
		}
		got = chunk.Metrics
	}
	if rtProfile != p {
		t.Fatal("Runtime.Profile must equal injected profile")
	}
	if got.CostTier != "Context over 300" || !got.Priced {
		t.Fatalf("tier/priced not set: %+v", got)
	}
	if want := message.Cost(10*2000 + 300*1000 + 5*4000); got.TotalCost != want {
		t.Fatalf("total = %v, want %v", got.TotalCost, want)
	}

	// WithProfile(nil) skips estimation.
	resp, _ = withP.WithProfile(nil).Stream(context.Background(), nil)
	for chunk := range resp {
		if chunk.Metrics.Priced || chunk.Metrics.TotalCost != 0 {
			t.Fatalf("nil profile must skip estimation: %+v", chunk.Metrics)
		}
	}
}
