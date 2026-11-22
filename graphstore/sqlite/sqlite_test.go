package sqlite_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/graphstore"
	gssqlite "github.com/masterkeysrd/loom/graphstore/sqlite"
)

type sqliteEmbedProvider struct{}

func (sqliteEmbedProvider) Name() string { return "graphstore-sqlite-embed" }

func (sqliteEmbedProvider) Embed(_ context.Context, req *embedding.Request) (*embedding.Response, error) {
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

func TestSQLiteGraphStore_UpsertFTS5TraverseCTEAndDelete(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	gs, err := gssqlite.New(db)
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}

	docs := []graphstore.GraphDocument{
		{
			Source: document.New("Checkout API calls Auth Service which depends on Redis Cluster.").WithID("doc-1"),
			Nodes: []graphstore.Node{
				{ID: "checkout_api", Label: "Checkout API", Type: "Service", Properties: map[string]any{"tier": "edge"}},
				{ID: "auth_service", Label: "Auth Service", Type: "Service", Properties: map[string]any{"tier": "core"}},
				{ID: "redis_cluster", Label: "Redis Cluster", Type: "Infrastructure", Properties: map[string]any{"tier": "data"}},
			},
			Edges: []graphstore.Edge{
				{Source: "checkout_api", Target: "auth_service", Type: "CALLS", Weight: 0.9},
				{Source: "auth_service", Target: "redis_cluster", Type: "DEPENDS_ON", Weight: 1.0},
				{Source: "redis_cluster", Target: "checkout_api", Type: "NOTIFIES", Weight: 0.5},
			},
		},
	}

	if err := gs.Upsert(ctx, docs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// FTS5 trigram lexical search + NodeType filter
	nodes, err := gs.SearchNodes(ctx, "Auth", 5, graphstore.WithNodeTypes("Service"))
	if err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	if len(nodes) == 0 || nodes[0].ID != "auth_service" {
		t.Fatalf("expected auth_service via FTS5, got %#v", nodes)
	}

	// Bidirectional 1-hop CTE traversal from auth_service
	sub, err := gs.Traverse(ctx, []string{"auth_service"}, 1)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if len(sub.Nodes) != 3 || len(sub.Edges) != 2 {
		t.Fatalf("expected 3 nodes & 2 edges, got %d nodes, %d edges (%+v)", len(sub.Nodes), len(sub.Edges), sub)
	}
	if len(sub.SourceIDs) != 1 || sub.SourceIDs[0] != "doc-1" {
		t.Fatalf("expected provenance [doc-1], got %v", sub.SourceIDs)
	}

	// Directed 1-hop CTE traversal with edge-type filter
	directed, err := gs.Traverse(ctx, []string{"auth_service"}, 1,
		graphstore.WithBidirectional(false),
		graphstore.WithEdgeTypes("DEPENDS_ON"),
	)
	if err != nil {
		t.Fatalf("Traverse directed: %v", err)
	}
	if len(directed.Nodes) != 2 || len(directed.Edges) != 1 || directed.Edges[0].Target != "redis_cluster" {
		t.Fatalf("unexpected directed traversal: %+v", directed)
	}

	// Cycle-safe multi-hop traversal
	deep, err := gs.Traverse(ctx, []string{"checkout_api"}, 5, graphstore.WithBidirectional(false))
	if err != nil {
		t.Fatalf("Traverse deep: %v", err)
	}
	if len(deep.Nodes) != 3 {
		t.Fatalf("expected 3 nodes in cycle-safe traversal, got %d", len(deep.Nodes))
	}

	// Delete by DocID
	if err := gs.Delete(ctx, []string{"doc-1"}, graphstore.WithDeleteByDocID(true)); err != nil {
		t.Fatalf("Delete by doc ID: %v", err)
	}
	afterDel, err := gs.Traverse(ctx, []string{"auth_service"}, 2)
	if err != nil {
		t.Fatalf("Traverse after doc delete: %v", err)
	}
	if len(afterDel.Edges) != 0 || len(afterDel.Nodes) != 1 {
		t.Fatalf("expected 0 edges and 1 seed node after doc delete, got %+v", afterDel)
	}

	// Delete node and verify FTS5 index is cleaned
	if err := gs.Delete(ctx, []string{"auth_service"}); err != nil {
		t.Fatalf("Delete node: %v", err)
	}
	remaining, err := gs.SearchNodes(ctx, "Auth", 5)
	if err != nil {
		t.Fatalf("SearchNodes after delete: %v", err)
	}
	for _, n := range remaining {
		if n.ID == "auth_service" {
			t.Fatalf("auth_service should have been deleted from FTS5 and nodes table")
		}
	}
}

func TestSQLiteGraphStore_SemanticSearch(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	embedModel, err := embedding.NewModel(sqliteEmbedProvider{}, "test-embed", nil)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	gs, err := gssqlite.New(db, gssqlite.WithEmbedding(embedModel))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}

	if err := gs.Upsert(ctx, []graphstore.GraphDocument{{
		Nodes: []graphstore.Node{
			{ID: "kubernetes", Label: "Kubernetes", Type: "Platform"},
			{ID: "redis", Label: "Redis", Type: "Cache"},
		},
	}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	nodes, err := gs.SearchNodes(ctx, "k8s", 1)
	if err != nil {
		t.Fatalf("SearchNodes semantic: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != "kubernetes" {
		t.Fatalf("expected kubernetes via loom_vec_cosine, got %#v", nodes)
	}
}
