package loomollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/message"
	"github.com/ollama/ollama/api"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestToEmbedRequestConversion(t *testing.T) {
	dims := 256
	req := &embedding.Request{
		Model: "nomic-embed-text",
		Inputs: []embedding.Input{
			embedding.Text("first document"),
			embedding.Multimodal(&message.TextBlock{Text: "second document"}),
		},
		Dimensions: &dims,
		Options: map[string]any{
			"runner":   "mlx",
			"truncate": true,
		},
	}

	embedReq, err := toEmbedRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if embedReq.Model != "nomic-embed-text" {
		t.Errorf("expected model 'nomic-embed-text', got %q", embedReq.Model)
	}
	if embedReq.Dimensions != 256 {
		t.Errorf("expected dimensions 256, got %d", embedReq.Dimensions)
	}
	if embedReq.Runner != "mlx" {
		t.Errorf("expected runner 'mlx', got %q", embedReq.Runner)
	}
	if embedReq.Truncate == nil || !*embedReq.Truncate {
		t.Errorf("expected truncate=true, got %v", embedReq.Truncate)
	}

	expectedInputs := []string{"first document", "second document"}
	if !reflect.DeepEqual(embedReq.Input, expectedInputs) {
		t.Errorf("expected inputs %v, got %v", expectedInputs, embedReq.Input)
	}
}

func TestToEmbedRequestErrors(t *testing.T) {
	t.Run("empty inputs", func(t *testing.T) {
		_, err := toEmbedRequest(&embedding.Request{Model: "m"})
		if err == nil {
			t.Fatal("expected error for empty inputs")
		}
	})

	t.Run("empty text input", func(t *testing.T) {
		_, err := toEmbedRequest(&embedding.Request{
			Model:  "m",
			Inputs: []embedding.Input{embedding.Text("")},
		})
		if err == nil {
			t.Fatal("expected error for empty text input")
		}
	})

	t.Run("unsupported multimodal image input", func(t *testing.T) {
		_, err := toEmbedRequest(&embedding.Request{
			Model:  "m",
			Inputs: []embedding.Input{embedding.Image([]byte("png"), "image/png")},
		})
		if err == nil {
			t.Fatal("expected error for unsupported multimodal image input")
		}
	})
}

func TestProviderEmbedRoundTrip(t *testing.T) {
	var receivedReq api.EmbedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/embed" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedReq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		resp := api.EmbedResponse{
			Model: "nomic-embed-text",
			Embeddings: [][]float32{
				{0.1, 0.2, 0.3},
				{0.4, 0.5, 0.6},
			},
			PromptEvalCount: 12,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("failed to parse server url: %v", err)
	}

	client := api.NewClient(u, server.Client())
	provider := (&Provider{}).NewProvider(client, u)

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	ctx, span := tp.Tracer("test").Start(context.Background(), "embed-test")
	dims := 3
	req := &embedding.Request{
		Model:      "nomic-embed-text",
		Inputs:     embedding.Texts("hello", "world"),
		Dimensions: &dims,
	}

	resp, err := provider.Embed(ctx, req)
	span.End()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedReq.Model != "nomic-embed-text" || receivedReq.Dimensions != 3 {
		t.Errorf("unexpected server request: %+v", receivedReq)
	}

	if resp.Model != "nomic-embed-text" {
		t.Errorf("expected model 'nomic-embed-text', got %q", resp.Model)
	}
	if len(resp.Embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(resp.Embeddings))
	}
	if !reflect.DeepEqual(resp.Embeddings[0], embedding.Vector{0.1, 0.2, 0.3}) {
		t.Errorf("embedding[0] mismatch: %v", resp.Embeddings[0])
	}
	if !reflect.DeepEqual(resp.Embeddings[1], embedding.Vector{0.4, 0.5, 0.6}) {
		t.Errorf("embedding[1] mismatch: %v", resp.Embeddings[1])
	}

	if resp.Metrics == nil || resp.Metrics.TotalTokens != 12 || resp.Metrics.Tokens.Input != 12 {
		t.Errorf("metrics mismatch: %+v", resp.Metrics)
	}

	// Verify OTel span attribute
	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least 1 span")
	}
	var foundType bool
	for _, attr := range spans[0].Attributes {
		if attr.Key == "ollama.api.type" && attr.Value.AsString() == "embed" {
			foundType = true
		}
	}
	if !foundType {
		t.Errorf("expected ollama.api.type='embed', got %v", spans[0].Attributes)
	}
}

func TestProviderEmbedErrors(t *testing.T) {
	var nilProvider *Provider
	if _, err := nilProvider.Embed(context.Background(), &embedding.Request{}); err == nil {
		t.Error("expected error on nil provider")
	}

	emptyProvider := &Provider{}
	if _, err := emptyProvider.Embed(context.Background(), &embedding.Request{}); err == nil {
		t.Error("expected error on uninitialized client")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	p := (&Provider{}).NewProvider(api.NewClient(u, server.Client()), u)

	if _, err := p.Embed(context.Background(), nil); err == nil {
		t.Error("expected error on nil request")
	}

	if _, err := p.Embed(context.Background(), &embedding.Request{
		Model:  "nomic-embed-text",
		Inputs: embedding.Texts("fail"),
	}); err == nil {
		t.Error("expected error on server 500")
	}
}
