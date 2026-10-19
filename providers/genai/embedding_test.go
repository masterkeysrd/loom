package loomgenai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/message"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/genai"
)

func TestEmbeddingInterfaceCompliance(t *testing.T) {
	p := &Provider{}
	var _ embedding.Provider = p
	var _ embedding.ProfileProvider = p
}

func TestToEmbedContentArgs(t *testing.T) {
	dims := 768
	req := &embedding.Request{
		Model: "gemini-embedding-2",
		Inputs: []embedding.Input{
			embedding.Text("hello world"),
			embedding.Image([]byte("png-data"), "image/png"),
			embedding.Text("caption").WithParts(
				&message.AudioBlock{Data: []byte("audio-data"), MIMEType: "audio/mp3"},
				&message.DocumentBlock{URL: "gs://bucket/doc.pdf", MIMEType: "application/pdf"},
			),
		},
		Dimensions: &dims,
		TaskType:   embedding.TaskRetrievalDocument,
		Options: map[string]any{
			"title":         "My Document",
			"mime_type":     "text/plain",
			"auto_truncate": true,
		},
	}

	contents, config, err := toEmbedContentArgs(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(contents))
	}

	// Content 0: text
	if len(contents[0].Parts) != 1 || contents[0].Parts[0].Text != "hello world" {
		t.Errorf("unexpected content[0]: %+v", contents[0])
	}

	// Content 1: inline image
	if len(contents[1].Parts) != 1 || contents[1].Parts[0].InlineData == nil || contents[1].Parts[0].InlineData.MIMEType != "image/png" {
		t.Errorf("unexpected content[1]: %+v", contents[1])
	}

	// Content 2: text + audio + document URI
	if len(contents[2].Parts) != 3 {
		t.Fatalf("expected 3 parts in content[2], got %d", len(contents[2].Parts))
	}
	if contents[2].Parts[0].Text != "caption" {
		t.Errorf("expected part 0 text 'caption', got %q", contents[2].Parts[0].Text)
	}
	if contents[2].Parts[1].InlineData == nil || contents[2].Parts[1].InlineData.MIMEType != "audio/mp3" {
		t.Errorf("expected part 1 inline audio, got %+v", contents[2].Parts[1])
	}
	if contents[2].Parts[2].FileData == nil || contents[2].Parts[2].FileData.FileURI != "gs://bucket/doc.pdf" {
		t.Errorf("expected part 2 file URI, got %+v", contents[2].Parts[2])
	}

	// Config assertions
	if config.TaskType != "RETRIEVAL_DOCUMENT" {
		t.Errorf("expected TaskType 'RETRIEVAL_DOCUMENT', got %q", config.TaskType)
	}
	if config.OutputDimensionality == nil || *config.OutputDimensionality != 768 {
		t.Errorf("expected OutputDimensionality 768, got %v", config.OutputDimensionality)
	}
	if config.Title != "My Document" || config.MIMEType != "text/plain" || !config.AutoTruncate {
		t.Errorf("unexpected config options: %+v", config)
	}
}

func TestToGenaiTaskType(t *testing.T) {
	cases := map[embedding.TaskType]string{
		embedding.TaskRetrievalQuery:            "RETRIEVAL_QUERY",
		embedding.TaskRetrievalDocument:         "RETRIEVAL_DOCUMENT",
		embedding.TaskSemanticSimilarity:        "SEMANTIC_SIMILARITY",
		embedding.TaskClassification:            "CLASSIFICATION",
		embedding.TaskClustering:                "CLUSTERING",
		embedding.TaskType("fact_verification"): "FACT_VERIFICATION",
	}

	for in, want := range cases {
		if got := toGenaiTaskType(in); got != want {
			t.Errorf("toGenaiTaskType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToEmbedContentArgs_Errors(t *testing.T) {
	if _, _, err := toEmbedContentArgs(nil); err == nil {
		t.Error("expected error for nil request")
	}
	if _, _, err := toEmbedContentArgs(&embedding.Request{Model: "m"}); err == nil {
		t.Error("expected error for empty inputs")
	}
	if _, _, err := toEmbedContentArgs(&embedding.Request{
		Model:  "m",
		Inputs: []embedding.Input{{}},
	}); err == nil {
		t.Error("expected error for empty input item")
	}
}

func TestProviderEmbedRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		// Gemini API batchEmbedContents or embedContent response
		resp := map[string]any{
			"embeddings": []map[string]any{
				{
					"values": []float32{0.25, 0.5, 0.75},
					"statistics": map[string]any{
						"tokenCount": 5,
					},
				},
				{
					"values": []float32{-0.1, 0.2, 0.9},
					"statistics": map[string]any{
						"tokenCount": 7,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p, err := NewProvider(context.Background(), &genai.ClientConfig{
		APIKey:     "test-api-key",
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: server.Client(),
		HTTPOptions: genai.HTTPOptions{
			BaseURL: server.URL,
		},
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	ctx, span := tp.Tracer("test").Start(context.Background(), "genai-embed-test")
	dims := 3
	req := &embedding.Request{
		Model:      "gemini-embedding-001",
		Inputs:     embedding.Texts("query one", "query two"),
		Dimensions: &dims,
		TaskType:   embedding.TaskRetrievalQuery,
	}

	resp, err := p.Embed(ctx, req)
	span.End()

	if err != nil {
		t.Fatalf("unexpected error from Embed: %v", err)
	}

	if resp.Model != "gemini-embedding-001" {
		t.Errorf("expected model 'gemini-embedding-001', got %q", resp.Model)
	}
	if len(resp.Embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(resp.Embeddings))
	}
	if !reflect.DeepEqual(resp.Embeddings[0], embedding.Vector{0.25, 0.5, 0.75}) {
		t.Errorf("embedding[0] mismatch: %v", resp.Embeddings[0])
	}
	if !reflect.DeepEqual(resp.Embeddings[1], embedding.Vector{-0.1, 0.2, 0.9}) {
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
		if attr.Key == "genai.api.type" && attr.Value.AsString() == "embed_content" {
			foundType = true
		}
	}
	if !foundType {
		t.Errorf("expected genai.api.type='embed_content', got %v", spans[0].Attributes)
	}
}

func TestProviderEmbedErrors(t *testing.T) {
	var nilProvider *Provider
	if _, err := nilProvider.Embed(context.Background(), &embedding.Request{}); err == nil {
		t.Error("expected error for nil provider")
	}

	emptyProvider := &Provider{}
	if _, err := emptyProvider.Embed(context.Background(), &embedding.Request{}); err == nil {
		t.Error("expected error for nil client")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":500,"message":"internal error"}}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	p, err := NewProvider(context.Background(), &genai.ClientConfig{
		APIKey:     "test-api-key",
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: server.Client(),
		HTTPOptions: genai.HTTPOptions{
			BaseURL: server.URL,
		},
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	if _, err := p.Embed(context.Background(), nil); err == nil {
		t.Error("expected error for nil request")
	}

	if _, err := p.Embed(context.Background(), &embedding.Request{
		Model:  "gemini-embedding-001",
		Inputs: embedding.Texts("test"),
	}); err == nil {
		t.Error("expected error on server 500")
	}
}
