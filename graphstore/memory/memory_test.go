package memory_test

import (
	"context"
	"strings"
	"testing"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/graphstore"
	"github.com/masterkeysrd/loom/graphstore/memory"
)

type testEmbedProvider struct{}

func (testEmbedProvider) Name() string { return "graphstore-mem-embed" }

func (testEmbedProvider) Embed(_ context.Context, req *embedding.Request) (*embedding.Response, error) {
	vecs := make([]embedding.Vector, len(req.Inputs))
	for i, inp := range req.Inputs {
		lower := strings.ToLower(inp.Text)
		switch {
		case strings.Contains(lower, "kubernetes") || strings.Contains(lower, "k8s"):
			vecs[i] = embedding.Vector{1, 0, 0}
		case strings.Contains(lower, "redis"):
			vecs[i] = embedding.Vector{0, 1, 0}
		default:
			vecs[i] = embedding.Vector{0, 0, 1}
		}
	}
	return &embedding.Response{Model: req.Model, Embeddings: vecs}, nil
}

func TestMemoryGraphStore_UpsertSearchTraverseDelete(t *testing.T) {
	ctx := context.Background()
	gs := memory.New()

	docs := []graphstore.GraphDocument{
		{
			Source: document.New("Checkout API calls Auth Service which depends on Redis Cluster.").WithID("doc-1"),
			Nodes: []graphstore.Node{
				{ID: "checkout_api", Label: "Checkout API", Type: "Service", Properties: map[string]any{"tier": "edge"}},
				{ID: "auth_service", Label: "Auth Service", Type: "Service", Properties: map[string]any{"tier": "core"}},
				{ID: "redis_cluster", Label: "Redis Cluster", Type: "Infrastructure", Properties: map[string]any{"tier": "data"}},
			},
			Edges: []graphstore.Edge{
				{Source: "checkout_api", Target: "auth_service", Type: "CALLS"},
				{Source: "auth_service", Target: "redis_cluster", Type: "DEPENDS_ON"},
				{Source: "redis_cluster", Target: "checkout_api", Type: "NOTIFIES"}, // cycle edge
			},
		},
	}

	if err := gs.Upsert(ctx, docs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Search by BM25 + NodeType filter
	nodes, err := gs.SearchNodes(ctx, "Auth Service", 5, graphstore.WithNodeTypes("Service"))
	if err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	if len(nodes) == 0 || nodes[0].ID != "auth_service" {
		t.Fatalf("expected auth_service first, got %#v", nodes)
	}

	// Bidirectional 1-hop traversal from auth_service should reach checkout_api and redis_cluster
	sub, err := gs.Traverse(ctx, []string{"auth_service"}, 1)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if len(sub.Nodes) != 3 || len(sub.Edges) != 2 {
		t.Fatalf("expected 3 nodes & 2 edges at depth 1, got %d nodes, %d edges", len(sub.Nodes), len(sub.Edges))
	}
	if len(sub.SourceIDs) != 1 || sub.SourceIDs[0] != "doc-1" {
		t.Fatalf("expected provenance doc-1, got %v", sub.SourceIDs)
	}

	// Directed 1-hop traversal from auth_service only follows outgoing DEPENDS_ON
	directed, err := gs.Traverse(ctx, []string{"auth_service"}, 1, graphstore.WithBidirectional(false))
	if err != nil {
		t.Fatalf("Traverse directed: %v", err)
	}
	if len(directed.Nodes) != 2 || len(directed.Edges) != 1 || directed.Edges[0].Type != "DEPENDS_ON" {
		t.Fatalf("unexpected directed subgraph: %+v", directed)
	}

	// Cycle-safe multi-hop traversal
	deep, err := gs.Traverse(ctx, []string{"checkout_api"}, 5, graphstore.WithBidirectional(false))
	if err != nil {
		t.Fatalf("Traverse deep: %v", err)
	}
	if len(deep.Nodes) != 3 || len(deep.Edges) != 3 {
		t.Fatalf("expected full 3-node cycle without infinite loop, got %d nodes / %d edges", len(deep.Nodes), len(deep.Edges))
	}

	// Delete by doc ID removes edges while preserving nodes
	if err := gs.Delete(ctx, []string{"doc-1"}, graphstore.WithDeleteByDocID(true)); err != nil {
		t.Fatalf("Delete by doc ID: %v", err)
	}
	afterDocDel, err := gs.Traverse(ctx, []string{"auth_service"}, 2)
	if err != nil {
		t.Fatalf("Traverse after doc delete: %v", err)
	}
	if len(afterDocDel.Edges) != 0 || len(afterDocDel.Nodes) != 1 {
		t.Fatalf("expected 0 edges and 1 seed node after doc delete, got %+v", afterDocDel)
	}

	// Delete node
	if err := gs.Delete(ctx, []string{"auth_service"}); err != nil {
		t.Fatalf("Delete node: %v", err)
	}
	remaining, err := gs.SearchNodes(ctx, "Auth Service", 5)
	if err != nil {
		t.Fatalf("SearchNodes after delete: %v", err)
	}
	for _, n := range remaining {
		if n.ID == "auth_service" {
			t.Fatalf("auth_service should have been deleted")
		}
	}
}

func TestMemoryGraphStore_SemanticSearch(t *testing.T) {
	ctx := context.Background()
	embedModel, err := embedding.NewModel(testEmbedProvider{}, "test-embed", nil)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	gs := memory.New(memory.WithEmbedding(embedModel))

	if err := gs.Upsert(ctx, []graphstore.GraphDocument{{
		Nodes: []graphstore.Node{
			{ID: "kubernetes", Label: "Kubernetes", Type: "Platform"},
			{ID: "redis", Label: "Redis", Type: "Cache"},
		},
	}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Alias "k8s" resolves to Kubernetes via embeddings
	nodes, err := gs.SearchNodes(ctx, "k8s", 1)
	if err != nil {
		t.Fatalf("SearchNodes semantic: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != "kubernetes" {
		t.Fatalf("expected kubernetes via semantic search, got %#v", nodes)
	}
}
