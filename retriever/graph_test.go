package retriever_test

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/retriever"
	"github.com/masterkeysrd/loom/vectorstore/memory"
)

type graphTestEmbedProvider struct{}

func (graphTestEmbedProvider) Name() string { return "graph-test-embed" }

func (graphTestEmbedProvider) Embed(_ context.Context, req *embedding.Request) (*embedding.Response, error) {
	vecs := make([]embedding.Vector, len(req.Inputs))
	for i, inp := range req.Inputs {
		lower := strings.ToLower(inp.Text)
		switch {
		case lower == "mmr-query":
			vecs[i] = embedding.Vector{0.707, 0.707, 0.0}
		case strings.Contains(lower, "seed-a"):
			vecs[i] = embedding.Vector{1.0, 0.0, 0.0}
		case strings.Contains(lower, "near-dup"):
			vecs[i] = embedding.Vector{0.99, 0.01, 0.0}
		case strings.Contains(lower, "hop-1"):
			vecs[i] = embedding.Vector{0.0, 1.0, 0.0}
		case strings.Contains(lower, "hop-2"):
			vecs[i] = embedding.Vector{0.0, 0.2, 0.9}
		default:
			vecs[i] = embedding.Vector{0.0, 0.0, 1.0}
		}
	}
	return &embedding.Response{Model: req.Model, Embeddings: vecs}, nil
}

func TestGraphRetriever_EagerMultiHopAndCycleSafety(t *testing.T) {
	ctx := context.Background()
	model, err := embedding.NewModel(graphTestEmbedProvider{}, "graph-embed", nil)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	vs := memory.New(model)

	docs := []document.Document{
		document.New("seed-a entry document").WithID("doc-a").WithMetadata("links_to", []string{"doc-b"}),
		document.New("hop-1 linked neighbor").WithID("doc-b").WithMetadata("links_to", []string{"doc-c", "doc-a"}),
		document.New("hop-2 second-hop neighbor").WithID("doc-c").WithMetadata("links_to", []string{"doc-a"}),
		document.New("unrelated document").WithID("doc-z"),
	}
	if _, err := vs.Add(ctx, docs); err != nil {
		t.Fatalf("Add: %v", err)
	}

	gr := retriever.NewGraphRetriever(
		vs,
		[]retriever.EdgeSpec{{SourceKey: "links_to", TargetKey: retriever.DocumentIDKey}},
		retriever.WithGraphStartK(1),
		retriever.WithGraphMaxDepth(2),
		retriever.WithGraphAdjacentK(4),
		retriever.WithGraphDepthDecay(0.8),
		retriever.WithGraphStrategy(retriever.TraversalEager),
	)

	got, err := gr.Retrieve(ctx, "seed-a", retriever.WithTopK(5))
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 docs (doc-a, doc-b, doc-c), got %d: %#v", len(got), got)
	}
	if got[0].ID != "doc-a" || got[1].ID != "doc-b" || got[2].ID != "doc-c" {
		t.Fatalf("unexpected order: %q, %q, %q", got[0].ID, got[1].ID, got[2].ID)
	}

	// Verify decay math: doc-a=1.0, doc-b=0.8, doc-c=0.8 * 0.8^2 = 0.512
	if math.Abs(float64(got[0].Score-1.0)) > 1e-4 {
		t.Errorf("expected doc-a score ~1.0, got %v", got[0].Score)
	}
	if math.Abs(float64(got[1].Score-0.8)) > 1e-4 {
		t.Errorf("expected doc-b score ~0.8, got %v", got[1].Score)
	}
	if got[2].Score >= got[1].Score || got[2].Score <= 0 {
		t.Errorf("expected doc-c score decayed below doc-b, got %v", got[2].Score)
	}
}

func TestGraphRetriever_SharedEntityMetadataAndMMR(t *testing.T) {
	ctx := context.Background()
	model, err := embedding.NewModel(graphTestEmbedProvider{}, "graph-embed", nil)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	vs := memory.New(model)

	docs := []document.Document{
		document.New("seed-a auth overview").WithID("auth-1").WithMetadata("entities", []string{"OAuth", "Redis"}),
		document.New("near-dup auth overview copy").WithID("auth-2").WithMetadata("entities", []string{"OAuth"}),
		document.New("hop-1 redis session store details").WithID("redis-1").WithMetadata("entities", []string{"Redis", "Cluster"}),
	}
	if _, err := vs.Add(ctx, docs); err != nil {
		t.Fatalf("Add: %v", err)
	}

	gr := retriever.NewGraphRetriever(
		vs,
		[]retriever.EdgeSpec{{SourceKey: "entities", TargetKey: "entities"}},
		retriever.WithGraphStartK(2),
		retriever.WithGraphMaxDepth(1),
		retriever.WithGraphAdjacentK(4),
		retriever.WithGraphStrategy(retriever.TraversalMMR),
		retriever.WithGraphMMRLambda(0.5),
	)

	got, err := gr.Retrieve(ctx, "mmr-query", retriever.WithTopK(2))
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(got))
	}
	hasRedis := got[0].ID == "redis-1" || got[1].ID == "redis-1"
	if !hasRedis {
		t.Fatalf("expected MMR over graph pool to select diverse neighbor redis-1, got %#v", got)
	}
}
