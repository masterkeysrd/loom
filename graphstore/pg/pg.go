package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/lib/pq"

	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/graphstore"
	"github.com/masterkeysrd/loom/vectorstore"
	vspg "github.com/masterkeysrd/loom/vectorstore/pg"
)

var _ graphstore.GraphStore = (*Store)(nil)

// Option configures a PostgreSQL [Store].
type Option func(*Store)

// WithEmbedding configures an optional embedding model for semantic node grounding via pgvector.
func WithEmbedding(embed *embedding.Model) Option {
	return func(s *Store) {
		s.embed = embed
	}
}

// WithDims configures the expected vector dimensionality for pgvector migrations.
func WithDims(dims int) Option {
	return func(s *Store) {
		s.dims = dims
	}
}

// Store is a PostgreSQL-backed implementation of [graphstore.GraphStore].
type Store struct {
	db    *sql.DB
	embed *embedding.Model
	dims  int
}

// New creates a new PostgreSQL [Store] and applies pending migrations.
func New(db *sql.DB, opts ...Option) (*Store, error) {
	if db == nil {
		return nil, errors.New("graphstore/pg: db cannot be nil")
	}

	s := &Store{db: db}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}

	dims := vectorstore.ResolveDims(s.embed, s.dims)
	s.dims = dims

	if err := Migrate(db, s.embed != nil, dims); err != nil {
		return nil, err
	}

	return s, nil
}

// Upsert atomically merges nodes, edges, and provenance links into PostgreSQL.
func (s *Store) Upsert(ctx context.Context, docs []graphstore.GraphDocument, opts ...graphstore.UpsertOption) error {
	if len(docs) == 0 {
		return nil
	}

	cfg := graphstore.ApplyUpsertOptions(opts...)

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
			if prev, ok := stagedNodes[id]; ok {
				stagedNodes[id] = graphstore.MergeNode(prev, n)
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

	if len(stagedNodes) == 0 && len(stagedEdges) == 0 {
		return nil
	}

	nodeIDs := slices.Sorted(maps.Keys(stagedNodes))
	existingNodes, err := s.fetchNodesByID(ctx, cfg.Namespace, nodeIDs)
	if err != nil {
		return err
	}
	mergedNodes := make(map[string]graphstore.Node, len(stagedNodes))
	for _, id := range nodeIDs {
		incoming := stagedNodes[id]
		if prev, ok := existingNodes[id]; ok {
			mergedNodes[id] = graphstore.MergeNode(prev, incoming)
		} else {
			mergedNodes[id] = incoming
		}
	}

	var nodeVecs map[string]string
	if s.embed != nil && len(nodeIDs) > 0 {
		texts := make([]string, len(nodeIDs))
		for i, id := range nodeIDs {
			texts[i] = graphstore.NodeSearchText(mergedNodes[id])
		}
		vecs, err := s.embed.EmbedDocuments(ctx, texts)
		if err != nil {
			return err
		}
		nodeVecs = make(map[string]string, len(nodeIDs))
		for i, id := range nodeIDs {
			nodeVecs[id] = vspg.FormatVector(vecs[i])
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if s.embed != nil {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO graphstore_nodes (namespace, id, label, type, properties, embedding)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6::vector)
			ON CONFLICT (namespace, id) DO UPDATE SET
				label      = EXCLUDED.label,
				type       = EXCLUDED.type,
				properties = EXCLUDED.properties,
				embedding  = EXCLUDED.embedding
		`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, id := range nodeIDs {
			n := mergedNodes[id]
			propBlob, err := marshalJSONObject(n.Properties)
			if err != nil {
				return fmt.Errorf("marshal node properties %s: %w", id, err)
			}
			if _, err := stmt.ExecContext(ctx, cfg.Namespace, n.ID, n.Label, n.Type, propBlob, nodeVecs[id]); err != nil {
				return fmt.Errorf("upsert node %s: %w", id, err)
			}
		}
	} else {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO graphstore_nodes (namespace, id, label, type, properties)
			VALUES ($1, $2, $3, $4, $5::jsonb)
			ON CONFLICT (namespace, id) DO UPDATE SET
				label      = EXCLUDED.label,
				type       = EXCLUDED.type,
				properties = EXCLUDED.properties
		`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, id := range nodeIDs {
			n := mergedNodes[id]
			propBlob, err := marshalJSONObject(n.Properties)
			if err != nil {
				return fmt.Errorf("marshal node properties %s: %w", id, err)
			}
			if _, err := stmt.ExecContext(ctx, cfg.Namespace, n.ID, n.Label, n.Type, propBlob); err != nil {
				return fmt.Errorf("upsert node %s: %w", id, err)
			}
		}
	}

	edgeStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO graphstore_edges (namespace, source, target, type, weight, properties, doc_id)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)
		ON CONFLICT (namespace, source, target, type, doc_id) DO UPDATE SET
			weight     = EXCLUDED.weight,
			properties = EXCLUDED.properties
	`)
	if err != nil {
		return err
	}
	defer edgeStmt.Close()

	for _, e := range stagedEdges {
		propBlob, err := marshalJSONObject(e.Properties)
		if err != nil {
			return fmt.Errorf("marshal edge properties %s->%s: %w", e.Source, e.Target, err)
		}
		if _, err := edgeStmt.ExecContext(ctx, cfg.Namespace, e.Source, e.Target, e.Type, e.Weight, propBlob, e.DocID); err != nil {
			return fmt.Errorf("upsert edge %s->%s: %w", e.Source, e.Target, err)
		}
	}

	return tx.Commit()
}

// SearchNodes finds up to k nodes matching query via pgvector cosine similarity
// (when an embedding model is configured) or pg_trgm trigram similarity + ILIKE.
func (s *Store) SearchNodes(ctx context.Context, query string, k int, opts ...graphstore.SearchOption) ([]graphstore.Node, error) {
	if k <= 0 {
		return nil, nil
	}

	cfg := graphstore.ApplySearchOptions(opts...)
	typeSet := makeTypeSet(cfg.NodeTypes)

	if s.embed != nil {
		return s.searchSemantic(ctx, cfg, typeSet, query, k)
	}
	return s.searchTrigram(ctx, cfg, typeSet, query, k)
}

func (s *Store) searchSemantic(ctx context.Context, cfg graphstore.SearchConfig, typeSet map[string]struct{}, query string, k int) ([]graphstore.Node, error) {
	queryVec, err := s.embed.EmbedQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	querySQL := `
		SELECT id, label, type, properties, 1 - (embedding <=> $2::vector) AS score
		FROM graphstore_nodes
		WHERE namespace = $1 AND embedding IS NOT NULL`
	args := []any{cfg.Namespace, vspg.FormatVector(queryVec)}
	argIdx := 3

	if len(cfg.NodeTypes) > 0 {
		querySQL += fmt.Sprintf(` AND LOWER(type) = ANY($%d)`, argIdx)
		args = append(args, pq.Array(lowerSlice(cfg.NodeTypes)))
		argIdx++
	}
	if cfg.HasMinScore {
		querySQL += fmt.Sprintf(` AND 1 - (embedding <=> $2::vector) >= $%d`, argIdx)
		args = append(args, cfg.MinScore)
		argIdx++
	}
	querySQL += ` ORDER BY embedding <=> $2::vector ASC, id ASC`

	return s.scanSearchRows(ctx, querySQL, args, typeSet, cfg.Filter, k)
}

func (s *Store) searchTrigram(ctx context.Context, cfg graphstore.SearchConfig, typeSet map[string]struct{}, query string, k int) ([]graphstore.Node, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, nil
	}

	querySQL := `
		SELECT id, label, type, properties, GREATEST(similarity(label, $2), similarity(id, $2)) AS score
		FROM graphstore_nodes
		WHERE namespace = $1
		  AND (label % $2 OR label ILIKE $3 OR id ILIKE $3 OR $2 ILIKE '%' || label || '%')`
	args := []any{cfg.Namespace, trimmed, "%" + trimmed + "%"}
	argIdx := 4

	if len(cfg.NodeTypes) > 0 {
		querySQL += fmt.Sprintf(` AND LOWER(type) = ANY($%d)`, argIdx)
		args = append(args, pq.Array(lowerSlice(cfg.NodeTypes)))
		argIdx++
	}
	if cfg.HasMinScore {
		querySQL += fmt.Sprintf(` AND GREATEST(similarity(label, $2), similarity(id, $2)) >= $%d`, argIdx)
		args = append(args, cfg.MinScore)
		argIdx++
	}
	querySQL += ` ORDER BY score DESC, id ASC`

	return s.scanSearchRows(ctx, querySQL, args, typeSet, cfg.Filter, k)
}

func (s *Store) scanSearchRows(ctx context.Context, querySQL string, args []any, typeSet map[string]struct{}, filter map[string]any, k int) ([]graphstore.Node, error) {
	rows, err := s.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []graphstore.Node
	for rows.Next() {
		var n graphstore.Node
		var propBlob []byte
		var score float64
		if err := rows.Scan(&n.ID, &n.Label, &n.Type, &propBlob, &score); err != nil {
			return nil, err
		}
		if len(propBlob) > 0 {
			if err := json.Unmarshal(propBlob, &n.Properties); err != nil {
				return nil, fmt.Errorf("unmarshal node properties: %w", err)
			}
		}
		if !matchesNodeConstraints(n, typeSet, filter) {
			continue
		}
		out = append(out, n)
		if len(out) == k {
			break
		}
	}
	return out, rows.Err()
}

// Traverse performs a cycle-safe breadth-first expansion up to maxDepth hops
// using PostgreSQL 14+ WITH RECURSIVE ... SEARCH BREADTH FIRST ... CYCLE.
func (s *Store) Traverse(ctx context.Context, seedNodeIDs []string, maxDepth int, opts ...graphstore.TraverseOption) (graphstore.Subgraph, error) {
	if len(seedNodeIDs) == 0 || maxDepth < 0 {
		return graphstore.Subgraph{}, nil
	}

	cfg := graphstore.ApplyTraverseOptions(opts...)

	seedSet := make(map[string]struct{}, len(seedNodeIDs))
	var seeds []string
	for _, raw := range seedNodeIDs {
		id := graphstore.NormalizeID(raw)
		if id == "" {
			continue
		}
		if _, dup := seedSet[id]; dup {
			continue
		}
		seedSet[id] = struct{}{}
		seeds = append(seeds, id)
	}
	if len(seeds) == 0 {
		return graphstore.Subgraph{}, nil
	}

	existingSeeds, err := s.fetchNodesByID(ctx, cfg.Namespace, seeds)
	if err != nil {
		return graphstore.Subgraph{}, err
	}
	if len(existingSeeds) == 0 {
		return graphstore.Subgraph{}, nil
	}

	validSeeds := slices.Sorted(maps.Keys(existingSeeds))
	if len(validSeeds) > cfg.MaxNodes {
		validSeeds = validSeeds[:cfg.MaxNodes]
	}

	if maxDepth == 0 {
		nodes := make([]graphstore.Node, len(validSeeds))
		for i, id := range validSeeds {
			nodes[i] = existingSeeds[id]
		}
		return graphstore.Subgraph{Nodes: nodes}, nil
	}

	normEdgeTypes := make([]string, 0, len(cfg.EdgeTypes))
	for _, t := range cfg.EdgeTypes {
		if nt := graphstore.NormalizeEdgeType(t); nt != "" {
			normEdgeTypes = append(normEdgeTypes, nt)
		}
	}

	querySQL, args := buildTraverseSQL(cfg.Namespace, validSeeds, normEdgeTypes, cfg.Bidirectional, maxDepth, cfg.MaxEdges)
	rows, err := s.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return graphstore.Subgraph{}, err
	}
	defer rows.Close()

	nodeIDSet := make(map[string]struct{}, len(validSeeds))
	for _, id := range validSeeds {
		nodeIDSet[id] = struct{}{}
	}
	sourceIDSet := make(map[string]struct{})
	var edges []graphstore.Edge

	for rows.Next() {
		var e graphstore.Edge
		var propBlob []byte
		var minDepth int
		if err := rows.Scan(&e.Source, &e.Target, &e.Type, &e.Weight, &propBlob, &e.DocID, &minDepth); err != nil {
			return graphstore.Subgraph{}, err
		}
		if len(propBlob) > 0 {
			if err := json.Unmarshal(propBlob, &e.Properties); err != nil {
				return graphstore.Subgraph{}, fmt.Errorf("unmarshal edge properties: %w", err)
			}
		}

		newNodes := 0
		if _, ok := nodeIDSet[e.Source]; !ok {
			newNodes++
		}
		if _, ok := nodeIDSet[e.Target]; !ok && e.Target != e.Source {
			newNodes++
		}
		if len(nodeIDSet)+newNodes > cfg.MaxNodes {
			continue
		}

		nodeIDSet[e.Source] = struct{}{}
		nodeIDSet[e.Target] = struct{}{}
		edges = append(edges, e)
		if e.DocID != "" {
			sourceIDSet[e.DocID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return graphstore.Subgraph{}, err
	}

	allNodeIDs := slices.Sorted(maps.Keys(nodeIDSet))
	nodeMap, err := s.fetchNodesByID(ctx, cfg.Namespace, allNodeIDs)
	if err != nil {
		return graphstore.Subgraph{}, err
	}

	nodes := make([]graphstore.Node, 0, len(allNodeIDs))
	for _, id := range allNodeIDs {
		if n, ok := nodeMap[id]; ok {
			nodes = append(nodes, n)
		} else {
			nodes = append(nodes, graphstore.Node{ID: id, Label: id})
		}
	}

	return graphstore.Subgraph{
		Nodes:     nodes,
		Edges:     edges,
		SourceIDs: slices.Sorted(maps.Keys(sourceIDSet)),
	}, nil
}

// Delete removes nodes (and their incident edges) or provenance edges linked to doc IDs.
func (s *Store) Delete(ctx context.Context, ids []string, opts ...graphstore.DeleteOption) error {
	if len(ids) == 0 {
		return nil
	}

	cfg := graphstore.ApplyDeleteOptions(opts...)

	if cfg.ByDocID {
		_, err := s.db.ExecContext(ctx,
			`DELETE FROM graphstore_edges WHERE namespace = $1 AND doc_id = ANY($2)`,
			cfg.Namespace, pq.Array(ids),
		)
		return err
	}

	normIDs := make([]string, 0, len(ids))
	for _, raw := range ids {
		if id := graphstore.NormalizeID(raw); id != "" {
			normIDs = append(normIDs, id)
		}
	}
	if len(normIDs) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM graphstore_edges WHERE namespace = $1 AND (source = ANY($2) OR target = ANY($2))`,
		cfg.Namespace, pq.Array(normIDs),
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM graphstore_nodes WHERE namespace = $1 AND id = ANY($2)`,
		cfg.Namespace, pq.Array(normIDs),
	); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) fetchNodesByID(ctx context.Context, namespace string, ids []string) (map[string]graphstore.Node, error) {
	if len(ids) == 0 {
		return map[string]graphstore.Node{}, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, label, type, properties
		FROM graphstore_nodes
		WHERE namespace = $1 AND id = ANY($2)
	`, namespace, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]graphstore.Node, len(ids))
	for rows.Next() {
		var n graphstore.Node
		var propBlob []byte
		if err := rows.Scan(&n.ID, &n.Label, &n.Type, &propBlob); err != nil {
			return nil, err
		}
		if len(propBlob) > 0 {
			if err := json.Unmarshal(propBlob, &n.Properties); err != nil {
				return nil, fmt.Errorf("unmarshal node properties: %w", err)
			}
		}
		out[n.ID] = n
	}
	return out, rows.Err()
}

func buildTraverseSQL(namespace string, seeds, edgeTypes []string, bidirectional bool, maxDepth, maxEdges int) (string, []any) {
	args := []any{namespace, pq.Array(seeds), bidirectional, maxDepth, maxEdges}
	edgeFilter := ""
	if len(edgeTypes) > 0 {
		args = append(args, pq.Array(edgeTypes))
		edgeFilter = ` AND e.type = ANY($6)`
	}

	query := fmt.Sprintf(`WITH RECURSIVE traverse (source, target, type, weight, properties, doc_id, curr_node, depth) AS (
		SELECT e.source, e.target, e.type, e.weight, e.properties, e.doc_id,
		       CASE WHEN e.source = ANY($2) THEN e.target ELSE e.source END AS curr_node,
		       1 AS depth
		FROM graphstore_edges e
		WHERE e.namespace = $1
		  AND (e.source = ANY($2) OR ($3 AND e.target = ANY($2)))%s

		UNION ALL

		SELECT e.source, e.target, e.type, e.weight, e.properties, e.doc_id,
		       CASE WHEN e.source = t.curr_node THEN e.target ELSE e.source END AS curr_node,
		       t.depth + 1
		FROM graphstore_edges e
		JOIN traverse t
		  ON e.namespace = $1
		 AND (e.source = t.curr_node OR ($3 AND e.target = t.curr_node))
		WHERE t.depth < $4%s
	)
	SEARCH BREADTH FIRST BY source, target SET ordercol
	CYCLE curr_node SET is_cycle USING path
	SELECT source, target, type, weight, properties, doc_id, MIN(depth) AS min_depth
	FROM traverse
	WHERE NOT is_cycle
	GROUP BY source, target, type, weight, properties, doc_id
	ORDER BY min_depth ASC, source ASC, target ASC, type ASC, doc_id ASC
	LIMIT $5`, edgeFilter, edgeFilter)

	return query, args
}

func marshalJSONObject(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

func lowerSlice(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(strings.TrimSpace(s))
	}
	return out
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

func matchesNodeConstraints(node graphstore.Node, typeSet map[string]struct{}, filter map[string]any) bool {
	if len(typeSet) > 0 {
		if _, ok := typeSet[strings.ToLower(strings.TrimSpace(node.Type))]; !ok {
			return false
		}
	}
	return graphstore.MatchesProperties(node.Properties, filter)
}
