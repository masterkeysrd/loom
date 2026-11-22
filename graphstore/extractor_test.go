package graphstore_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/graphstore"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
)

type mockExtractProvider struct {
	mu          sync.Mutex
	requests    []*llm.Request
	respondFunc func(req *llm.Request) string
}

func (m *mockExtractProvider) Name() string                      { return "mock-extract" }
func (m *mockExtractProvider) ListProfiles() []profile.Profile   { return nil }
func (m *mockExtractProvider) GetProfile(string) (profile.Profile, bool) {
	return profile.Profile{}, false
}
func (m *mockExtractProvider) SearchProfiles(string) []profile.Profile { return nil }
func (m *mockExtractProvider) OverrideProfile(string, profile.Profile) {}

func (m *mockExtractProvider) Stream(_ context.Context, req *llm.Request) (llm.StreamResponse, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	fn := m.respondFunc
	m.mu.Unlock()

	text := fn(req)
	return func(yield func(message.AssistantChunk, error) bool) {
		yield(message.AssistantChunk{
			Content: []message.Block{&message.TextBlock{Text: text}},
			Done:    true,
		}, nil)
	}, nil
}

func TestExtractor_SchemaEnumsTripleValidationAndConcurrency(t *testing.T) {
	prov := &mockExtractProvider{
		respondFunc: func(req *llm.Request) string {
			userText := req.Messages[len(req.Messages)-1].GetContent().Text()
			if strings.Contains(userText, "Checkout") {
				return `{
					"nodes": [
						{"id": "Checkout API", "label": "Checkout API", "type": "Service"},
						{"id": "Auth Service", "label": "Auth Service", "type": "Service"},
						{"id": "Evil Node", "label": "Evil Node", "type": "ForbiddenType"}
					],
					"edges": [
						{"source": "Checkout API", "target": "Auth Service", "type": "calls"},
						{"source": "Checkout API", "target": "Evil Node", "type": "CALLS"},
						{"source": "Auth Service", "target": "Checkout API", "type": "DEPENDS_ON"}
					]
				}`
			}
			return "```json\n" + `{
				"nodes": [
					{"id": "Auth Service", "label": "Auth Service", "type": "Service"},
					{"id": "Redis", "label": "Redis Cluster", "type": "Infrastructure"}
				],
				"edges": [
					{"source": "Auth Service", "target": "Redis", "type": "DEPENDS_ON"}
				]
			}` + "\n```"
		},
	}

	model, err := llm.NewModel(prov, "test-model", nil)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}

	extractor, err := graphstore.NewExtractor(
		model,
		graphstore.WithAllowedNodeTypes("Service", "Infrastructure"),
		graphstore.WithAllowedEdgeTypes("CALLS", "DEPENDS_ON"),
		graphstore.WithAllowedTriples(
			graphstore.TripleRule{SourceType: "Service", EdgeType: "CALLS", TargetType: "Service"},
			graphstore.TripleRule{SourceType: "Service", EdgeType: "DEPENDS_ON", TargetType: "Infrastructure"},
		),
		graphstore.WithConcurrency(2),
	)
	if err != nil {
		t.Fatalf("NewExtractor: %v", err)
	}

	// Verify compiled JSON Schema has enum constraints.
	schema := extractor.Schema()
	nodeTypeEnum := schema.Properties["nodes"].Items.Properties["type"].Enum
	edgeTypeEnum := schema.Properties["edges"].Items.Properties["type"].Enum
	if len(nodeTypeEnum) != 2 || len(edgeTypeEnum) != 2 {
		t.Fatalf("expected 2 node enums and 2 edge enums, got %v / %v", nodeTypeEnum, edgeTypeEnum)
	}

	chunks := []document.Document{
		document.New("Checkout API calls Auth Service.").WithID("chunk-1"),
		document.New("Auth Service depends on Redis Cluster.").WithID("chunk-2"),
	}

	graphDocs, err := extractor.Extract(context.Background(), chunks)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(graphDocs) != 2 {
		t.Fatalf("expected 2 graph docs, got %d", len(graphDocs))
	}

	// Chunk 1: ForbiddenType node dropped; Service-DEPENDS_ON->Service edge dropped by TripleRule.
	gd1 := graphDocs[0]
	if gd1.Source.ID != "chunk-1" || len(gd1.Nodes) != 2 || len(gd1.Edges) != 1 {
		t.Fatalf("unexpected chunk-1 extraction: %+v", gd1)
	}
	if gd1.Edges[0].Source != "checkout_api" || gd1.Edges[0].Target != "auth_service" || gd1.Edges[0].Type != "CALLS" || gd1.Edges[0].DocID != "chunk-1" {
		t.Fatalf("unexpected chunk-1 edge: %+v", gd1.Edges[0])
	}

	// Chunk 2: Markdown fence stripped, valid DEPENDS_ON edge preserved with provenance.
	gd2 := graphDocs[1]
	if gd2.Source.ID != "chunk-2" || len(gd2.Nodes) != 2 || len(gd2.Edges) != 1 {
		t.Fatalf("unexpected chunk-2 extraction: %+v", gd2)
	}
	if gd2.Edges[0].Target != "redis" || gd2.Edges[0].DocID != "chunk-2" {
		t.Fatalf("unexpected chunk-2 edge: %+v", gd2.Edges[0])
	}
}
