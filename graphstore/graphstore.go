package graphstore

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/vectorstore"
)

// DefaultNamespace is the default namespace used across graph store operations.
const DefaultNamespace = "default"

// Node represents an entity or concept in the knowledge graph.
type Node struct {
	ID         string         `json:"id"`
	Label      string         `json:"label,omitempty"`
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties,omitempty"`
}

// Edge represents a directed, typed relationship between two nodes.
type Edge struct {
	Source     string         `json:"source"`
	Target     string         `json:"target"`
	Type       string         `json:"type"`
	Weight     float32        `json:"weight,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
	DocID      string         `json:"doc_id,omitempty"`
}

// GraphDocument pairs extracted nodes and edges with their source document chunk.
type GraphDocument struct {
	Source document.Document `json:"source"`
	Nodes  []Node            `json:"nodes"`
	Edges  []Edge            `json:"edges"`
}

// Subgraph represents the induced neighborhood returned by [GraphStore.Traverse].
type Subgraph struct {
	Nodes     []Node
	Edges     []Edge
	SourceIDs []string
}

// TripleRule constrains allowed (SourceType, EdgeType, TargetType) ontology tuples.
// An empty field acts as a wildcard matching any type in that position.
type TripleRule struct {
	SourceType string
	EdgeType   string
	TargetType string
}

// GraphStore represents a storage backend for knowledge graph nodes, edges,
// entity search, and cycle-safe multi-hop traversal.
type GraphStore interface {
	// Upsert atomically merges nodes, edges, and document provenance links.
	Upsert(ctx context.Context, docs []GraphDocument, opts ...UpsertOption) error

	// SearchNodes finds seed nodes matching a text query and optional filters.
	SearchNodes(ctx context.Context, query string, k int, opts ...SearchOption) ([]Node, error)

	// Traverse performs a cycle-safe breadth-first expansion up to maxDepth hops
	// starting from seedNodeIDs, returning the induced subgraph.
	Traverse(ctx context.Context, seedNodeIDs []string, maxDepth int, opts ...TraverseOption) (Subgraph, error)

	// Delete removes nodes (and incident edges) or provenance edges matching ids.
	Delete(ctx context.Context, ids []string, opts ...DeleteOption) error
}

// UpsertConfig configures graph ingestion.
type UpsertConfig struct {
	Namespace string
}

// UpsertOption configures an [UpsertConfig].
type UpsertOption func(*UpsertConfig)

// WithUpsertNamespace sets the namespace for Upsert.
func WithUpsertNamespace(ns string) UpsertOption {
	return func(c *UpsertConfig) {
		if ns != "" {
			c.Namespace = ns
		}
	}
}

// ApplyUpsertOptions resolves options into an [UpsertConfig], defaulting Namespace to [DefaultNamespace].
func ApplyUpsertOptions(opts ...UpsertOption) UpsertConfig {
	cfg := UpsertConfig{
		Namespace: DefaultNamespace,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

// SearchConfig configures node search queries.
type SearchConfig struct {
	Namespace   string
	NodeTypes   []string
	Filter      map[string]any
	MinScore    float32
	HasMinScore bool
}

// SearchOption configures a [SearchConfig].
type SearchOption func(*SearchConfig)

// WithNamespace sets the namespace to query in SearchNodes.
func WithNamespace(ns string) SearchOption {
	return func(c *SearchConfig) {
		if ns != "" {
			c.Namespace = ns
		}
	}
}

// WithNodeTypes restricts SearchNodes to nodes whose Type matches one of types.
func WithNodeTypes(types ...string) SearchOption {
	return func(c *SearchConfig) {
		c.NodeTypes = append([]string(nil), types...)
	}
}

// WithFilter restricts SearchNodes to nodes whose Properties match filter.
func WithFilter(filter map[string]any) SearchOption {
	return func(c *SearchConfig) {
		c.Filter = filter
	}
}

// WithMinScore sets the minimum similarity/relevance score threshold for SearchNodes.
func WithMinScore(score float32) SearchOption {
	return func(c *SearchConfig) {
		c.MinScore = score
		c.HasMinScore = true
	}
}

// ApplySearchOptions resolves options into a [SearchConfig], defaulting Namespace to [DefaultNamespace].
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

// TraverseConfig configures multi-hop subgraph traversal.
type TraverseConfig struct {
	Namespace     string
	EdgeTypes     []string
	Bidirectional bool
	MaxEdges      int
	MaxNodes      int
}

// TraverseOption configures a [TraverseConfig].
type TraverseOption func(*TraverseConfig)

// WithTraverseNamespace sets the namespace for Traverse.
func WithTraverseNamespace(ns string) TraverseOption {
	return func(c *TraverseConfig) {
		if ns != "" {
			c.Namespace = ns
		}
	}
}

// WithEdgeTypes restricts Traverse to edges whose Type matches one of types.
func WithEdgeTypes(types ...string) TraverseOption {
	return func(c *TraverseConfig) {
		c.EdgeTypes = append([]string(nil), types...)
	}
}

// WithBidirectional controls whether Traverse follows incoming as well as outgoing edges (default: true).
func WithBidirectional(enabled bool) TraverseOption {
	return func(c *TraverseConfig) {
		c.Bidirectional = enabled
	}
}

// WithMaxEdges caps the total number of edges returned by Traverse (default: 50).
func WithMaxEdges(n int) TraverseOption {
	return func(c *TraverseConfig) {
		if n > 0 {
			c.MaxEdges = n
		}
	}
}

// WithMaxNodes caps the total number of nodes returned by Traverse (default: 100).
func WithMaxNodes(n int) TraverseOption {
	return func(c *TraverseConfig) {
		if n > 0 {
			c.MaxNodes = n
		}
	}
}

// ApplyTraverseOptions resolves options into a [TraverseConfig].
func ApplyTraverseOptions(opts ...TraverseOption) TraverseConfig {
	cfg := TraverseConfig{
		Namespace:     DefaultNamespace,
		Bidirectional: true,
		MaxEdges:      50,
		MaxNodes:      100,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

// DeleteConfig configures graph deletion.
type DeleteConfig struct {
	Namespace string
	ByDocID   bool
}

// DeleteOption configures a [DeleteConfig].
type DeleteOption func(*DeleteConfig)

// WithDeleteNamespace sets the namespace for Delete.
func WithDeleteNamespace(ns string) DeleteOption {
	return func(c *DeleteConfig) {
		if ns != "" {
			c.Namespace = ns
		}
	}
}

// WithDeleteByDocID deletes edges and provenance linked to source document IDs
// instead of deleting by node ID.
func WithDeleteByDocID(byDocID bool) DeleteOption {
	return func(c *DeleteConfig) {
		c.ByDocID = byDocID
	}
}

// ApplyDeleteOptions resolves options into a [DeleteConfig], defaulting Namespace to [DefaultNamespace].
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

// NormalizeID converts an arbitrary entity identifier or name into a canonical
// lowercase underscore-separated slug (e.g., "Marie Curie" -> "marie_curie").
func NormalizeID(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	prevSep := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevSep = false
		} else if !prevSep && b.Len() > 0 {
			b.WriteByte('_')
			prevSep = true
		}
	}
	return strings.Trim(b.String(), "_")
}

// NormalizeEdgeType normalizes a relationship predicate to uppercase underscore form.
func NormalizeEdgeType(s string) string {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	prevSep := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevSep = false
		} else if !prevSep && b.Len() > 0 {
			b.WriteByte('_')
			prevSep = true
		}
	}
	return strings.Trim(b.String(), "_")
}

// NodeSearchText builds a deterministic searchable string from a node's label,
// ID, type, and property values for lexical or embedding indexing.
func NodeSearchText(n Node) string {
	var parts []string
	if n.Label != "" {
		parts = append(parts, n.Label)
	}
	if n.ID != "" && !strings.EqualFold(n.ID, n.Label) {
		parts = append(parts, strings.ReplaceAll(n.ID, "_", " "))
	}
	if n.Type != "" {
		parts = append(parts, n.Type)
	}
	if len(n.Properties) > 0 {
		for _, k := range slices.Sorted(maps.Keys(n.Properties)) {
			v := n.Properties[k]
			if v == nil {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s %v", k, v))
		}
	}
	return strings.Join(parts, " ")
}

// CloneProperties returns a shallow copy of a node/edge property map.
func CloneProperties(m map[string]any) map[string]any {
	return document.CloneMetadata(m)
}

// MatchesProperties reports whether props satisfies filter using [vectorstore.MatchesFilter].
func MatchesProperties(props, filter map[string]any) bool {
	return vectorstore.MatchesFilter(props, filter)
}

// MergeNode merges incoming node fields into existing, preserving existing
// metadata when incoming omits fields and merging Properties keys.
func MergeNode(existing, incoming Node) Node {
	out := existing
	if out.ID == "" {
		out.ID = incoming.ID
	}
	if incoming.Label != "" {
		out.Label = incoming.Label
	}
	if out.Label == "" {
		out.Label = out.ID
	}
	if incoming.Type != "" {
		out.Type = incoming.Type
	}
	if len(existing.Properties) > 0 || len(incoming.Properties) > 0 {
		merged := CloneProperties(existing.Properties)
		if merged == nil {
			merged = make(map[string]any, len(incoming.Properties))
		}
		maps.Copy(merged, incoming.Properties)
		out.Properties = merged
	}
	return out
}
