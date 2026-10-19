package loomopenai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestEmbeddingInterfaceCompliance(t *testing.T) {
	p := &Provider{}
	var _ embedding.Provider = p
	var _ profile.Provider = p
}

func TestToEmbeddingNewParams(t *testing.T) {
	dims := 512
	req := &embedding.Request{
		Model: "text-embedding-3-small",
		Inputs: []embedding.Input{
			embedding.Text("first text"),
			embedding.Multimodal(&message.TextBlock{Text: "second text"}),
		},
		Dimensions: &dims,
		Options: map[string]any{
			"user": "user-123",
		},
	}

	params, err := toEmbeddingNewParams(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(params.Model) != "text-embedding-3-small" {
		t.Errorf("expected model 'text-embedding-3-small', got %q", params.Model)
	}
	if param.IsOmitted(params.Dimensions) || params.Dimensions.Value != 512 {
		t.Errorf("expected dimensions 512, got %v", params.Dimensions)
	}
	if param.IsOmitted(params.User) || params.User.Value != "user-123" {
		t.Errorf("expected user 'user-123', got %v", params.User)
	}

	expectedInputs := []string{"first text", "second text"}
	if !reflect.DeepEqual(params.Input.OfArrayOfStrings, expectedInputs) {
		t.Errorf("expected inputs %v, got %v", expectedInputs, params.Input.OfArrayOfStrings)
	}
}

func TestToEmbeddingNewParams_Errors(t *testing.T) {
	if _, err := toEmbeddingNewParams(nil); err == nil {
		t.Error("expected error for nil request")
	}
	if _, err := toEmbeddingNewParams(&embedding.Request{Model: "m"}); err == nil {
		t.Error("expected error for empty inputs")
	}
	if _, err := toEmbeddingNewParams(&embedding.Request{
		Model:  "m",
		Inputs: []embedding.Input{embedding.Text("")},
	}); err == nil {
		t.Error("expected error for empty text input")
	}
	if _, err := toEmbeddingNewParams(&embedding.Request{
		Model:  "m",
		Inputs: []embedding.Input{embedding.Image([]byte("img"), "image/png")},
	}); err == nil {
		t.Error("expected error for unsupported image block")
	}
}

func TestProvider_Embed_RoundTrip(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	otel.SetTracerProvider(tp)
	tracer := tp.Tracer("test")

	// Return embeddings out-of-order (index 1 first, then index 0) to verify ordering by Index
	mockResp := map[string]any{
		"object": "list",
		"model":  "text-embedding-3-small",
		"data": []map[string]any{
			{
				"object":    "embedding",
				"index":     1,
				"embedding": []float64{0.4, 0.5, 0.6},
			},
			{
				"object":    "embedding",
				"index":     0,
				"embedding": []float64{0.1, 0.2, 0.3},
			},
		},
		"usage": map[string]any{
			"prompt_tokens": 8,
			"total_tokens":  8,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/embeddings") {
			t.Errorf("expected path ending with /embeddings, got %s", r.URL.Path)
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read body: %v", err)
		}
		var parsed map[string]any
		if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
			t.Fatalf("failed to parse json: %v", err)
		}
		if parsed["model"] != "text-embedding-3-small" {
			t.Errorf("expected model 'text-embedding-3-small', got %v", parsed["model"])
		}
		if parsed["dimensions"] != float64(3) {
			t.Errorf("expected dimensions 3, got %v", parsed["dimensions"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mockResp)
	}))
	defer server.Close()

	client := openai.NewClient(
		option.WithBaseURL(server.URL),
		option.WithAPIKey("test-key"),
	)
	p := NewProvider(&client)

	ctx, span := tracer.Start(context.Background(), "embed_test")
	dims := 3
	req := &embedding.Request{
		Model:      "text-embedding-3-small",
		Inputs:     embedding.Texts("first", "second"),
		Dimensions: &dims,
	}

	resp, err := p.Embed(ctx, req)
	span.End()

	if err != nil {
		t.Fatalf("unexpected error from Embed: %v", err)
	}

	if resp.Model != "text-embedding-3-small" {
		t.Errorf("expected model 'text-embedding-3-small', got %q", resp.Model)
	}
	if len(resp.Embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(resp.Embeddings))
	}

	// Verify reordered by Index
	if !reflect.DeepEqual(resp.Embeddings[0], embedding.Vector{0.1, 0.2, 0.3}) {
		t.Errorf("expected embeddings[0] = [0.1, 0.2, 0.3], got %v", resp.Embeddings[0])
	}
	if !reflect.DeepEqual(resp.Embeddings[1], embedding.Vector{0.4, 0.5, 0.6}) {
		t.Errorf("expected embeddings[1] = [0.4, 0.5, 0.6], got %v", resp.Embeddings[1])
	}

	if resp.Metrics == nil || resp.Metrics.TotalTokens != 8 || resp.Metrics.Tokens.Input != 8 {
		t.Errorf("unexpected metrics: %+v", resp.Metrics)
	}

	// Check span attribute
	spans := spanRecorder.Ended()
	foundAttr := false
	for _, s := range spans {
		for _, attr := range s.Attributes() {
			if string(attr.Key) == "openai.api.type" && attr.Value.AsString() == "embeddings" {
				foundAttr = true
				break
			}
		}
	}
	if !foundAttr {
		t.Error("expected span attribute openai.api.type=embeddings")
	}
}

func TestProvider_Embed_Errors(t *testing.T) {
	var p *Provider
	if _, err := p.Embed(context.Background(), &embedding.Request{}); err == nil {
		t.Error("expected error for nil provider")
	}

	p2 := &Provider{}
	if _, err := p2.Embed(context.Background(), &embedding.Request{}); err == nil {
		t.Error("expected error for nil client")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": {"message": "server error"}}`))
	}))
	defer server.Close()

	client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	p3 := NewProvider(&client)

	if _, err := p3.Embed(context.Background(), nil); err == nil {
		t.Error("expected error for nil request")
	}

	if _, err := p3.Embed(context.Background(), &embedding.Request{
		Model:  "text-embedding-3-small",
		Inputs: embedding.Texts("test"),
	}); err == nil {
		t.Error("expected error on server 500")
	}
}
