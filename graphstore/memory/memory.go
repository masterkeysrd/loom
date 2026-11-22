package memory

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/graphstore"
	"github.com/masterkeysrd/loom/internal/bm25"
)

var _ graphstore.GraphStore = (*Store)(nil)

// Option configures an in-memory [Store].
type Option func(*Store)

// WithEmbedding configures an optional embedding model for semantic node grounding in SearchNodes.
func WithEmbedding(embed *embedding.Model) Option {
	return func(s *Store) {
		s.embed = embed
	}
}

type edgeKey struct {
	source string
	target string
	typ    string
	docID  string
}

type nsGraph struct {
	nodes    map[string]graphstore.Node
	vecs     map[string]embedding.Vector
	edges    map[edgeKey]graphstore.Edge
	outEdges map[string]map[edgeKey]struct{}
	inEdges  map[string]map[edgeKey]struct{}
	bm25Idx  *bm25.Index[string]
	dirty    bool
}

func newNSGraph() *nsGraph {
	return &nsGraph{
		nodes:    make(map[string]graphstore.Node),
		vecs:     make(map[string]embedding.Vector),
		edges:    make(map[edgeKey]graphstore.Edge),
		outEdges: make(map[string]map[edgeKey]struct{}),
		inEdges:  make(map[string]map[edgeKey]struct{}),
		dirty:    true,
	}
}

func (g *nsGraph) ensureBM25() *bm25.Index[string] {
	if !g.dirty && g.bm25Idx != nil {
		return g.bm25Idx
	}
	idx := bm25.New[string]()
	for _, id := range slices.Sorted(maps.Keys(g.nodes)) {
		idx.Add(graphstore.NodeSearchText(g.nodes[id]), id)
	}
	g.bm25Idx = idx
	g.dirty = false
	return idx
}

func (g *nsGraph) addEdge(e graphstore.Edge) {
	key := edgeKey{source: e.Source, target: e.Target, typ: e.Type, docID: e.DocID}
	e.Properties = graphstore.CloneProperties(e.Properties)
	g.edges[key] = e
	if g.outEdges[e.Source] == nil {
		g.outEdges[e.Source] = make(map[edgeKey]struct{})
	}
	g.outEdges[e.Source][key] = struct{}{}
	if g.inEdges[e.Target] == nil {
		g.inEdges[e.Target] = make(map[edgeKey]struct{})
	}
	g.inEdges[e.Target][key] = struct{}{}
}

func (g *nsGraph) removeEdge(key edgeKey) {
	delete(g.edges, key)
	if out := g.outEdges[key.source]; out != nil {
		delete(out, key)
		if len(out) == 0 {
			delete(g.outEdges, key.source)
		}
	}
	if in := g.inEdges[key.target]; in != nil {
		delete(in, key)
		if len(in) == 0 {
			delete(g.inEdges, key.target)
		}
	}
}

// Store is an in-memory, thread-safe implementation of [graphstore.GraphStore].
type Store struct {
	embed  *embedding.Model
	mu     sync.RWMutex
	graphs map[string]*nsGraph
}

// New creates a new in-memory [Store].
func New(opts ...Option) *Store {
	s := &Store{
		graphs: make(map[string]*nsGraph),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Upsert atomically merges nodes, edges, and provenance links into the specified namespace.
func (s *Store) Upsert(ctx context.Context, docs []graphstore.GraphDocument, opts ...graphstore.UpsertOption) error {
	if len(docs) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	cfg := graphstore.ApplyUpsertOptions(opts...)

	// Prepare normalized nodes and edges before acquiring lock.
	stagedNodes := make(map[string]graphstore.Node)
	var stagedEdges []graphstore.Edge

	for _, gd := range docs {
		for _, n := range gd.Nodes {
			id := graphstore.NormalizeID(n.ID)
			if id == "" {
				id = graphstore.NormalizeID(n.Label)
			}
			if id == "" {
				continue
			}
			n.ID = id
			if n.Label == "" {
				n.Label = id
			}
			n.Properties = graphstore.CloneProperties(n.Properties)
			if existing, ok := stagedNodes[id]; ok {
				stagedNodes[id] = graphstore.MergeNode(existing, n)
			} else {
				stagedNodes[id] = n
			}
		}

		for _, e := range gd.Edges {
			src := graphstore.NormalizeID(e.Source)
			dst := graphstore.NormalizeID(e.Target)
			typ := graphstore.NormalizeEdgeType(e.Type)
			if src == "" || dst == "" || typ == "" {
				continue
			}
			e.Source = src
			e.Target = dst
			e.Type = typ
			if e.Weight <= 0 {
				e.Weight = 1.0
			}
			if e.DocID == "" {
				e.DocID = gd.Source.ID
			}
			e.Properties = graphstore.CloneProperties(e.Properties)
			stagedEdges = append(stagedEdges, e)

			if _, ok := stagedNodes[src]; !ok {
				stagedNodes[src] = graphstore.Node{ID: src, Label: src}
			}
			if _, ok := stagedNodes[dst]; !ok {
				stagedNodes[dst] = graphstore.Node{ID: dst, Label: dst}
			}
		}
	}

	// Merge against existing nodes to compute final search texts if embedding is enabled.
	s.mu.RLock()
	existingNS := s.graphs[cfg.Namespace]
	mergedNodes := make(map[string]graphstore.Node, len(stagedNodes))
	for id, incoming := range stagedNodes {
		if existingNS != nil {
			if prev, ok := existingNS.nodes[id]; ok {
				mergedNodes[id] = graphstore.MergeNode(prev, incoming)
				continue
			}
		}
		mergedNodes[id] = incoming
	}
	s.mu.RUnlock()

	var embeddedVecs map[string]embedding.Vector
	if s.embed != nil && len(mergedNodes) > 0 {
		ids := slices.Sorted(maps.Keys(mergedNodes))
		texts := make([]string, len(ids))
		for i, id := range ids {
			texts[i] = graphstore.NodeSearchText(mergedNodes[id])
		}
		vecs, err := s.embed.EmbedDocuments(ctx, texts)
		if err != nil {
			return err
		}
		embeddedVecs = make(map[string]embedding.Vector, len(ids))
		for i, id := range ids {
			embeddedVecs[id] = vecs[i]
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	g := s.graphs[cfg.Namespace]
	if g == nil {
		g = newNSGraph()
		s.graphs[cfg.Namespace] = g
	}

	for id, incoming := range stagedNodes {
		if prev, ok := g.nodes[id]; ok {
			g.nodes[id] = graphstore.MergeNode(prev, incoming)
		} else {
			g.nodes[id] = incoming
		}
		if vec, ok := embeddedVecs[id]; ok {
			g.vecs[id] = vec
		}
		g.dirty = true
	}

	for _, e := range stagedEdges {
		g.addEdge(e)
	}

	return nil
}

// SearchNodes finds up to k nodes matching query within the specified namespace.
func (s *Store) SearchNodes(ctx context.Context, query string, k int, opts ...graphstore.SearchOption) ([]graphstore.Node, error) {
	if k <= 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cfg := graphstore.ApplySearchOptions(opts...)
	typeSet := makeTypeSet(cfg.NodeTypes)

	if s.embed != nil {
		queryVec, err := s.embed.EmbedQuery(ctx, query)
		if err != nil {
			return nil, err
		}
		return s.searchSemantic(cfg, typeSet, queryVec, k), nil
	}

	return s.searchLexical(cfg, typeSet, query, k), nil
}

func (s *Store) searchSemantic(cfg graphstore.SearchConfig, typeSet map[string]struct{}, queryVec embedding.Vector, k int) []graphstore.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()

	g := s.graphs[cfg.Namespace]
	if g == nil || len(g.nodes) == 0 {
		return nil
	}

	type scored struct {
		node  graphstore.Node
		score float32
	}
	var matches []scored
	for id, node := range g.nodes {
		if !matchesNodeConstraints(node, typeSet, cfg.Filter) {
			continue
		}
		vec, ok := g.vecs[id]
		if !ok || len(vec) == 0 {
			continue
		}
		score := queryVec.CosineSimilarity(vec)
		if cfg.HasMinScore && score < cfg.MinScore {
			continue
		}
		matches = append(matches, scored{node: cloneNode(node), score: score})
	}

	slices.SortFunc(matches, func(a, b scored) int {
		if a.score > b.score {
			return -1
		}
		if a.score < b.score {
			return 1
		}
		return cmp.Compare(a.node.ID, b.node.ID)
	})

	if len(matches) > k {
		matches = matches[:k]
	}
	out := make([]graphstore.Node, len(matches))
	for i, m := range matches {
		out[i] = m.node
	}
	return out
}

func (s *Store) searchLexical(cfg graphstore.SearchConfig, typeSet map[string]struct{}, query string, k int) []graphstore.Node {
	s.mu.Lock()
	defer s.mu.Unlock()

	g := s.graphs[cfg.Namespace]
	if g == nil || len(g.nodes) == 0 {
		return nil
	}

	idx := g.ensureBM25()
	bm25Results := idx.SearchSorted(query, func(id string) bool {
		node, ok := g.nodes[id]
		if !ok {
			return false
		}
		return matchesNodeConstraints(node, typeSet, cfg.Filter)
	})

	seen := make(map[string]struct{}, k)
	out := make([]graphstore.Node, 0, min(k, len(g.nodes)))
	for _, r := range bm25Results {
		if cfg.HasMinScore && float32(r.Score) < cfg.MinScore {
			continue
		}
		if node, ok := g.nodes[r.Item]; ok {
			seen[node.ID] = struct{}{}
			out = append(out, cloneNode(node))
			if len(out) == k {
				return out
			}
		}
	}

	// Substring fallback for partial/fuzzy labels when BM25 has fewer than k matches.
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" || len(out) >= k {
		return out
	}

	for _, id := range slices.Sorted(maps.Keys(g.nodes)) {
		if _, ok := seen[id]; ok {
			continue
		}
		node := g.nodes[id]
		if !matchesNodeConstraints(node, typeSet, cfg.Filter) {
			continue
		}
		haystack := strings.ToLower(graphstore.NodeSearchText(node))
		if strings.Contains(haystack, needle) || strings.Contains(needle, strings.ToLower(node.Label)) {
			seen[id] = struct{}{}
			out = append(out, cloneNode(node))
			if len(out) == k {
				break
			}
		}
	}

	return out
}

// Traverse performs a cycle-safe breadth-first expansion up to maxDepth hops
// starting from seedNodeIDs, returning the induced subgraph.
func (s *Store) Traverse(ctx context.Context, seedNodeIDs []string, maxDepth int, opts ...graphstore.TraverseOption) (graphstore.Subgraph, error) {
	if len(seedNodeIDs) == 0 || maxDepth < 0 {
		return graphstore.Subgraph{}, nil
	}
	if err := ctx.Err(); err != nil {
		return graphstore.Subgraph{}, err
	}

	cfg := graphstore.ApplyTraverseOptions(opts...)
	edgeTypeSet := makeEdgeTypeSet(cfg.EdgeTypes)

	s.mu.RLock()
	defer s.mu.RUnlock()

	g := s.graphs[cfg.Namespace]
	if g == nil || len(g.nodes) == 0 {
		return graphstore.Subgraph{}, nil
	}

	visitedNodes := make(map[string]graphstore.Node)
	var frontier []string

	for _, rawID := range seedNodeIDs {
		id := graphstore.NormalizeID(rawID)
		if id == "" {
			continue
		}
		if _, dup := visitedNodes[id]; dup {
			continue
		}
		node, ok := g.nodes[id]
		if !ok {
			continue
		}
		if len(visitedNodes) >= cfg.MaxNodes {
			break
		}
		visitedNodes[id] = cloneNode(node)
		frontier = append(frontier, id)
	}

	collectedEdges := make(map[edgeKey]graphstore.Edge)
	sourceIDSet := make(map[string]struct{})

	for depth := 1; depth <= maxDepth && len(frontier) > 0 && len(collectedEdges) < cfg.MaxEdges; depth++ {
		slices.Sort(frontier)
		var nextFrontier []string
		nextSeen := make(map[string]struct{})

		for _, nodeID := range frontier {
			if len(collectedEdges) >= cfg.MaxEdges {
				break
			}
			keys := collectIncidentKeys(g, nodeID, cfg.Bidirectional)
			for _, key := range keys {
				if len(collectedEdges) >= cfg.MaxEdges {
					break
				}
				if _, seenEdge := collectedEdges[key]; seenEdge {
					continue
				}
				edge := g.edges[key]
				if len(edgeTypeSet) > 0 {
					if _, allowed := edgeTypeSet[edge.Type]; !allowed {
						continue
					}
				}

				neighborID := edge.Target
				if neighborID == nodeID {
					neighborID = edge.Source
				}

				if _, alreadyNode := visitedNodes[neighborID]; !alreadyNode {
					if len(visitedNodes) >= cfg.MaxNodes {
						continue
					}
					if nb, ok := g.nodes[neighborID]; ok {
						visitedNodes[neighborID] = cloneNode(nb)
					} else {
						visitedNodes[neighborID] = graphstore.Node{ID: neighborID, Label: neighborID}
					}
					if _, queued := nextSeen[neighborID]; !queued {
						nextSeen[neighborID] = struct{}{}
						nextFrontier = append(nextFrontier, neighborID)
					}
				}

				edgeCopy := edge
				edgeCopy.Properties = graphstore.CloneProperties(edge.Properties)
				collectedEdges[key] = edgeCopy
				if edge.DocID != "" {
					sourceIDSet[edge.DocID] = struct{}{}
				}
			}
		}

		frontier = nextFrontier
	}

	nodeIDs := slices.Sorted(maps.Keys(visitedNodes))
	nodes := make([]graphstore.Node, len(nodeIDs))
	for i, id := range nodeIDs {
		nodes[i] = visitedNodes[id]
	}

	edgeKeys := slices.SortedFunc(maps.Keys(collectedEdges), compareEdgeKeys)
	edges := make([]graphstore.Edge, len(edgeKeys))
	for i, k := range edgeKeys {
		edges[i] = collectedEdges[k]
	}

	sourceIDs := slices.Sorted(maps.Keys(sourceIDSet))
	return graphstore.Subgraph{
		Nodes:     nodes,
		Edges:     edges,
		SourceIDs: sourceIDs,
	}, nil
}

// Delete removes nodes (and their incident edges) or provenance edges linked to doc IDs.
func (s *Store) Delete(ctx context.Context, ids []string, opts ...graphstore.DeleteOption) error {
	if len(ids) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	cfg := graphstore.ApplyDeleteOptions(opts...)

	s.mu.Lock()
	defer s.mu.Unlock()

	g := s.graphs[cfg.Namespace]
	if g == nil {
		return nil
	}

	if cfg.ByDocID {
		docSet := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			docSet[id] = struct{}{}
		}
		var toRemove []edgeKey
		for k := range g.edges {
			if _, ok := docSet[k.docID]; ok {
				toRemove = append(toRemove, k)
			}
		}
		for _, k := range toRemove {
			g.removeEdge(k)
		}
		return nil
	}

	for _, rawID := range ids {
		id := graphstore.NormalizeID(rawID)
		if id == "" {
			continue
		}
		if _, ok := g.nodes[id]; ok {
			delete(g.nodes, id)
			delete(g.vecs, id)
			g.dirty = true
		}
		var toRemove []edgeKey
		for k := range g.outEdges[id] {
			toRemove = append(toRemove, k)
		}
		for k := range g.inEdges[id] {
			toRemove = append(toRemove, k)
		}
		for _, k := range toRemove {
			g.removeEdge(k)
		}
	}

	return nil
}

func collectIncidentKeys(g *nsGraph, nodeID string, bidirectional bool) []edgeKey {
	keySet := make(map[edgeKey]struct{})
	for k := range g.outEdges[nodeID] {
		keySet[k] = struct{}{}
	}
	if bidirectional {
		for k := range g.inEdges[nodeID] {
			keySet[k] = struct{}{}
		}
	}
	keys := make([]edgeKey, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, compareEdgeKeys)
	return keys
}

func compareEdgeKeys(a, b edgeKey) int {
	if c := cmp.Compare(a.source, b.source); c != 0 {
		return c
	}
	if c := cmp.Compare(a.target, b.target); c != 0 {
		return c
	}
	if c := cmp.Compare(a.typ, b.typ); c != 0 {
		return c
	}
	return cmp.Compare(a.docID, b.docID)
}

func makeTypeSet(types []string) map[string]struct{} {
	if len(types) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(types))
	for _, t := range types {
		set[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
	}
	return set
}

func makeEdgeTypeSet(types []string) map[string]struct{} {
	if len(types) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(types))
	for _, t := range types {
		if norm := graphstore.NormalizeEdgeType(t); norm != "" {
			set[norm] = struct{}{}
		}
	}
	return set
}

func matchesNodeConstraints(node graphstore.Node, typeSet map[string]struct{}, filter map[string]any) bool {
	if len(typeSet) > 0 {
		if _, ok := typeSet[strings.ToLower(strings.TrimSpace(node.Type))]; !ok {
			return false
		}
	}
	return graphstore.MatchesProperties(node.Properties, filter)
}

func cloneNode(n graphstore.Node) graphstore.Node {
	n.Properties = graphstore.CloneProperties(n.Properties)
	return n
}
