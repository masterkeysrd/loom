package vectorstore_test

import (
	"context"
	"testing"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/vectorstore"
)

func TestOptions(t *testing.T) {
	// Add options
	defaultAdd := vectorstore.ApplyAddOptions()
	if defaultAdd.Namespace != vectorstore.DefaultNamespace {
		t.Errorf("expected default namespace %q, got %q", vectorstore.DefaultNamespace, defaultAdd.Namespace)
	}

	customAdd := vectorstore.ApplyAddOptions(vectorstore.WithAddNamespace("custom_add"))
	if customAdd.Namespace != "custom_add" {
		t.Errorf("expected %q, got %q", "custom_add", customAdd.Namespace)
	}

	// Search options
	defaultSearch := vectorstore.ApplySearchOptions()
	if defaultSearch.Namespace != vectorstore.DefaultNamespace {
		t.Errorf("expected default namespace %q, got %q", vectorstore.DefaultNamespace, defaultSearch.Namespace)
	}
	if defaultSearch.HasMinScore {
		t.Errorf("expected HasMinScore false")
	}

	filter := map[string]any{"category": "news"}
	customSearch := vectorstore.ApplySearchOptions(
		vectorstore.WithNamespace("custom_search"),
		vectorstore.WithMinScore(0.85),
		vectorstore.WithFilter(filter),
	)
	if customSearch.Namespace != "custom_search" {
		t.Errorf("expected %q, got %q", "custom_search", customSearch.Namespace)
	}
	if !customSearch.HasMinScore || customSearch.MinScore != 0.85 {
		t.Errorf("expected min score 0.85, got %v (has: %v)", customSearch.MinScore, customSearch.HasMinScore)
	}
	if customSearch.Filter["category"] != "news" {
		t.Errorf("expected filter category=news, got %v", customSearch.Filter)
	}

	mmrSearch := vectorstore.ApplySearchOptions(
		vectorstore.WithMMR(0.7, 20),
	)
	if !mmrSearch.UseMMR || mmrSearch.MMRLambda != 0.7 || mmrSearch.MMRFetchK != 20 {
		t.Errorf("unexpected MMR search config: %+v", mmrSearch)
	}

	// Clamping lambda
	clampedLow := vectorstore.ApplySearchOptions(vectorstore.WithMMR(-0.5, 10))
	if clampedLow.MMRLambda != 0.0 {
		t.Errorf("expected lambda clamped to 0.0, got %v", clampedLow.MMRLambda)
	}
	clampedHigh := vectorstore.ApplySearchOptions(vectorstore.WithMMR(1.5, 10))
	if clampedHigh.MMRLambda != 1.0 {
		t.Errorf("expected lambda clamped to 1.0, got %v", clampedHigh.MMRLambda)
	}

	// Delete options
	defaultDelete := vectorstore.ApplyDeleteOptions()
	if defaultDelete.Namespace != vectorstore.DefaultNamespace {
		t.Errorf("expected default namespace %q, got %q", vectorstore.DefaultNamespace, defaultDelete.Namespace)
	}

	customDelete := vectorstore.ApplyDeleteOptions(vectorstore.WithDeleteNamespace("custom_delete"))
	if customDelete.Namespace != "custom_delete" {
		t.Errorf("expected %q, got %q", "custom_delete", customDelete.Namespace)
	}
}

func TestMatchesFilter(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]any
		filter   map[string]any
		want     bool
	}{
		{
			name:     "empty filter always matches non-nil metadata",
			metadata: map[string]any{"a": 1},
			filter:   nil,
			want:     true,
		},
		{
			name:     "empty filter matches nil metadata",
			metadata: nil,
			filter:   nil,
			want:     true,
		},
		{
			name:     "nil metadata fails non-empty filter",
			metadata: nil,
			filter:   map[string]any{"a": 1},
			want:     false,
		},
		{
			name:     "exact string match",
			metadata: map[string]any{"author": "alice", "year": 2024},
			filter:   map[string]any{"author": "alice"},
			want:     true,
		},
		{
			name:     "missing key in metadata",
			metadata: map[string]any{"author": "alice"},
			filter:   map[string]any{"author": "alice", "genre": "tech"},
			want:     false,
		},
		{
			name:     "numeric normalization int in filter and float64 in metadata",
			metadata: map[string]any{"count": float64(42)},
			filter:   map[string]any{"count": 42},
			want:     true,
		},
		{
			name:     "numeric normalization float64 in filter and int in metadata",
			metadata: map[string]any{"count": 42},
			filter:   map[string]any{"count": float64(42.0)},
			want:     true,
		},
		{
			name:     "numeric normalization float32 in metadata and int64 in filter",
			metadata: map[string]any{"count": float32(100.0)},
			filter:   map[string]any{"count": int64(100)},
			want:     true,
		},
		{
			name:     "numeric mismatch",
			metadata: map[string]any{"count": float64(42)},
			filter:   map[string]any{"count": 43},
			want:     false,
		},
		{
			name:     "boolean match",
			metadata: map[string]any{"published": true},
			filter:   map[string]any{"published": true},
			want:     true,
		},
		{
			name:     "boolean mismatch",
			metadata: map[string]any{"published": false},
			filter:   map[string]any{"published": true},
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := vectorstore.MatchesFilter(tc.metadata, tc.filter)
			if got != tc.want {
				t.Errorf("MatchesFilter(%v, %v) = %v; want %v", tc.metadata, tc.filter, got, tc.want)
			}
		})
	}
}

type dummyProvider struct{}

func (d dummyProvider) Name() string { return "dummy" }
func (d dummyProvider) Embed(ctx context.Context, req *embedding.Request) (*embedding.Response, error) {
	return &embedding.Response{}, nil
}

type dummyProfileProvider struct {
	prof profile.Profile
}

func (d dummyProfileProvider) Name() string { return "dummy-profile" }
func (d dummyProfileProvider) Embed(ctx context.Context, req *embedding.Request) (*embedding.Response, error) {
	return &embedding.Response{}, nil
}
func (d dummyProfileProvider) GetProfile(name string) (profile.Profile, bool) {
	return d.prof, true
}
func (d dummyProfileProvider) ListProfiles() []profile.Profile {
	return []profile.Profile{d.prof}
}

func TestResolveDims(t *testing.T) {
	// 1. Explicit dims take precedence
	dims := vectorstore.ResolveDims(nil, 512)
	if dims != 512 {
		t.Errorf("expected 512, got %d", dims)
	}

	// 2. Nil embed with 0 explicit
	dims = vectorstore.ResolveDims(nil, 0)
	if dims != 0 {
		t.Errorf("expected 0, got %d", dims)
	}

	// 3. Model config dims
	m1, err := embedding.NewModel(dummyProvider{}, "test-model", nil)
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}
	m1 = m1.WithDimensions(256)
	dims = vectorstore.ResolveDims(m1, 0)
	if dims != 256 {
		t.Errorf("expected 256 from config, got %d", dims)
	}

	// Explicit override of model with config
	dims = vectorstore.ResolveDims(m1, 1024)
	if dims != 1024 {
		t.Errorf("expected 1024 explicit override, got %d", dims)
	}

	// 4. Model profile limits dims
	prof := profile.Profile{
		Name: "prof-model",
		Limits: profile.Limits{
			Dimensions: 768,
		},
	}
	m2, err := embedding.NewModel(dummyProfileProvider{prof: prof}, "prof-model", nil)
	if err != nil {
		t.Fatalf("failed to create profile model: %v", err)
	}
	dims = vectorstore.ResolveDims(m2, 0)
	if dims != 768 {
		t.Errorf("expected 768 from profile, got %d", dims)
	}
}

func TestVectorstoreMMR(t *testing.T) {
	q := embedding.Vector{1, 1, 0}
	candidates := []embedding.Vector{
		{1, 0, 0},
		{1, 0, 0}, // duplicate
		{0, 1, 0}, // diverse
	}

	docs := []document.Document{
		document.New("doc0").WithID("0"),
		document.New("doc1").WithID("1"),
		document.New("doc2").WithID("2"),
	}

	selected := vectorstore.SelectMMR(q, docs, candidates, 0.5, 2)
	if len(selected) != 2 || selected[0].ID != "0" || selected[1].ID != "2" {
		t.Fatalf("expected docs 0 and 2, got %v", selected)
	}

	if vectorstore.SelectMMR(q, nil, candidates, 0.5, 2) != nil {
		t.Errorf("expected nil for empty docs")
	}
}
