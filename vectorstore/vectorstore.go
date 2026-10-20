package vectorstore

import (
	"context"
	"reflect"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/embedding"
)

// DefaultNamespace is the default namespace used across vector store operations.
const DefaultNamespace = "default"

// VectorStore represents a storage backend for indexing and searching documents
// by their vector embeddings.
type VectorStore interface {
	Add(ctx context.Context, docs []document.Document, opts ...AddOption) ([]string, error)
	Search(ctx context.Context, query string, k int, opts ...SearchOption) ([]document.Document, error)
	Delete(ctx context.Context, ids []string, opts ...DeleteOption) error
}

// AddConfig configures document ingestion into a vector store.
type AddConfig struct {
	Namespace string
}

// AddOption configures an [AddConfig].
type AddOption func(*AddConfig)

// WithAddNamespace sets the namespace where documents will be added.
func WithAddNamespace(ns string) AddOption {
	return func(c *AddConfig) {
		c.Namespace = ns
	}
}

// ApplyAddOptions resolves options into an [AddConfig], defaulting Namespace
// to [DefaultNamespace].
func ApplyAddOptions(opts ...AddOption) AddConfig {
	cfg := AddConfig{
		Namespace: DefaultNamespace,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

// SearchConfig configures document retrieval from a vector store.
type SearchConfig struct {
	Namespace   string
	MinScore    float32
	HasMinScore bool
	Filter      map[string]any
}

// SearchOption configures a [SearchConfig].
type SearchOption func(*SearchConfig)

// WithNamespace sets the namespace to query.
func WithNamespace(ns string) SearchOption {
	return func(c *SearchConfig) {
		c.Namespace = ns
	}
}

// WithMinScore sets the minimum similarity score threshold for returned documents.
func WithMinScore(score float32) SearchOption {
	return func(c *SearchConfig) {
		c.MinScore = score
		c.HasMinScore = true
	}
}

// WithFilter sets metadata key-value filters that returned documents must match.
func WithFilter(filter map[string]any) SearchOption {
	return func(c *SearchConfig) {
		c.Filter = filter
	}
}

// ApplySearchOptions resolves options into a [SearchConfig], defaulting Namespace
// to [DefaultNamespace].
func ApplySearchOptions(opts ...SearchOption) SearchConfig {
	cfg := SearchConfig{
		Namespace: DefaultNamespace,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

// DeleteConfig configures document deletion from a vector store.
type DeleteConfig struct {
	Namespace string
}

// DeleteOption configures a [DeleteConfig].
type DeleteOption func(*DeleteConfig)

// WithDeleteNamespace sets the namespace from which documents will be deleted.
func WithDeleteNamespace(ns string) DeleteOption {
	return func(c *DeleteConfig) {
		c.Namespace = ns
	}
}

// ApplyDeleteOptions resolves options into a [DeleteConfig], defaulting Namespace
// to [DefaultNamespace].
func ApplyDeleteOptions(opts ...DeleteOption) DeleteConfig {
	cfg := DeleteConfig{
		Namespace: DefaultNamespace,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

// MatchesFilter checks whether metadata matches the specified filter criteria.
// Returns true if filter is empty. Returns false if metadata is nil.
// Performs numeric normalization so that JSON-unmarshaled float64 matches int filter values.
func MatchesFilter(metadata, filter map[string]any) bool {
	if len(filter) == 0 {
		return true
	}
	if metadata == nil {
		return false
	}
	for k, want := range filter {
		got, ok := metadata[k]
		if !ok {
			return false
		}
		if !valuesMatch(got, want) {
			return false
		}
	}
	return true
}

func valuesMatch(got, want any) bool {
	gotF, gotOk := toFloat64(got)
	wantF, wantOk := toFloat64(want)
	if gotOk && wantOk {
		return gotF == wantF
	}
	return reflect.DeepEqual(got, want)
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

// ResolveDims determines the vector dimensionality from explicit configuration,
// the embedding model configuration, or the model profile limits. Returns 0 if unknown.
func ResolveDims(embed *embedding.Model, explicitDims int) int {
	if explicitDims > 0 {
		return explicitDims
	}
	if embed == nil {
		return 0
	}
	if cfg := embed.Config(); cfg != nil && cfg.Dimensions != nil && *cfg.Dimensions > 0 {
		return *cfg.Dimensions
	}
	if prof := embed.Profile(); prof != nil && prof.Limits.Dimensions > 0 {
		return prof.Limits.Dimensions
	}
	return 0
}
