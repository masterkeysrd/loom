package retriever

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"go.opentelemetry.io/otel/trace"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/graphstore"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/telemetry"
	"github.com/masterkeysrd/loom/vectorstore"
)

var _ Retriever = (*KnowledgeGraphRetriever)(nil)

// RenderMode controls how [KnowledgeGraphRetriever] formats traversed subgraphs
// into [document.Document] results.
type RenderMode int

const (
	// RenderTriples formats compact structured entity-relationship neighborhood documents.
	RenderTriples RenderMode = iota
	// RenderProvenance hydrates and returns source prose documents referenced by Subgraph.SourceIDs.
	RenderProvenance
	// RenderHybrid returns both structured neighborhood documents and hydrated provenance documents.
	RenderHybrid
)

// GraphStoreOption configures a [KnowledgeGraphRetriever].
type GraphStoreOption func(*KnowledgeGraphRetriever)

// WithGraphStoreMaxDepth sets the maximum N-hop traversal depth (default: 2).
func WithGraphStoreMaxDepth(depth int) GraphStoreOption {
	return func(r *KnowledgeGraphRetriever) {
		if depth >= 0 {
			r.maxDepth = depth
		}
	}
}

// WithGraphStoreSeedK sets the maximum number of seed entities grounded per query (default: 5).
func WithGraphStoreSeedK(k int) GraphStoreOption {
	return func(r *KnowledgeGraphRetriever) {
		if k > 0 {
			r.seedK = k
		}
	}
}

// WithGraphStoreMaxEdges caps the total edges returned during traversal (default: 50).
func WithGraphStoreMaxEdges(n int) GraphStoreOption {
	return func(r *KnowledgeGraphRetriever) {
		if n > 0 {
			r.maxEdges = n
		}
	}
}

// WithGraphStoreMaxNodes caps the total nodes returned during traversal (default: 100).
func WithGraphStoreMaxNodes(n int) GraphStoreOption {
	return func(r *KnowledgeGraphRetriever) {
		if n > 0 {
			r.maxNodes = n
		}
	}
}

// WithGraphStoreBidirectional controls whether traversal follows incoming as well as outgoing edges (default: true).
func WithGraphStoreBidirectional(enabled bool) GraphStoreOption {
	return func(r *KnowledgeGraphRetriever) {
		r.bidirectional = enabled
	}
}

// WithGraphStoreEdgeTypes restricts traversal to specific relationship predicates.
func WithGraphStoreEdgeTypes(types ...string) GraphStoreOption {
	return func(r *KnowledgeGraphRetriever) {
		r.edgeTypes = append([]string(nil), types...)
	}
}

// WithGraphStoreNodeTypes restricts seed entity grounding to specific entity classes.
func WithGraphStoreNodeTypes(types ...string) GraphStoreOption {
	return func(r *KnowledgeGraphRetriever) {
		r.nodeTypes = append([]string(nil), types...)
	}
}

// WithGraphStoreRenderMode selects [RenderTriples] (default), [RenderProvenance], or [RenderHybrid].
func WithGraphStoreRenderMode(mode RenderMode) GraphStoreOption {
	return func(r *KnowledgeGraphRetriever) {
		r.renderMode = mode
	}
}

// WithGraphStoreDocumentStore attaches a [vectorstore.VectorStore] used to hydrate
// source passages referenced by [graphstore.Subgraph.SourceIDs] in [RenderProvenance] and [RenderHybrid] modes.
func WithGraphStoreDocumentStore(vs vectorstore.VectorStore) GraphStoreOption {
	return func(r *KnowledgeGraphRetriever) {
		r.docStore = vs
	}
}

// WithQueryEntityExtractor uses model (via [llm.Model.WithStructuredOutput]) to extract
// named entities from complex queries before grounding them in the [graphstore.GraphStore].
func WithQueryEntityExtractor(model *llm.Model) GraphStoreOption {
	return func(r *KnowledgeGraphRetriever) {
		if model != nil {
			r.entityModel = model.WithStructuredOutput(queryEntitiesSchema())
		}
	}
}

// WithGraphStoreDefaultOptions appends default [Option] values applied on every Retrieve call.
func WithGraphStoreDefaultOptions(opts ...Option) GraphStoreOption {
	return func(r *KnowledgeGraphRetriever) {
		r.defaultOpts = append(r.defaultOpts, opts...)
	}
}

// KnowledgeGraphRetriever adapts a [graphstore.GraphStore] (and optional provenance store)
// to the [Retriever] interface for entity-grounded GraphRAG.
type KnowledgeGraphRetriever struct {
	gs            graphstore.GraphStore
	docStore      vectorstore.VectorStore
	entityModel   *llm.Model
	maxDepth      int
	seedK         int
	maxEdges      int
	maxNodes      int
	bidirectional bool
	edgeTypes     []string
	nodeTypes     []string
	renderMode    RenderMode
	defaultOpts   []Option
	middleware    []Middleware
}

// FromGraphStore creates a [KnowledgeGraphRetriever] wrapped with
// [TelemetryMiddleware]("knowledge_graph") by default.
func FromGraphStore(gs graphstore.GraphStore, opts ...GraphStoreOption) *KnowledgeGraphRetriever {
	r := &KnowledgeGraphRetriever{
		gs:            gs,
		maxDepth:      2,
		seedK:         5,
		maxEdges:      50,
		maxNodes:      100,
		bidirectional: true,
		renderMode:    RenderTriples,
		middleware:    []Middleware{TelemetryMiddleware("knowledge_graph")},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

// WithMiddleware returns a clone of the retriever with additional middleware appended.
func (r *KnowledgeGraphRetriever) WithMiddleware(mws ...Middleware) *KnowledgeGraphRetriever {
	clone := *r
	clone.edgeTypes = slices.Clone(r.edgeTypes)
	clone.nodeTypes = slices.Clone(r.nodeTypes)
	clone.defaultOpts = slices.Clone(r.defaultOpts)
	clone.middleware = append(slices.Clone(r.middleware), mws...)
	return &clone
}

// Retrieve grounds query entities in the graph store, traverses their N-hop neighborhood,
// and renders structured triples and/or provenance documents.
func (r *KnowledgeGraphRetriever) Retrieve(ctx context.Context, query string, opts ...Option) ([]document.Document, error) {
	if r == nil || r.gs == nil {
		return nil, errors.New("retriever: graph store is nil")
	}

	allOpts := append(slices.Clone(r.defaultOpts), opts...)
	base := RetrieverFunc(func(ctx context.Context, q string, callOpts ...Option) ([]document.Document, error) {
		cfg := ApplyOptions(callOpts...)
		if cfg.TopK <= 0 {
			return nil, nil
		}

		span := trace.SpanFromContext(ctx)
		span.SetAttributes(telemetry.KeyLoomRetrieverMaxDepth.Int(r.maxDepth))

		seeds, err := r.groundSeeds(ctx, q, cfg)
		if err != nil {
			return nil, err
		}
		if len(seeds) == 0 {
			return nil, nil
		}

		seedIDs := make([]string, len(seeds))
		for i, n := range seeds {
			seedIDs[i] = n.ID
		}

		var travOpts []graphstore.TraverseOption
		if cfg.Namespace != "" {
			travOpts = append(travOpts, graphstore.WithTraverseNamespace(cfg.Namespace))
		}
		if len(r.edgeTypes) > 0 {
			travOpts = append(travOpts, graphstore.WithEdgeTypes(r.edgeTypes...))
		}
		travOpts = append(travOpts,
			graphstore.WithBidirectional(r.bidirectional),
			graphstore.WithMaxEdges(r.maxEdges),
			graphstore.WithMaxNodes(r.maxNodes),
		)

		sub, err := r.gs.Traverse(ctx, seedIDs, r.maxDepth, travOpts...)
		if err != nil {
			return nil, err
		}

		var tripleDocs []document.Document
		if r.renderMode == RenderTriples || r.renderMode == RenderHybrid {
			tripleDocs = renderNeighborhoodDocs(seeds, sub)
		}

		var provDocs []document.Document
		if (r.renderMode == RenderProvenance || r.renderMode == RenderHybrid) && r.docStore != nil && len(sub.SourceIDs) > 0 {
			var getOpts []vectorstore.SearchOption
			if cfg.Namespace != "" {
				getOpts = append(getOpts, vectorstore.WithNamespace(cfg.Namespace))
			}
			fetched, err := r.docStore.Get(ctx, sub.SourceIDs, getOpts...)
			if err != nil {
				return nil, err
			}
			for i, doc := range fetched {
				d := doc.WithMetadata("source_type", "knowledge_graph_provenance")
				if d.Score == 0 {
					d.Score = 1.0 / float32(len(tripleDocs)+i+1)
				}
				provDocs = append(provDocs, d)
			}
		}

		var results []document.Document
		switch r.renderMode {
		case RenderProvenance:
			results = provDocs
			if len(results) == 0 {
				results = renderNeighborhoodDocs(seeds, sub)
			}
		case RenderHybrid:
			results = append(tripleDocs, provDocs...)
		default:
			results = tripleDocs
		}

		if cfg.HasMinScore {
			filtered := results[:0]
			for _, d := range results {
				if d.Score >= cfg.MinScore {
					filtered = append(filtered, d)
				}
			}
			results = filtered
		}

		if len(results) > cfg.TopK {
			results = results[:cfg.TopK]
		}
		return results, nil
	})

	chained := Chain(base, r.middleware...)
	return chained.Retrieve(ctx, query, allOpts...)
}

func (r *KnowledgeGraphRetriever) groundSeeds(ctx context.Context, query string, cfg Config) ([]graphstore.Node, error) {
	var searchOpts []graphstore.SearchOption
	if cfg.Namespace != "" {
		searchOpts = append(searchOpts, graphstore.WithNamespace(cfg.Namespace))
	}
	if len(r.nodeTypes) > 0 {
		searchOpts = append(searchOpts, graphstore.WithNodeTypes(r.nodeTypes...))
	}
	if len(cfg.Filter) > 0 {
		searchOpts = append(searchOpts, graphstore.WithFilter(cfg.Filter))
	}

	if r.entityModel != nil {
		entities, err := r.extractQueryEntities(ctx, query)
		if err != nil {
			return nil, err
		}
		if len(entities) > 0 {
			seen := make(map[string]struct{}, r.seedK)
			var seeds []graphstore.Node
			for _, ent := range entities {
				nodes, err := r.gs.SearchNodes(ctx, ent, r.seedK, searchOpts...)
				if err != nil {
					return nil, err
				}
				for _, n := range nodes {
					if _, dup := seen[n.ID]; dup {
						continue
					}
					seen[n.ID] = struct{}{}
					seeds = append(seeds, n)
					if len(seeds) >= r.seedK {
						return seeds, nil
					}
				}
			}
			if len(seeds) > 0 {
				return seeds, nil
			}
		}
	}

	return r.gs.SearchNodes(ctx, query, r.seedK, searchOpts...)
}

func (r *KnowledgeGraphRetriever) extractQueryEntities(ctx context.Context, query string) ([]string, error) {
	reply, err := r.entityModel.Invoke(ctx, []message.Message{
		message.NewSystemText("Extract the key entity names or concepts mentioned in the user's query. Return only concise entity names."),
		message.NewUserText(query),
	})
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(reply.Content.Text())
	if strings.HasPrefix(raw, "```") {
		if nl := strings.IndexByte(raw, '\n'); nl >= 0 {
			raw = raw[nl+1:]
		}
		raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "```"))
	}
	var payload struct {
		Entities []string `json:"entities"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("retriever: decode extracted query entities: %w", err)
	}
	var out []string
	for _, e := range payload.Entities {
		if s := strings.TrimSpace(e); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func queryEntitiesSchema() *jsonschema.Schema {
	noExtra := &jsonschema.Schema{Not: &jsonschema.Schema{}}
	return &jsonschema.Schema{
		Title:                "QueryEntities",
		Description:          "Named entities and key concepts referenced in a user query",
		Type:                 "object",
		AdditionalProperties: noExtra,
		Required:             []string{"entities"},
		Properties: map[string]*jsonschema.Schema{
			"entities": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type: "string",
				},
			},
		},
	}
}

func renderNeighborhoodDocs(seeds []graphstore.Node, sub graphstore.Subgraph) []document.Document {
	nodeByID := make(map[string]graphstore.Node, len(sub.Nodes))
	for _, n := range sub.Nodes {
		nodeByID[n.ID] = n
	}

	formatNode := func(id string) string {
		if n, ok := nodeByID[id]; ok {
			label := n.Label
			if label == "" {
				label = n.ID
			}
			if n.Type != "" {
				return fmt.Sprintf("%s (%s)", label, n.Type)
			}
			return label
		}
		return id
	}

	// Build a consolidated neighborhood document per grounded seed entity.
	// Direct edges touching the seed are listed first, followed by multi-hop edges reachable in the subgraph.
	docs := make([]document.Document, 0, len(seeds))
	for rank, seed := range seeds {
		var direct, indirect []graphstore.Edge
		srcSet := make(map[string]struct{})

		for _, e := range sub.Edges {
			if e.Source == seed.ID || e.Target == seed.ID {
				direct = append(direct, e)
			} else {
				indirect = append(indirect, e)
			}
			if e.DocID != "" {
				srcSet[e.DocID] = struct{}{}
			}
		}

		var sb strings.Builder
		sb.WriteString("Entity: ")
		sb.WriteString(formatNode(seed.ID))
		if len(seed.Properties) > 0 {
			sb.WriteString("\nProperties:")
			for _, k := range slices.Sorted(maps.Keys(seed.Properties)) {
				fmt.Fprintf(&sb, "\n- %s: %v", k, seed.Properties[k])
			}
		}
		if len(direct) > 0 || len(indirect) > 0 {
			sb.WriteString("\nRelationships:")
			for _, e := range direct {
				fmt.Fprintf(&sb, "\n- %s -[%s]-> %s", formatNode(e.Source), e.Type, formatNode(e.Target))
			}
			if rank == 0 {
				for _, e := range indirect {
					fmt.Fprintf(&sb, "\n- %s -[%s]-> %s", formatNode(e.Source), e.Type, formatNode(e.Target))
				}
			}
		}

		doc := document.New(sb.String()).
			WithID("kg:"+seed.ID).
			WithScore(1.0/float32(rank+1)).
			WithMetadata("source_type", "knowledge_graph").
			WithMetadata("entity_id", seed.ID)
		if seed.Type != "" {
			doc = doc.WithMetadata("entity_type", seed.Type)
		}
		if len(srcSet) > 0 {
			doc = doc.WithMetadata("source_doc_ids", slices.Sorted(maps.Keys(srcSet)))
		}
		docs = append(docs, doc)
	}

	return docs
}
