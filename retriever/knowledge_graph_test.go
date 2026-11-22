package retriever_test

import (
	"context"
	"strings"
	"testing"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/graphstore"
	gsmemory "github.com/masterkeysrd/loom/graphstore/memory"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/retriever"
	vsmemory "github.com/masterkeysrd/loom/vectorstore/memory"
)

type kgMockLLMProvider struct {
	response string
}

func (m kgMockLLMProvider) Name() string                            { return "kg-mock-llm" }
func (m kgMockLLMProvider) ListProfiles() []profile.Profile         { return nil }
func (m kgMockLLMProvider) GetProfile(string) (profile.Profile, bool) { return profile.Profile{}, false }
func (m kgMockLLMProvider) SearchProfiles(string) []profile.Profile { return nil }
func (m kgMockLLMProvider) OverrideProfile(string, profile.Profile) {}
func (m kgMockLLMProvider) Stream(_ context.Context, _ *llm.Request) (llm.StreamResponse, error) {
	return func(yield func(message.AssistantChunk, error) bool) {
		yield(message.AssistantChunk{
			Content: []message.Block{&message.TextBlock{Text: m.response}},
			Done:    true,
		}, nil)
	}, nil
}

func TestKnowledgeGraphRetriever_TriplesProvenanceHybridAndLLMExtraction(t *testing.T) {
	ctx := context.Background()

	// Seed a vector store for provenance hydration and hybrid RRF.
	embedModel, err := embedding.NewModel(graphTestEmbedProvider{}, "graph-embed", nil)
	if err != nil {
		t.Fatalf("NewModel embed: %v", err)
	}
	vs := vsmemory.New(embedModel)
	chunks := []document.Document{
		document.New("Checkout API delegates authentication to Auth Service.").WithID("chunk-1"),
		document.New("Auth Service stores sessions in Redis Cluster.").WithID("chunk-2"),
	}
	if _, err := vs.Add(ctx, chunks); err != nil {
		t.Fatalf("vs.Add: %v", err)
	}

	// Seed a knowledge graph store.
	gs := gsmemory.New()
	if err := gs.Upsert(ctx, []graphstore.GraphDocument{
		{
			Source: chunks[0],
			Nodes: []graphstore.Node{
				{ID: "checkout_api", Label: "Checkout API", Type: "Service"},
				{ID: "auth_service", Label: "Auth Service", Type: "Service"},
			},
			Edges: []graphstore.Edge{
				{Source: "checkout_api", Target: "auth_service", Type: "CALLS", DocID: "chunk-1"},
			},
		},
		{
			Source: chunks[1],
			Nodes: []graphstore.Node{
				{ID: "auth_service", Label: "Auth Service", Type: "Service"},
				{ID: "redis_cluster", Label: "Redis Cluster", Type: "Infrastructure"},
			},
			Edges: []graphstore.Edge{
				{Source: "auth_service", Target: "redis_cluster", Type: "DEPENDS_ON", DocID: "chunk-2"},
			},
		},
	}); err != nil {
		t.Fatalf("gs.Upsert: %v", err)
	}

	// 1. RenderTriples (default lexical grounding, 2-hop)
	kgTriples := retriever.FromGraphStore(gs,
		retriever.WithGraphStoreMaxDepth(2),
		retriever.WithGraphStoreSeedK(2),
	)
	docs, err := kgTriples.Retrieve(ctx, "Auth Service", retriever.WithTopK(4))
	if err != nil {
		t.Fatalf("Retrieve RenderTriples: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least 1 neighborhood doc")
	}
	if docs[0].Metadata["source_type"] != "knowledge_graph" {
		t.Fatalf("unexpected source_type: %v", docs[0].Metadata["source_type"])
	}
	if !strings.Contains(docs[0].Content, "Auth Service (Service) -[DEPENDS_ON]-> Redis Cluster (Infrastructure)") {
		t.Fatalf("expected DEPENDS_ON triple in output:\n%s", docs[0].Content)
	}
	if !strings.Contains(docs[0].Content, "Checkout API (Service) -[CALLS]-> Auth Service (Service)") {
		t.Fatalf("expected CALLS triple in output:\n%s", docs[0].Content)
	}

	// 2. RenderHybrid with provenance hydration + LLM query entity extractor
	entityModel, err := llm.NewModel(kgMockLLMProvider{response: `{"entities":["Checkout API"]}`}, "mock-llm", nil)
	if err != nil {
		t.Fatalf("NewModel llm: %v", err)
	}
	kgHybrid := retriever.FromGraphStore(gs,
		retriever.WithGraphStoreMaxDepth(2),
		retriever.WithGraphStoreSeedK(2),
		retriever.WithGraphStoreRenderMode(retriever.RenderHybrid),
		retriever.WithGraphStoreDocumentStore(vs),
		retriever.WithQueryEntityExtractor(entityModel),
	)
	hybridDocs, err := kgHybrid.Retrieve(ctx, "Which cache breaks if checkout has an outage?", retriever.WithTopK(5))
	if err != nil {
		t.Fatalf("Retrieve RenderHybrid: %v", err)
	}
	var sawKG, sawProv bool
	for _, d := range hybridDocs {
		if d.Metadata["source_type"] == "knowledge_graph" {
			sawKG = true
			if !strings.Contains(d.Content, "Redis Cluster") {
				t.Fatalf("expected 2-hop Redis Cluster reachable from Checkout API:\n%s", d.Content)
			}
		}
		if d.Metadata["source_type"] == "knowledge_graph_provenance" {
			sawProv = true
		}
	}
	if !sawKG || !sawProv {
		t.Fatalf("expected both knowledge_graph and provenance docs in RenderHybrid, got %#v", hybridDocs)
	}

	// 3. Composes seamlessly with retriever.NewMerger (RRF)
	merger := retriever.NewMerger([]retriever.Retriever{
		retriever.FromVectorStore(vs),
		retriever.NewBM25(chunks),
		kgTriples,
	})
	merged, err := merger.Retrieve(ctx, "Auth Service Redis", retriever.WithTopK(4))
	if err != nil || len(merged) == 0 {
		t.Fatalf("NewMerger with KnowledgeGraphRetriever failed: %v (len=%d)", err, len(merged))
	}
}
