package retriever

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/masterkeysrd/loom/decision"
	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/tool"
	"github.com/masterkeysrd/loom/vectorstore"
)

var (
	_ Retriever = (*VectorStoreRetriever)(nil)
	_ Retriever = (*MergerRetriever)(nil)
	_ Retriever = (*DecisionReranker)(nil)
)

func sortDocumentsByScore(docs []document.Document) {
	slices.SortFunc(docs, func(a, b document.Document) int {
		if a.Score > b.Score {
			return -1
		}
		if a.Score < b.Score {
			return 1
		}
		if a.ID != b.ID {
			if a.ID < b.ID {
				return -1
			}
			return 1
		}
		if a.Content < b.Content {
			return -1
		}
		if a.Content > b.Content {
			return 1
		}
		return 0
	})
}

// -------------------------------------------------------------------------
// 1. VectorStoreRetriever
// -------------------------------------------------------------------------

// VectorStoreRetriever adapts a [vectorstore.VectorStore] to the [Retriever] interface.
type VectorStoreRetriever struct {
	vs          vectorstore.VectorStore
	defaultOpts []Option
	middleware  []Middleware
}

// FromVectorStore creates a new [VectorStoreRetriever] wrapped with [TelemetryMiddleware] by default.
func FromVectorStore(vs vectorstore.VectorStore, defaultOpts ...Option) *VectorStoreRetriever {
	return &VectorStoreRetriever{
		vs:          vs,
		defaultOpts: defaultOpts,
		middleware:  []Middleware{TelemetryMiddleware("vectorstore")},
	}
}

// WithMiddleware returns a clone of the retriever with additional middleware appended.
func (r *VectorStoreRetriever) WithMiddleware(mws ...Middleware) *VectorStoreRetriever {
	clone := *r
	clone.middleware = append(slices.Clone(r.middleware), mws...)
	return &clone
}

// Retrieve queries the underlying vector store, mapping retrieval options to vector search options.
func (r *VectorStoreRetriever) Retrieve(ctx context.Context, query string, opts ...Option) ([]document.Document, error) {
	if r == nil || r.vs == nil {
		return nil, errors.New("retriever: vector store is nil")
	}

	allOpts := append(slices.Clone(r.defaultOpts), opts...)
	base := RetrieverFunc(func(ctx context.Context, q string, callOpts ...Option) ([]document.Document, error) {
		cfg := ApplyOptions(callOpts...)

		var searchOpts []vectorstore.SearchOption
		if cfg.Namespace != "" {
			searchOpts = append(searchOpts, vectorstore.WithNamespace(cfg.Namespace))
		}
		if cfg.HasMinScore {
			searchOpts = append(searchOpts, vectorstore.WithMinScore(cfg.MinScore))
		}
		if len(cfg.Filter) > 0 {
			searchOpts = append(searchOpts, vectorstore.WithFilter(cfg.Filter))
		}

		return r.vs.Search(ctx, q, cfg.TopK, searchOpts...)
	})

	chained := Chain(base, r.middleware...)
	return chained.Retrieve(ctx, query, allOpts...)
}

// -------------------------------------------------------------------------
// 2. MergerRetriever (Reciprocal Rank Fusion - RRF)
// -------------------------------------------------------------------------

// MergerOption configures a [MergerRetriever].
type MergerOption func(*MergerRetriever)

// WithRRFConstant sets the RRF smoothing constant k (defaults to 60).
func WithRRFConstant(k int) MergerOption {
	return func(m *MergerRetriever) {
		if k > 0 {
			m.k = k
		}
	}
}

// MergerRetriever merges results from multiple child retrievers using Reciprocal Rank Fusion (RRF).
type MergerRetriever struct {
	retrievers []Retriever
	k          int
}

// NewMerger constructs a [MergerRetriever] over the provided child retrievers.
func NewMerger(retrievers []Retriever, opts ...MergerOption) *MergerRetriever {
	m := &MergerRetriever{
		retrievers: retrievers,
		k:          60,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
}

type mergedEntry struct {
	doc   document.Document
	score float64
}

// Retrieve calls each child retriever, fuses rankings with RRF, deduplicates by ID (or Content),
// filters by MinScore, sorts by score descending, and truncates to TopK.
func (m *MergerRetriever) Retrieve(ctx context.Context, query string, opts ...Option) ([]document.Document, error) {
	cfg := ApplyOptions(opts...)

	docMap := make(map[string]*mergedEntry)
	var keyOrder []string

	for _, r := range m.retrievers {
		if r == nil {
			continue
		}
		docs, err := r.Retrieve(ctx, query, opts...)
		if err != nil {
			return nil, err
		}

		for rank, doc := range docs {
			key := doc.ID
			if key == "" {
				key = doc.Content
			}

			// RRF score formula: 1 / (k + rank_i + 1) where rank is 0-indexed
			rrfScore := 1.0 / float64(m.k+rank+1)

			if entry, exists := docMap[key]; exists {
				entry.score += rrfScore
			} else {
				docMap[key] = &mergedEntry{
					doc:   doc,
					score: rrfScore,
				}
				keyOrder = append(keyOrder, key)
			}
		}
	}

	var results []document.Document
	for _, key := range keyOrder {
		entry := docMap[key]
		score := float32(entry.score)

		if cfg.HasMinScore && score < cfg.MinScore {
			continue
		}

		results = append(results, entry.doc.WithScore(score))
	}

	sortDocumentsByScore(results)

	if len(results) > cfg.TopK {
		results = results[:cfg.TopK]
	}

	return results, nil
}

// -------------------------------------------------------------------------
// 3. DecisionReranker
// -------------------------------------------------------------------------

// RerankerOption configures a [DecisionReranker].
type RerankerOption func(*DecisionReranker)

// WithThreshold sets the minimum decision probability threshold for returned documents.
func WithThreshold(threshold float64) RerankerOption {
	return func(r *DecisionReranker) {
		r.threshold = threshold
	}
}

// DecisionReranker evaluates retrieved documents using a probabilistic decision model.
type DecisionReranker struct {
	base      Retriever
	model     *decision.Model
	criterion string
	threshold float64
}

// NewDecisionReranker constructs a [DecisionReranker] using the given decision model and evaluation criterion.
func NewDecisionReranker(base Retriever, model *decision.Model, criterion string, opts ...RerankerOption) *DecisionReranker {
	r := &DecisionReranker{
		base:      base,
		model:     model,
		criterion: criterion,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

// Retrieve queries the base retriever, evaluates each candidate document with the decision model,
// annotates decision confidence metadata, filters by threshold and MinScore, and returns the top k results.
func (r *DecisionReranker) Retrieve(ctx context.Context, query string, opts ...Option) ([]document.Document, error) {
	if r == nil || r.base == nil {
		return nil, errors.New("retriever: base retriever is nil")
	}
	if r.model == nil {
		return nil, errors.New("retriever: decision model is nil")
	}

	cfg := ApplyOptions(opts...)

	// Forward Namespace and Filter to the base retriever without prematurely
	// filtering by the reranker's MinScore threshold.
	var baseOpts []Option
	if cfg.Namespace != "" {
		baseOpts = append(baseOpts, WithNamespace(cfg.Namespace))
	}
	if len(cfg.Filter) > 0 {
		baseOpts = append(baseOpts, WithFilter(cfg.Filter))
	}
	if cfg.TopK > 0 {
		baseOpts = append(baseOpts, WithTopK(cfg.TopK))
	}

	docs, err := r.base.Retrieve(ctx, query, baseOpts...)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, nil
	}

	var reranked []document.Document
	for _, doc := range docs {
		inputText := fmt.Sprintf("Query: %s\n\nDocument:\n%s", query, doc.Content)
		ans, err := r.model.Check(ctx, decision.Text(inputText), r.criterion)
		if err != nil {
			return nil, fmt.Errorf("decision reranker check failed: %w", err)
		}

		if ans.Probability < r.threshold {
			continue
		}

		score := float32(ans.Probability)
		if cfg.HasMinScore && score < cfg.MinScore {
			continue
		}

		annotated := doc.WithScore(score).WithMetadata("decision_confidence", ans.Confidence)
		reranked = append(reranked, annotated)
	}

	sortDocumentsByScore(reranked)

	if len(reranked) > cfg.TopK {
		reranked = reranked[:cfg.TopK]
	}

	return reranked, nil
}

// -------------------------------------------------------------------------
// 4. NewTool
// -------------------------------------------------------------------------

// ToolInput defines the schema for invoking a retriever tool.
type ToolInput struct {
	Query string `json:"query" jsonschema:"Search query"`
	TopK  int    `json:"top_k,omitempty" jsonschema:"Maximum number of documents to return"`
}

// NewTool wraps a [Retriever] into an executable [*tool.Tool] callable by LLM agents.
func NewTool(name, description string, r Retriever, defaultOpts ...Option) (*tool.Tool, error) {
	if r == nil {
		return nil, errors.New("retriever: retriever cannot be nil")
	}
	handler := func(ctx context.Context, in ToolInput) ([]document.Document, error) {
		callOpts := slices.Clone(defaultOpts)
		if in.TopK > 0 {
			callOpts = append(callOpts, WithTopK(in.TopK))
		}
		return r.Retrieve(ctx, in.Query, callOpts...)
	}

	return tool.New[ToolInput, []document.Document](
		name,
		name,
		description,
		handler,
		tool.WithAnnotation(tool.Annotation{
			IsReadOnly:   true,
			IsIdempotent: true,
		}),
	)
}
