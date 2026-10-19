package store_test

import (
	"context"
	"testing"

	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/store"
)

type mockProvider struct {
	dims int
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) Embed(_ context.Context, req *embedding.Request) (*embedding.Response, error) {
	vecs := make([]embedding.Vector, len(req.Inputs))
	for i := range req.Inputs {
		vecs[i] = make(embedding.Vector, m.dims)
		vecs[i][0] = 1
	}
	return &embedding.Response{Model: req.Model, Embeddings: vecs}, nil
}

func (m *mockProvider) GetProfile(id string) (profile.Profile, bool) {
	return profile.Profile{
		ID:     id,
		Limits: profile.Limits{Dimensions: m.dims},
	}, true
}

func TestExtractText(t *testing.T) {
	t.Run("entire JSON when fields is empty or $", func(t *testing.T) {
		raw := []byte(`{"text":"Python tutorial","count":3}`)
		if got := store.ExtractText(raw, nil); got != string(raw) {
			t.Fatalf("expected %q, got %q", string(raw), got)
		}
		if got := store.ExtractText(raw, []string{"$"}); got != string(raw) {
			t.Fatalf("expected %q, got %q", string(raw), got)
		}
	})

	t.Run("plain string JSON literal", func(t *testing.T) {
		raw := []byte(`"hello world"`)
		if got := store.ExtractText(raw, nil); got != "hello world" {
			t.Fatalf("expected %q, got %q", "hello world", got)
		}
	})

	t.Run("gjson paths including nested objects and array queries", func(t *testing.T) {
		raw := []byte(`{
			"title": "Agent Workflows",
			"meta": {"summary": "Building stateful graphs in Go"},
			"tags": ["go", "ai"],
			"sections": [{"heading": "Intro"}, {"heading": "Conclusion"}]
		}`)
		got := store.ExtractText(raw, []string{"title", "meta.summary", "tags", "sections.#.heading", "missing"})
		want := "Agent Workflows\nBuilding stateful graphs in Go\ngo\nai\nIntro\nConclusion"
		if got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})

	t.Run("missing fields return empty string", func(t *testing.T) {
		raw := []byte(`{"other": "value"}`)
		if got := store.ExtractText(raw, []string{"text"}); got != "" {
			t.Fatalf("expected empty string, got %q", got)
		}
	})
}

func TestIndexConfigFunctions(t *testing.T) {
	p := &mockProvider{dims: 4}
	model, err := embedding.NewModel(p, "test-embed", nil)
	if err != nil {
		t.Fatal(err)
	}

	cfg := store.IndexConfig{
		Embed:  model,
		Fields: []string{"text"},
	}

	if !store.IndexEnabled(cfg) {
		t.Fatal("expected IndexEnabled(cfg) to be true")
	}
	if got := store.ResolveIndexDims(cfg); got != 4 {
		t.Fatalf("expected ResolveIndexDims(cfg) == 4 from model profile, got %d", got)
	}

	ctx := context.Background()
	vec, err := store.EmbedValue(ctx, cfg, []byte(`{"text":"hello"}`), &store.PutConfig{})
	if err != nil {
		t.Fatalf("EmbedValue: %v", err)
	}
	if len(vec) != 4 || vec[0] != 1 {
		t.Fatalf("unexpected vector: %v", vec)
	}

	// Disabled per-put
	vecDisabled, err := store.EmbedValue(ctx, cfg, []byte(`{"text":"hello"}`), &store.PutConfig{DisableIndex: true})
	if err != nil || vecDisabled != nil {
		t.Fatalf("expected nil vector when indexing disabled, got %v, err=%v", vecDisabled, err)
	}

	qVec, err := store.EmbedQuery(ctx, cfg, "search query")
	if err != nil || len(qVec) != 4 {
		t.Fatalf("EmbedQuery failed: vec=%v, err=%v", qVec, err)
	}
}
