package llm_test

import (
	"context"
	"testing"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
)

type modelMockProvider struct {
	llm.Provider
	lastRequest *llm.Request
}

func (m *modelMockProvider) Stream(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
	m.lastRequest = req
	return func(yield func(message.AssistantChunk, error) bool) {}, nil
}

func (m *modelMockProvider) GetConfig(name string) (llm.ModelConfig, bool) {
	return llm.ModelConfig{}, false
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
