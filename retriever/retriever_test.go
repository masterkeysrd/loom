package retriever_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/masterkeysrd/loom/decision"
	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/retriever"
	"github.com/masterkeysrd/loom/stream"
	"github.com/masterkeysrd/loom/tool"
	"github.com/masterkeysrd/loom/vectorstore"
	"github.com/masterkeysrd/loom/vectorstore/memory"
)

// deterministicEmbeddingProvider gives predictable vectors for testing
type deterministicEmbeddingProvider struct{}

func (d deterministicEmbeddingProvider) Name() string { return "deterministic" }

func (d deterministicEmbeddingProvider) Embed(ctx context.Context, req *embedding.Request) (*embedding.Response, error) {
	embeddings := make([]embedding.Vector, len(req.Inputs))
	for i, inp := range req.Inputs {
		text := inp.Text
		vec := embedding.Vector{0.1, 0.1, 0.1, 0.1}
		switch text {
		case "apple fruit":
			vec = embedding.Vector{1.0, 0.0, 0.0, 0.0}
		case "apple tree":
			vec = embedding.Vector{0.9, 0.1, 0.0, 0.0}
		case "banana fruit":
			vec = embedding.Vector{0.8, 0.2, 0.0, 0.0}
		case "sports car":
			vec = embedding.Vector{0.0, 0.0, 1.0, 0.0}
		case "query apple":
			vec = embedding.Vector{1.0, 0.0, 0.0, 0.0}
		case "query car":
			vec = embedding.Vector{0.0, 0.0, 1.0, 0.0}
		}
		embeddings[i] = vec
	}
	return &embedding.Response{
		Model:      req.Model,
		Embeddings: embeddings,
	}, nil
}

type testStreamWriter struct {
	mu     sync.Mutex
	events []stream.Event
}

func (w *testStreamWriter) Write(ctx context.Context, data any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if ev, ok := data.(stream.Event); ok {
		w.events = append(w.events, ev)
	}
	return nil
}

func TestVectorStoreRetriever(t *testing.T) {
	ctx := context.Background()

	embedModel, err := embedding.NewModel(deterministicEmbeddingProvider{}, "test-embed", nil)
	if err != nil {
		t.Fatalf("failed to create embedding model: %v", err)
	}

	memStore := memory.New(embedModel)
	docs := []document.Document{
		document.New("apple fruit", map[string]any{"category": "food", "in_stock": true}).WithID("doc-1"),
		document.New("apple tree", map[string]any{"category": "nature", "in_stock": false}).WithID("doc-2"),
		document.New("sports car", map[string]any{"category": "vehicle", "in_stock": true}).WithID("doc-3"),
	}

	if _, err := memStore.Add(ctx, docs); err != nil {
		t.Fatalf("failed to add documents: %v", err)
	}

	// 1. Basic retrieval with default TopK=4
	r := retriever.FromVectorStore(memStore)
	results, err := r.Retrieve(ctx, "query apple")
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].ID != "doc-1" {
		t.Errorf("expected doc-1 as top result, got %s", results[0].ID)
	}

	// 2. Options override: WithTopK, WithMinScore, WithFilter
	resFiltered, err := r.Retrieve(ctx, "query apple",
		retriever.WithTopK(1),
		retriever.WithFilter(map[string]any{"category": "nature"}),
	)
	if err != nil {
		t.Fatalf("Retrieve with filter failed: %v", err)
	}
	if len(resFiltered) != 1 || resFiltered[0].ID != "doc-2" {
		t.Fatalf("expected only doc-2, got %v", resFiltered)
	}

	// 3. MinScore filtering
	resScore, err := r.Retrieve(ctx, "query apple", retriever.WithMinScore(0.5))
	if err != nil {
		t.Fatalf("Retrieve with min score failed: %v", err)
	}
	if len(resScore) != 2 {
		t.Fatalf("expected 2 results above min score 0.5, got %d", len(resScore))
	}

	// 4. Stream event verification
	writer := &testStreamWriter{}
	streamCtx := stream.WithWriter(ctx, writer)
	_, err = r.Retrieve(streamCtx, "query apple", retriever.WithTopK(2))
	if err != nil {
		t.Fatalf("Retrieve with stream failed: %v", err)
	}

	var retrieveEvent *stream.Event
	for i := range writer.events {
		if writer.events[i].Name == "on_retrieve" {
			retrieveEvent = &writer.events[i]
			break
		}
	}
	if retrieveEvent == nil {
		t.Fatalf("expected 'on_retrieve' stream event emitted, got events: %v", writer.events)
	}
	streamDocs, ok := retrieveEvent.Data.([]document.Document)
	if !ok || len(streamDocs) != 2 {
		t.Errorf("expected event data to contain 2 documents, got %v", retrieveEvent.Data)
	}

	// 5. Middleware chaining
	var middlewareCalled bool
	customMW := func(next retriever.Retriever) retriever.Retriever {
		return retriever.RetrieverFunc(func(ctx context.Context, query string, opts ...retriever.Option) ([]document.Document, error) {
			middlewareCalled = true
			return next.Retrieve(ctx, query, opts...)
		})
	}
	rWithMW := r.WithMiddleware(customMW)
	_, err = rWithMW.Retrieve(ctx, "query apple")
	if err != nil {
		t.Fatalf("Retrieve with custom middleware failed: %v", err)
	}
	if !middlewareCalled {
		t.Errorf("expected custom middleware to be invoked")
	}
}

func TestVectorStoreRetriever_Namespace(t *testing.T) {
	ctx := context.Background()
	embedModel, _ := embedding.NewModel(deterministicEmbeddingProvider{}, "test-embed", nil)
	memStore := memory.New(embedModel)

	_, _ = memStore.Add(ctx, []document.Document{
		document.New("apple fruit").WithID("doc-ns"),
	}, vectorstore.WithAddNamespace("ns_special"))

	r := retriever.FromVectorStore(memStore, retriever.WithNamespace("ns_special"))
	results, err := r.Retrieve(ctx, "query apple")
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	if len(results) != 1 || results[0].ID != "doc-ns" {
		t.Fatalf("expected doc-ns from ns_special, got %v", results)
	}
}

func TestMergerRetriever(t *testing.T) {
	ctx := context.Background()

	// Child retriever 1 returns doc-1, doc-2
	r1 := retriever.RetrieverFunc(func(ctx context.Context, query string, opts ...retriever.Option) ([]document.Document, error) {
		return []document.Document{
			document.New("alpha content").WithID("doc-alpha"),
			document.New("beta content").WithID("doc-beta"),
		}, nil
	})

	// Child retriever 2 returns doc-beta, doc-gamma
	r2 := retriever.RetrieverFunc(func(ctx context.Context, query string, opts ...retriever.Option) ([]document.Document, error) {
		return []document.Document{
			document.New("beta content").WithID("doc-beta"),
			document.New("gamma content").WithID("doc-gamma"),
		}, nil
	})

	merger := retriever.NewMerger([]retriever.Retriever{r1, r2}, retriever.WithRRFConstant(60))

	results, err := merger.Retrieve(ctx, "test query", retriever.WithTopK(10))
	if err != nil {
		t.Fatalf("Merger Retrieve failed: %v", err)
	}

	// doc-beta appeared in both retrievers (rank 1 in r1, rank 0 in r2)
	// r1 score for doc-beta: 1 / (60 + 1 + 1) = 1/62 ≈ 0.016129
	// r2 score for doc-beta: 1 / (60 + 0 + 1) = 1/61 ≈ 0.016393
	// total score for doc-beta: ≈ 0.032522
	// doc-alpha: 1 / (60 + 0 + 1) = 1/61 ≈ 0.016393
	// doc-gamma: 1 / (60 + 1 + 1) = 1/62 ≈ 0.016129
	// So doc-beta should be ranked #1!
	if len(results) != 3 {
		t.Fatalf("expected 3 merged documents, got %d", len(results))
	}
	if results[0].ID != "doc-beta" {
		t.Errorf("expected doc-beta to be ranked top by RRF, got %s (score: %f)", results[0].ID, results[0].Score)
	}
	if results[1].ID != "doc-alpha" {
		t.Errorf("expected doc-alpha to be ranked second, got %s (score: %f)", results[1].ID, results[1].Score)
	}
	if results[2].ID != "doc-gamma" {
		t.Errorf("expected doc-gamma to be ranked third, got %s (score: %f)", results[2].ID, results[2].Score)
	}

	// Test TopK truncation
	top1, err := merger.Retrieve(ctx, "test query", retriever.WithTopK(1))
	if err != nil {
		t.Fatalf("TopK retrieve failed: %v", err)
	}
	if len(top1) != 1 || top1[0].ID != "doc-beta" {
		t.Fatalf("expected only 1 top result (doc-beta), got %v", top1)
	}

	// Test deduplication without ID (falling back to Content)
	rNoID1 := retriever.RetrieverFunc(func(ctx context.Context, query string, opts ...retriever.Option) ([]document.Document, error) {
		return []document.Document{
			document.New("same text"),
		}, nil
	})
	rNoID2 := retriever.RetrieverFunc(func(ctx context.Context, query string, opts ...retriever.Option) ([]document.Document, error) {
		return []document.Document{
			document.New("same text"),
		}, nil
	})
	mNoID := retriever.NewMerger([]retriever.Retriever{rNoID1, rNoID2})
	resNoID, err := mNoID.Retrieve(ctx, "query")
	if err != nil {
		t.Fatalf("Retrieve without ID failed: %v", err)
	}
	if len(resNoID) != 1 {
		t.Fatalf("expected 1 deduplicated document by content, got %d", len(resNoID))
	}
}

// mockDecisionProvider evaluates relevance by keyword matching
type mockDecisionProvider struct{}

func (m mockDecisionProvider) Name() string { return "mock-decision" }

func (m mockDecisionProvider) Evaluate(ctx context.Context, req *decision.Request) (*decision.Response, error) {
	answers := make(map[string]decision.Answer)
	for _, q := range req.Questions {
		var text string
		if s, ok := req.Input.State.(string); ok {
			text = s
		}
		parts := strings.Split(text, "\n\nDocument:\n")
		docText := text
		if len(parts) > 1 {
			docText = parts[1]
		}

		prob := 0.2
		conf := 0.6
		if strings.Contains(docText, "quantum computing") {
			prob = 0.95
			conf = 0.99
		} else if strings.Contains(docText, "machine learning") {
			prob = 0.75
			conf = 0.85
		}

		answers[q.Name] = decision.Answer{
			Name:        q.Name,
			Type:        q.Type,
			Probability: prob,
			Confidence:  conf,
		}
	}
	return &decision.Response{
		Model:   req.Model,
		Answers: answers,
	}, nil
}

func TestDecisionReranker(t *testing.T) {
	ctx := context.Background()

	base := retriever.RetrieverFunc(func(ctx context.Context, query string, opts ...retriever.Option) ([]document.Document, error) {
		return []document.Document{
			document.New("general physics concepts").WithID("doc-low"),
			document.New("advanced quantum computing algorithms").WithID("doc-high"),
			document.New("practical machine learning pipelines").WithID("doc-mid"),
		}, nil
	})

	decModel, err := decision.NewModel(mockDecisionProvider{}, "decision-model", nil)
	if err != nil {
		t.Fatalf("failed to create decision model: %v", err)
	}

	reranker := retriever.NewDecisionReranker(base, decModel, "Is the document relevant to the query?", retriever.WithThreshold(0.5))

	results, err := reranker.Retrieve(ctx, "quantum computing algorithms", retriever.WithTopK(10))
	if err != nil {
		t.Fatalf("DecisionReranker Retrieve failed: %v", err)
	}

	// doc-low (0.2) should be filtered out by threshold (0.5)
	// doc-high (0.95) should be ranked 1st
	// doc-mid (0.75) should be ranked 2nd
	if len(results) != 2 {
		t.Fatalf("expected 2 documents passing threshold 0.5, got %d", len(results))
	}
	if results[0].ID != "doc-high" {
		t.Errorf("expected doc-high at rank 0, got %s", results[0].ID)
	}
	if results[1].ID != "doc-mid" {
		t.Errorf("expected doc-mid at rank 1, got %s", results[1].ID)
	}
	if results[0].Score < 0.9 {
		t.Errorf("expected score ~0.95, got %f", results[0].Score)
	}
	if conf, ok := results[0].Metadata["decision_confidence"].(float64); !ok || conf < 0.9 {
		t.Errorf("expected decision_confidence metadata, got %v", results[0].Metadata)
	}
}

func TestNewTool(t *testing.T) {
	ctx := context.Background()

	embedModel, _ := embedding.NewModel(deterministicEmbeddingProvider{}, "test-embed", nil)
	memStore := memory.New(embedModel)
	_, _ = memStore.Add(ctx, []document.Document{
		document.New("apple fruit").WithID("doc-1"),
		document.New("banana fruit").WithID("doc-2"),
		document.New("sports car").WithID("doc-3"),
	})

	ret := retriever.FromVectorStore(memStore)

	tl, err := retriever.NewTool("search_knowledge", "Search knowledge base", ret)
	if err != nil {
		t.Fatalf("NewTool failed: %v", err)
	}

	if tl.Definition.Name != "search_knowledge" {
		t.Errorf("expected tool name 'search_knowledge', got %s", tl.Definition.Name)
	}

	// Execute via tool.Container.Call
	container := tool.NewContainer(tl)
	res, err := container.Call(ctx, &message.ToolCall{
		ID:   "call-1",
		Name: "search_knowledge",
		Args: map[string]any{"query": "query apple", "top_k": 2},
	})
	if err != nil {
		t.Fatalf("Tool Call failed: %v", err)
	}

	if res.IsError {
		t.Fatalf("Tool returned functional error: %s", res.Content.Text())
	}

	docs, ok := res.StructuredContent.([]document.Document)
	if !ok {
		t.Fatalf("expected StructuredContent to be []document.Document, got %T", res.StructuredContent)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents returned by tool, got %d", len(docs))
	}
	if docs[0].ID != "doc-1" {
		t.Errorf("expected doc-1 as first result, got %s", docs[0].ID)
	}
}
