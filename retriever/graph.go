package retriever

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"

	"go.opentelemetry.io/otel/trace"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/telemetry"
	"github.com/masterkeysrd/loom/vectorstore"
)

var _ Retriever = (*GraphRetriever)(nil)

// DocumentIDKey is the special EdgeSpec key referring to [document.Document.ID].
const DocumentIDKey = "$id"

// EdgeSpec defines a directed metadata link from SourceKey on a discovered
// document to TargetKey on candidate neighbor documents.
// Use "$id" ([DocumentIDKey]) to reference [document.Document.ID].
type EdgeSpec struct {
	SourceKey string
	TargetKey string
}

// TraversalStrategy controls how discovered graph candidates are ranked and selected.
type TraversalStrategy int

const (
	// TraversalEager performs BFS with per-hop score decay and selects top-K by score.
	TraversalEager TraversalStrategy = iota
	// TraversalMMR diversifies selection over the discovered pool using Maximal Marginal Relevance.
	TraversalMMR
)

// String returns the human-readable name of the traversal strategy.
func (s TraversalStrategy) String() string {
	switch s {
	case TraversalMMR:
		return "mmr"
	default:
		return "eager"
	}
}

// GraphOption configures a [GraphRetriever].
type GraphOption func(*GraphRetriever)

// WithGraphMaxDepth sets the maximum number of metadata hops to traverse (default: 1).
func WithGraphMaxDepth(depth int) GraphOption {
	return func(g *GraphRetriever) {
		if depth >= 0 {
			g.maxDepth = depth
		}
	}
}

// WithGraphStartK sets the number of initial vector seed documents (default: 4).
func WithGraphStartK(k int) GraphOption {
	return func(g *GraphRetriever) {
		if k > 0 {
			g.startK = k
		}
	}
}

// WithGraphAdjacentK sets the maximum neighbors fetched per edge spec at each hop (default: 4).
func WithGraphAdjacentK(k int) GraphOption {
	return func(g *GraphRetriever) {
		if k > 0 {
			g.adjacentK = k
		}
	}
}

// WithGraphDepthDecay sets the per-hop score multiplier in (0, 1] (default: 0.85).
func WithGraphDepthDecay(decay float32) GraphOption {
	return func(g *GraphRetriever) {
		if decay > 0 && decay <= 1 {
			g.depthDecay = decay
		}
	}
}

// WithGraphStrategy selects [TraversalEager] (default) or [TraversalMMR].
func WithGraphStrategy(s TraversalStrategy) GraphOption {
	return func(g *GraphRetriever) {
		g.strategy = s
	}
}

// WithGraphMMRLambda sets the relevance-vs-diversity trade-off for [TraversalMMR]
// in [0, 1] (default: 0.5).
func WithGraphMMRLambda(lambda float32) GraphOption {
	return func(g *GraphRetriever) {
		if lambda < 0 {
			lambda = 0
		} else if lambda > 1 {
			lambda = 1
		}
		g.mmrLambda = lambda
	}
}

// WithGraphDefaultOptions appends default [Option] values applied on every Retrieve call.
func WithGraphDefaultOptions(opts ...Option) GraphOption {
	return func(g *GraphRetriever) {
		g.defaultOpts = append(g.defaultOpts, opts...)
	}
}

// GraphRetriever performs multi-hop metadata-edge graph traversal over a [vectorstore.VectorStore].
type GraphRetriever struct {
	vs          vectorstore.VectorStore
	edges       []EdgeSpec
	maxDepth    int
	startK      int
	adjacentK   int
	depthDecay  float32
	strategy    TraversalStrategy
	mmrLambda   float32
	defaultOpts []Option
	middleware  []Middleware
}

// NewGraphRetriever creates a metadata-edge [GraphRetriever] wrapped with
// [TelemetryMiddleware]("graph_vector") by default.
func NewGraphRetriever(vs vectorstore.VectorStore, edges []EdgeSpec, opts ...GraphOption) *GraphRetriever {
	g := &GraphRetriever{
		vs:         vs,
		edges:      slices.Clone(edges),
		maxDepth:   1,
		startK:     4,
		adjacentK:  4,
		depthDecay: 0.85,
		strategy:   TraversalEager,
		mmrLambda:  0.5,
		middleware: []Middleware{TelemetryMiddleware("graph_vector")},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(g)
		}
	}
	return g
}

// WithMiddleware returns a clone of the retriever with additional middleware appended.
func (g *GraphRetriever) WithMiddleware(mws ...Middleware) *GraphRetriever {
	clone := *g
	clone.edges = slices.Clone(g.edges)
	clone.defaultOpts = slices.Clone(g.defaultOpts)
	clone.middleware = append(slices.Clone(g.middleware), mws...)
	return &clone
}

// Retrieve seeds from the vector store, expands along configured metadata edges
// up to MaxDepth hops, and selects up to TopK documents using the configured strategy.
func (g *GraphRetriever) Retrieve(ctx context.Context, query string, opts ...Option) ([]document.Document, error) {
	if g == nil || g.vs == nil {
		return nil, errors.New("retriever: vector store is nil")
	}

	allOpts := append(slices.Clone(g.defaultOpts), opts...)
	base := RetrieverFunc(func(ctx context.Context, q string, callOpts ...Option) ([]document.Document, error) {
		cfg := ApplyOptions(callOpts...)
		if cfg.TopK <= 0 {
			return nil, nil
		}

		span := trace.SpanFromContext(ctx)
		span.SetAttributes(
			telemetry.KeyLoomRetrieverMaxDepth.Int(g.maxDepth),
			telemetry.KeyLoomRetrieverStrategy.String(g.strategy.String()),
		)

		baseSearchOpts := buildBaseSearchOpts(cfg)
		seeds, err := g.vs.Search(ctx, q, g.startK, baseSearchOpts...)
		if err != nil {
			return nil, err
		}
		if len(seeds) == 0 {
			span.SetAttributes(telemetry.KeyLoomRetrieverHops.Int(0))
			return nil, nil
		}

		discovered := make(map[string]document.Document, len(seeds))
		frontier := make([]document.Document, 0, len(seeds))
		for _, doc := range seeds {
			if existing, ok := discovered[doc.ID]; !ok || doc.Score > existing.Score {
				discovered[doc.ID] = doc
			}
			frontier = append(frontier, doc)
		}

		hopsExecuted := 0
		for depth := 1; depth <= g.maxDepth && len(frontier) > 0 && len(g.edges) > 0; depth++ {
			decayFactor := float32(math.Pow(float64(g.depthDecay), float64(depth)))
			var nextFrontier []document.Document
			nextSeen := make(map[string]struct{})
			hopHadWork := false

			for _, edge := range g.edges {
				if edge.SourceKey == "" || edge.TargetKey == "" {
					continue
				}

				valScores := make(map[string]float32)
				var rawVals []any
				rawSeen := make(map[string]struct{})

				for _, doc := range frontier {
					vals := extractEdgeValues(doc, edge.SourceKey)
					for _, v := range vals {
						key := fmt.Sprint(v)
						if key == "" {
							continue
						}
						if prev, ok := valScores[key]; !ok || doc.Score > prev {
							valScores[key] = doc.Score
						}
						if _, ok := rawSeen[key]; !ok {
							rawSeen[key] = struct{}{}
							rawVals = append(rawVals, v)
						}
					}
				}
				if len(rawVals) == 0 {
					continue
				}
				hopHadWork = true

				var neighbors []document.Document
				if edge.TargetKey == DocumentIDKey {
					targetIDs := make([]string, 0, len(rawVals))
					for _, v := range rawVals {
						id := fmt.Sprint(v)
						if id == "" {
							continue
						}
						if _, already := discovered[id]; already {
							continue
						}
						targetIDs = append(targetIDs, id)
					}
					if len(targetIDs) == 0 {
						continue
					}
					neighbors, err = g.vs.Get(ctx, targetIDs, baseSearchOpts...)
					if err != nil {
						return nil, err
					}
					if len(neighbors) > g.adjacentK {
						neighbors = neighbors[:g.adjacentK]
					}
				} else {
					mergedFilter := make(map[string]any, len(cfg.Filter)+1)
					maps.Copy(mergedFilter, cfg.Filter)
					mergedFilter[edge.TargetKey] = rawVals

					hopOpts := []vectorstore.SearchOption{vectorstore.WithFilter(mergedFilter)}
					if cfg.Namespace != "" {
						hopOpts = append(hopOpts, vectorstore.WithNamespace(cfg.Namespace))
					}
					neighbors, err = g.vs.Search(ctx, q, g.adjacentK, hopOpts...)
					if err != nil {
						return nil, err
					}
				}

				for _, nb := range neighbors {
					parentScore := bestParentScore(nb, edge.TargetKey, valScores)
					if parentScore == 0 {
						parentScore = nb.Score
					}
					decayed := parentScore * decayFactor

					if existing, seen := discovered[nb.ID]; seen {
						if decayed > existing.Score {
							existing.Score = decayed
							discovered[nb.ID] = existing
						}
						continue
					}

					scored := nb.WithScore(decayed)
					discovered[nb.ID] = scored
					if _, queued := nextSeen[nb.ID]; !queued {
						nextSeen[nb.ID] = struct{}{}
						nextFrontier = append(nextFrontier, scored)
					}
				}
			}

			if hopHadWork {
				hopsExecuted = depth
			}
			frontier = nextFrontier
		}

		span.SetAttributes(telemetry.KeyLoomRetrieverHops.Int(hopsExecuted))

		candidates := make([]document.Document, 0, len(discovered))
		for _, doc := range discovered {
			if cfg.HasMinScore && doc.Score < cfg.MinScore {
				continue
			}
			candidates = append(candidates, doc)
		}
		if len(candidates) == 0 {
			return nil, nil
		}

		if g.strategy == TraversalMMR || cfg.UseMMR {
			lambda := g.mmrLambda
			if cfg.UseMMR {
				lambda = cfg.MMRLambda
			}
			candidateIDs := make([]string, len(candidates))
			for i, doc := range candidates {
				candidateIDs[i] = doc.ID
			}
			mmrOpts := append(slices.Clone(baseSearchOpts),
				vectorstore.WithIDs(candidateIDs...),
				vectorstore.WithMMR(lambda, len(candidateIDs)),
			)
			mmrDocs, err := g.vs.Search(ctx, q, cfg.TopK, mmrOpts...)
			if err != nil {
				return nil, err
			}
			if len(mmrDocs) > 0 {
				return mmrDocs, nil
			}
		}

		sortDocumentsByScore(candidates)
		if len(candidates) > cfg.TopK {
			candidates = candidates[:cfg.TopK]
		}
		return candidates, nil
	})

	chained := Chain(base, g.middleware...)
	return chained.Retrieve(ctx, query, allOpts...)
}

func buildBaseSearchOpts(cfg Config) []vectorstore.SearchOption {
	var opts []vectorstore.SearchOption
	if cfg.Namespace != "" {
		opts = append(opts, vectorstore.WithNamespace(cfg.Namespace))
	}
	if len(cfg.Filter) > 0 {
		opts = append(opts, vectorstore.WithFilter(cfg.Filter))
	}
	return opts
}

func extractEdgeValues(doc document.Document, key string) []any {
	if key == DocumentIDKey {
		if doc.ID == "" {
			return nil
		}
		return []any{doc.ID}
	}
	if doc.Metadata == nil {
		return nil
	}
	val, ok := doc.Metadata[key]
	if !ok || val == nil {
		return nil
	}
	switch s := val.(type) {
	case []string:
		out := make([]any, 0, len(s))
		for _, item := range s {
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(s))
		for _, item := range s {
			if item != nil {
				out = append(out, item)
			}
		}
		return out
	case []byte:
		return nil
	default:
		rv := reflect.ValueOf(val)
		if rv.Kind() == reflect.Slice {
			out := make([]any, 0, rv.Len())
			for i := range rv.Len() {
				elem := rv.Index(i).Interface()
				if elem != nil {
					out = append(out, elem)
				}
			}
			return out
		}
		return []any{val}
	}
}

func bestParentScore(doc document.Document, targetKey string, valScores map[string]float32) float32 {
	var best float32
	for _, v := range extractEdgeValues(doc, targetKey) {
		if s, ok := valScores[fmt.Sprint(v)]; ok && s > best {
			best = s
		}
	}
	return best
}
