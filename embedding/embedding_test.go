package embedding_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/stream"
	"github.com/masterkeysrd/loom/telemetry"
)

type mockExtension struct {
	id string
}

func (m mockExtension) ExtensionID() string {
	return m.id
}

type mockStreamWriter struct {
	events []stream.Event
}

func (w *mockStreamWriter) Write(ctx context.Context, data any) error {
	if evt, ok := data.(stream.Event); ok {
		w.events = append(w.events, evt)
	}
	return nil
}

type mockEmbeddingProvider struct {
	name      string
	embedFunc func(ctx context.Context, req *embedding.Request) (*embedding.Response, error)
	profiles  map[string]llm.ModelProfile
}

func (m *mockEmbeddingProvider) Name() string {
	return m.name
}

func (m *mockEmbeddingProvider) Embed(ctx context.Context, req *embedding.Request) (*embedding.Response, error) {
	if m.embedFunc != nil {
		return m.embedFunc(ctx, req)
	}
	vecs := make([]embedding.Vector, len(req.Inputs))
	for i := range req.Inputs {
		vecs[i] = embedding.Vector{float32(i + 1), 0.5, 0.25}
	}
	return &embedding.Response{
		Model:      req.Model,
		Embeddings: vecs,
	}, nil
}

func (m *mockEmbeddingProvider) GetProfile(id string) (llm.ModelProfile, bool) {
	if m.profiles == nil {
		return llm.ModelProfile{}, false
	}
	p, ok := m.profiles[id]
	return p, ok
}

type simpleMockProvider struct {
	name string
}

func (s *simpleMockProvider) Name() string { return s.name }
func (s *simpleMockProvider) Embed(ctx context.Context, req *embedding.Request) (*embedding.Response, error) {
	return &embedding.Response{
		Model:      req.Model,
		Embeddings: []embedding.Vector{{0.1, 0.2, 0.3}},
	}, nil
}

func TestVectorMath(t *testing.T) {
	t.Run("Dimension and Float64", func(t *testing.T) {
		var nilVec embedding.Vector
		if nilVec.Dimension() != 0 {
			t.Errorf("expected dimension 0 for nil vector, got %d", nilVec.Dimension())
		}
		if nilVec.Float64() != nil {
			t.Errorf("expected nil float64 slice for nil vector")
		}

		v := embedding.Vector{1.5, -2.0, 3.25}
		if v.Dimension() != 3 {
			t.Errorf("expected dimension 3, got %d", v.Dimension())
		}
		f64 := v.Float64()
		expected := []float64{1.5, -2.0, 3.25}
		if !reflect.DeepEqual(f64, expected) {
			t.Errorf("expected %v, got %v", expected, f64)
		}
	})

	t.Run("Dot", func(t *testing.T) {
		v1 := embedding.Vector{1, 2, 3}
		v2 := embedding.Vector{4, -5, 6}
		// 1*4 + 2*(-5) + 3*6 = 4 - 10 + 18 = 12
		if dot := v1.Dot(v2); dot != 12 {
			t.Errorf("expected dot product 12, got %v", dot)
		}

		// Mismatched lengths or empty
		if dot := v1.Dot(embedding.Vector{1, 2}); dot != 0 {
			t.Errorf("expected 0 for mismatched lengths, got %v", dot)
		}
		if dot := (embedding.Vector{}).Dot(embedding.Vector{}); dot != 0 {
			t.Errorf("expected 0 for empty vectors, got %v", dot)
		}
	})

	t.Run("CosineSimilarity", func(t *testing.T) {
		v1 := embedding.Vector{1, 0, 0}
		v2 := embedding.Vector{1, 0, 0}
		if sim := v1.CosineSimilarity(v2); math.Abs(float64(sim-1.0)) > 1e-6 {
			t.Errorf("expected cosine similarity 1.0 for identical vectors, got %v", sim)
		}

		vOrtho := embedding.Vector{0, 1, 0}
		if sim := v1.CosineSimilarity(vOrtho); math.Abs(float64(sim)) > 1e-6 {
			t.Errorf("expected cosine similarity 0.0 for orthogonal vectors, got %v", sim)
		}

		vOpposite := embedding.Vector{-2, 0, 0}
		if sim := v1.CosineSimilarity(vOpposite); math.Abs(float64(sim+1.0)) > 1e-6 {
			t.Errorf("expected cosine similarity -1.0 for opposite vectors, got %v", sim)
		}

		// Zero vector or mismatched length
		vZero := embedding.Vector{0, 0, 0}
		if sim := v1.CosineSimilarity(vZero); sim != 0 {
			t.Errorf("expected 0 for zero magnitude vector, got %v", sim)
		}
		if sim := v1.CosineSimilarity(embedding.Vector{1, 0}); sim != 0 {
			t.Errorf("expected 0 for mismatched length, got %v", sim)
		}
	})

	t.Run("EuclideanDistance", func(t *testing.T) {
		v1 := embedding.Vector{0, 0}
		v2 := embedding.Vector{3, 4}
		if dist := v1.EuclideanDistance(v2); math.Abs(float64(dist-5.0)) > 1e-6 {
			t.Errorf("expected distance 5.0, got %v", dist)
		}

		if dist := v1.EuclideanDistance(embedding.Vector{1}); dist != 0 {
			t.Errorf("expected 0 for mismatched length, got %v", dist)
		}
		if dist := (embedding.Vector{}).EuclideanDistance(embedding.Vector{}); dist != 0 {
			t.Errorf("expected 0 for empty vectors, got %v", dist)
		}
	})

	t.Run("Normalize", func(t *testing.T) {
		if norm := (embedding.Vector{}).Normalize(); norm != nil {
			t.Errorf("expected nil for empty vector Normalize, got %v", norm)
		}

		vZero := embedding.Vector{0, 0, 0}
		normZero := vZero.Normalize()
		if !reflect.DeepEqual(normZero, vZero) {
			t.Errorf("expected zero vector copy, got %v", normZero)
		}

		v := embedding.Vector{3, 4}
		n := v.Normalize()
		if math.Abs(float64(n[0]-0.6)) > 1e-6 || math.Abs(float64(n[1]-0.8)) > 1e-6 {
			t.Errorf("expected [0.6, 0.8], got %v", n)
		}
		// Ensure original was not mutated
		if v[0] != 3 || v[1] != 4 {
			t.Errorf("original vector was mutated: %v", v)
		}
	})
}

func TestInputConstructors(t *testing.T) {
	inText := embedding.Text("hello world")
	if inText.Text != "hello world" || len(inText.Parts) != 0 {
		t.Errorf("unexpected Text input: %+v", inText)
	}

	inTexts := embedding.Texts("a", "b", "c")
	if len(inTexts) != 3 || inTexts[0].Text != "a" || inTexts[1].Text != "b" || inTexts[2].Text != "c" {
		t.Errorf("unexpected Texts slice: %+v", inTexts)
	}

	inImg := embedding.Image([]byte("png-bytes"), "image/png")
	if len(inImg.Parts) != 1 {
		t.Fatalf("expected 1 part in Image input, got %d", len(inImg.Parts))
	}
	if ib, ok := inImg.Parts[0].(*message.ImageBlock); !ok || string(ib.Data) != "png-bytes" || ib.MIMEType != "image/png" {
		t.Errorf("unexpected ImageBlock: %+v", inImg.Parts[0])
	}

	inImgURL := embedding.ImageURL("https://example.com/pic.jpg", "image/jpeg")
	if len(inImgURL.Parts) != 1 {
		t.Fatalf("expected 1 part in ImageURL input, got %d", len(inImgURL.Parts))
	}
	if ib, ok := inImgURL.Parts[0].(*message.ImageBlock); !ok || ib.URL != "https://example.com/pic.jpg" || ib.MIMEType != "image/jpeg" {
		t.Errorf("unexpected ImageBlock from URL: %+v", inImgURL.Parts[0])
	}

	inMulti := embedding.Multimodal(
		&message.TextBlock{Text: "caption"},
		&message.AudioBlock{Data: []byte("wav"), MIMEType: "audio/wav"},
	)
	if len(inMulti.Parts) != 2 {
		t.Fatalf("expected 2 parts in Multimodal input, got %d", len(inMulti.Parts))
	}

	inWithParts := embedding.Text("caption").WithParts(&message.ImageBlock{URL: "https://example.com/a.png"})
	if inWithParts.Text != "caption" || len(inWithParts.Parts) != 1 {
		t.Errorf("unexpected WithParts input: %+v", inWithParts)
	}
}

func TestResponseFirst(t *testing.T) {
	var nilResp *embedding.Response
	if nilResp.First() != nil {
		t.Error("expected nil from First() on nil Response")
	}

	emptyResp := &embedding.Response{}
	if emptyResp.First() != nil {
		t.Error("expected nil from First() on empty Response")
	}

	resp := &embedding.Response{
		Embeddings: []embedding.Vector{
			{0.1, 0.2},
			{0.3, 0.4},
		},
	}
	if !reflect.DeepEqual(resp.First(), embedding.Vector{0.1, 0.2}) {
		t.Errorf("expected [0.1, 0.2], got %v", resp.First())
	}
}

func TestNewModelAndFluentConfig(t *testing.T) {
	t.Run("nil provider or empty name", func(t *testing.T) {
		if _, err := embedding.NewModel(nil, "m", nil); err == nil {
			t.Fatal("expected error for nil provider")
		}
		p := &simpleMockProvider{name: "test"}
		if _, err := embedding.NewModel(p, "", nil); err == nil {
			t.Fatal("expected error for empty model name")
		}
	})

	t.Run("with profile provider and fluent cloning", func(t *testing.T) {
		p := &mockEmbeddingProvider{
			name: "openai",
			profiles: map[string]llm.ModelProfile{
				"text-embedding-3-small": {
					ID:   "text-embedding-3-small",
					Name: "Text Embedding 3 Small",
					Capabilities: llm.Capabilities{
						Embedding: true,
					},
					Pricing: llm.Pricing{
						Input: 0.02,
					},
				},
			},
		}

		dims := 512
		initCfg := &embedding.ModelConfig{
			Dimensions: &dims,
			TaskType:   embedding.TaskSemanticSimilarity,
			Options:    map[string]any{"user": "u1"},
		}
		m1, err := embedding.NewModel(p, "text-embedding-3-small", initCfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Mutate initCfg to verify deep copy
		dims = 1024
		initCfg.Options["user"] = "mutated"
		if *m1.Config().Dimensions != 512 || m1.Config().Options["user"] != "u1" {
			t.Fatal("model config was mutated externally")
		}

		if m1.Name() != "text-embedding-3-small" {
			t.Errorf("expected name 'text-embedding-3-small', got %q", m1.Name())
		}
		if m1.Provider() != p {
			t.Error("provider mismatch")
		}
		if m1.Profile() == nil || !m1.Profile().Capabilities.Embedding {
			t.Errorf("expected embedding profile, got %+v", m1.Profile())
		}

		ext := mockExtension{id: "ext-1"}
		m2 := m1.WithDimensions(256).
			WithTaskType(embedding.TaskClustering).
			WithExtension(ext).
			WithOption("opt_key", "opt_val")

		// Verify m1 is untouched
		if *m1.Config().Dimensions != 512 || m1.Config().TaskType != embedding.TaskSemanticSimilarity {
			t.Error("m1 was mutated by fluent methods on m2")
		}
		if *m2.Config().Dimensions != 256 || m2.Config().TaskType != embedding.TaskClustering {
			t.Errorf("unexpected m2 config: %+v", m2.Config())
		}
		if m2.Config().Extensions["ext-1"] != ext || m2.Config().Options["opt_key"] != "opt_val" {
			t.Errorf("unexpected m2 extensions/options: %+v", m2.Config())
		}

		// WithConfig replaces config
		m3 := m2.WithConfig(embedding.ModelConfig{TaskType: embedding.TaskClassification})
		if m3.Config().Dimensions != nil || m3.Config().TaskType != embedding.TaskClassification {
			t.Errorf("unexpected m3 config after WithConfig: %+v", m3.Config())
		}
	})
}

func TestModelEmbedAndConvenienceMethods(t *testing.T) {
	t.Run("empty inputs error", func(t *testing.T) {
		p := &simpleMockProvider{name: "test"}
		m, _ := embedding.NewModel(p, "embed-1", nil)
		if _, err := m.Embed(context.Background(), nil); err == nil {
			t.Fatal("expected error for empty inputs")
		}
	})

	t.Run("provider error propagation", func(t *testing.T) {
		expectedErr := errors.New("embed backend error")
		p := &mockEmbeddingProvider{
			name: "err-p",
			embedFunc: func(ctx context.Context, req *embedding.Request) (*embedding.Response, error) {
				return nil, expectedErr
			},
		}
		m, _ := embedding.NewModel(p, "embed-1", nil)
		if _, err := m.EmbedInput(context.Background(), embedding.Text("test")); !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("Embed cost estimation and stream event", func(t *testing.T) {
		var capturedReq *embedding.Request
		p := &mockEmbeddingProvider{
			name: "openai",
			embedFunc: func(ctx context.Context, req *embedding.Request) (*embedding.Response, error) {
				capturedReq = req
				return &embedding.Response{
					Model: req.Model,
					Embeddings: []embedding.Vector{
						{0.25, 0.75},
					},
					Metrics: &message.TokenMetrics{
						TotalTokens: 100,
						Tokens: message.TokenDetails{
							Input: 100,
						},
					},
				}, nil
			},
			profiles: map[string]llm.ModelProfile{
				"text-embedding-3-small": {
					ID:   "text-embedding-3-small",
					Name: "Text Embedding 3 Small",
					Pricing: llm.Pricing{
						Input: 1.0, // $1 per million => 1000 nano-dollars per token
					},
				},
			},
		}

		m, _ := embedding.NewModel(p, "text-embedding-3-small", nil)
		writer := &mockStreamWriter{}
		ctx := stream.WithWriter(context.Background(), writer)

		ext := mockExtension{id: "custom-ext"}
		resp, err := m.Embed(ctx, embedding.Texts("hello world"),
			embedding.WithDimensionsOption(512),
			embedding.WithTaskTypeOption(embedding.TaskSemanticSimilarity),
			embedding.WithExtensionOption(ext),
			embedding.WithCallOption("encoding", "float"),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if capturedReq.Dimensions == nil || *capturedReq.Dimensions != 512 {
			t.Errorf("expected dimensions 512, got %v", capturedReq.Dimensions)
		}
		if capturedReq.TaskType != embedding.TaskSemanticSimilarity {
			t.Errorf("expected task type %q, got %q", embedding.TaskSemanticSimilarity, capturedReq.TaskType)
		}
		if capturedReq.Extensions["custom-ext"] != ext {
			t.Error("expected extension to be forwarded")
		}
		if capturedReq.Options["encoding"] != "float" {
			t.Error("expected call option to be forwarded")
		}

		// Verify token cost calculation: 100 tokens * 1000 nano-dollars = 100,000 nano-dollars
		expectedCost := message.Cost(100 * 1000)
		if resp.Metrics == nil || resp.Metrics.TotalCost != expectedCost {
			t.Errorf("expected total cost %v, got %+v", expectedCost, resp.Metrics)
		}

		// Verify stream event
		if len(writer.events) != 1 {
			t.Fatalf("expected 1 stream event, got %d", len(writer.events))
		}
		if writer.events[0].Name != "on_embedding" {
			t.Errorf("expected event name 'on_embedding', got %q", writer.events[0].Name)
		}
		if writer.events[0].Data != resp {
			t.Error("expected event data to match response")
		}
	})

	t.Run("EmbedQuery and EmbedDocuments task type defaults", func(t *testing.T) {
		var lastTaskType embedding.TaskType
		p := &mockEmbeddingProvider{
			name: "google-genai",
			embedFunc: func(ctx context.Context, req *embedding.Request) (*embedding.Response, error) {
				lastTaskType = req.TaskType
				vecs := make([]embedding.Vector, len(req.Inputs))
				for i := range req.Inputs {
					vecs[i] = embedding.Vector{float32(i + 1)}
				}
				return &embedding.Response{
					Model:      req.Model,
					Embeddings: vecs,
				}, nil
			},
		}

		m, _ := embedding.NewModel(p, "gemini-embedding-001", nil)

		// 1. EmbedQuery defaults to TaskRetrievalQuery
		vec, err := m.EmbedQuery(context.Background(), "what is loom?")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(vec) != 1 || vec[0] != 1 {
			t.Errorf("unexpected query vector: %v", vec)
		}
		if lastTaskType != embedding.TaskRetrievalQuery {
			t.Errorf("expected TaskRetrievalQuery, got %q", lastTaskType)
		}

		// 2. EmbedQuery with explicit CallOption override
		_, err = m.EmbedQuery(context.Background(), "what is loom?", embedding.WithTaskTypeOption(embedding.TaskClassification))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lastTaskType != embedding.TaskClassification {
			t.Errorf("expected TaskClassification override, got %q", lastTaskType)
		}

		// 3. EmbedDocuments defaults to TaskRetrievalDocument
		vecs, err := m.EmbedDocuments(context.Background(), []string{"doc 1", "doc 2"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(vecs) != 2 {
			t.Fatalf("expected 2 vectors, got %d", len(vecs))
		}
		if lastTaskType != embedding.TaskRetrievalDocument {
			t.Errorf("expected TaskRetrievalDocument, got %q", lastTaskType)
		}

		// 4. Model with pre-configured TaskType preserves model's TaskType
		mCustom := m.WithTaskType(embedding.TaskClustering)
		_, err = mCustom.EmbedQuery(context.Background(), "cluster me")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lastTaskType != embedding.TaskClustering {
			t.Errorf("expected model TaskClustering to be preserved, got %q", lastTaskType)
		}
	})
}

func TestMiddlewareExecutionOrder(t *testing.T) {
	p := &simpleMockProvider{name: "test"}
	m, _ := embedding.NewModel(p, "test-model", nil)

	var order []string
	mw1 := func(next embedding.Embedder) embedding.Embedder {
		return func(ctx context.Context, req *embedding.Request) (*embedding.Response, error) {
			order = append(order, "mw1_before")
			resp, err := next(ctx, req)
			order = append(order, "mw1_after")
			return resp, err
		}
	}
	mw2 := func(next embedding.Embedder) embedding.Embedder {
		return func(ctx context.Context, req *embedding.Request) (*embedding.Response, error) {
			order = append(order, "mw2_before")
			resp, err := next(ctx, req)
			order = append(order, "mw2_after")
			return resp, err
		}
	}

	m = m.WithMiddleware(mw1, mw2)
	if _, err := m.EmbedInput(context.Background(), embedding.Text("test")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"mw1_before", "mw2_before", "mw2_after", "mw1_after"}
	if !reflect.DeepEqual(order, expected) {
		t.Errorf("expected middleware order %v, got %v", expected, order)
	}
}

func TestTelemetryMiddleware(t *testing.T) {
	providers := []string{"openai", "anthropic", "google-genai", "ollama"}
	for _, pName := range providers {
		t.Run(pName, func(t *testing.T) {
			p := &mockEmbeddingProvider{
				name: pName,
				embedFunc: func(ctx context.Context, req *embedding.Request) (*embedding.Response, error) {
					return &embedding.Response{
						Model:      req.Model,
						Embeddings: []embedding.Vector{{0.1, 0.2}},
						Metrics: &message.TokenMetrics{
							TotalTokens: 15,
							Tokens: message.TokenDetails{
								Input: 15,
							},
						},
					}, nil
				},
			}

			m, _ := embedding.NewModel(p, "embed-model", nil)
			m = m.WithDimensions(256).WithTaskType(embedding.TaskRetrievalQuery)
			ctx := telemetry.WithContentRecording(context.Background())
			ctx = context.WithValue(ctx, telemetry.KeyLoomThreadID, "thread-xyz")

			_, err := m.EmbedInput(ctx, embedding.Text("telemetry test"))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
