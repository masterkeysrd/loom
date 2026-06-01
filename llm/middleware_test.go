package llm_test

import (
	"context"
	"testing"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
)

type middlewareMockProvider struct {
	llm.Provider
	lastRequest *llm.Request
}

func (m *middlewareMockProvider) Stream(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
	m.lastRequest = req
	return func(yield func(message.AssistantChunk, error) bool) {}, nil
}

func (m *middlewareMockProvider) GetProfile(id string) (llm.ModelProfile, bool) {
	return llm.ModelProfile{}, false
}

func TestModel_Middleware(t *testing.T) {
	provider := &middlewareMockProvider{}
	model, _ := llm.NewModel(provider, "test-model", nil)

	var executionOrder []string

	mw1 := func(next llm.Streamer) llm.Streamer {
		return func(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
			executionOrder = append(executionOrder, "mw1_start")
			res, err := next(ctx, req)
			executionOrder = append(executionOrder, "mw1_end")
			return res, err
		}
	}

	mw2 := func(next llm.Streamer) llm.Streamer {
		return func(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
			executionOrder = append(executionOrder, "mw2_start")
			res, err := next(ctx, req)
			executionOrder = append(executionOrder, "mw2_end")
			return res, err
		}
	}

	model = model.WithMiddleware(mw1, mw2)

	_, _ = model.Stream(context.Background(), nil)

	expectedOrder := []string{"mw1_start", "mw2_start", "mw2_end", "mw1_end"}
	if len(executionOrder) != len(expectedOrder) {
		t.Fatalf("expected %d execution steps, got %d", len(expectedOrder), len(executionOrder))
	}

	for i, v := range expectedOrder {
		if executionOrder[i] != v {
			t.Errorf("at index %d: expected %s, got %s", i, v, executionOrder[i])
		}
	}
}

type mockExtension struct {
	val string
}

func (m mockExtension) ExtensionID() string {
	return "mock.extension"
}

func TestModel_Extensions(t *testing.T) {
	provider := &middlewareMockProvider{}
	model, _ := llm.NewModel(provider, "test-model", nil)

	ext := mockExtension{val: "val1"}
	model = model.WithExtension(ext)

	_, _ = model.Stream(context.Background(), nil)

	req := provider.lastRequest
	if req.Extensions[ext.ExtensionID()].(mockExtension).val != "val1" {
		t.Errorf("expected extensions to be propagated, got %v", req.Extensions)
	}

	// Test per-call override
	override := mockExtension{val: "override"}
	_, _ = model.Stream(context.Background(), nil, llm.WithExtensionOption(override))
	if req := provider.lastRequest; req.Extensions[ext.ExtensionID()].(mockExtension).val != "override" {
		t.Errorf("expected per-call extension override, got %v", req.Extensions[ext.ExtensionID()])
	}
}

func TestModel_MiddlewareMutation(t *testing.T) {
	provider := &middlewareMockProvider{}
	model, _ := llm.NewModel(provider, "test-model", nil)

	mw := func(next llm.Streamer) llm.Streamer {
		return func(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
			if req.Extensions == nil {
				req.Extensions = make(map[string]llm.Extension)
			}
			req.Extensions["injected"] = mockExtension{val: "true"}
			return next(ctx, req)
		}
	}

	model = model.WithMiddleware(mw)

	_, _ = model.Stream(context.Background(), nil)

	if provider.lastRequest.Extensions["injected"].(mockExtension).val != "true" {
		t.Error("middleware failed to inject extension")
	}
}
