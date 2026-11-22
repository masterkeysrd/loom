package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"modernc.org/sqlite"

	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/graphstore"
	"github.com/masterkeysrd/loom/internal/bm25"
)

var _ graphstore.GraphStore = (*Store)(nil)

var registerVecOnce sync.Once

func ensureVecFunctionRegistered() {
	registerVecOnce.Do(func() {
		_ = sqlite.RegisterDeterministicScalarFunction(
			"loom_vec_cosine",
			2,
			func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
				if len(args) != 2 {
					return float64(-1), nil
				}
				a, ok1 := args[0].([]byte)
				b, ok2 := args[1].([]byte)
				if !ok1 || !ok2 {
					return float64(-1), nil
				}
				return embedding.CosineSimilarityBytes(a, b), nil
			},
		)
	})
}

// Option configures a SQLite [Store].
type Option func(*Store)

// WithEmbedding configures an optional embedding model for semantic node search.
func WithEmbedding(embed *embedding.Model) Option {
	return func(s *Store) {
		s.embed = embed
	}
}

// Store is a SQLite-backed implementation of [graphstore.GraphStore].
type Store struct {
	db    *sql.DB
	embed *embedding.Model
}

// New creates a new SQLite [Store] and applies pending migrations.
func New(db *sql.DB, opts ...Option) (*Store, error) {
	if db == nil {
		return nil, errors.New("graphstore/sqlite: db cannot be nil")
	}

	ensureVecFunctionRegistered()

	if err := Migrate(db); err != nil {
		return nil, err
	}

	s := &Store{db: db}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s, nil
}

// Upsert atomically merges nodes, edges, and document provenance links.
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

	// Merge with existing nodes so properties and labels are preserved across partial upserts.
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

	var nodeEmbeddings map[string][]byte
	if s.embed != nil && len(nodeIDs) > 0 {
		texts := make([]string, len(nodeIDs))
		for i, id := range nodeIDs {
			texts[i] = graphstore.NodeSearchText(mergedNodes[id])
		}
		vecs, err := s.embed.EmbedDocuments(ctx, texts)
		if err != nil {
			return err
		}
		nodeEmbeddings = make(map[string][]byte, len(nodeIDs))
		for i, id := range nodeIDs {
			nodeEmbeddings[id] = vecs[i].Bytes()
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	upsertNodeStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO graphstore_nodes (namespace, id, label, type, properties, embedding)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(namespace, id) DO UPDATE SET
			label      = excluded.label,
			type       = excluded.type,
			properties = excluded.properties,
			embedding  = COALESCE(excluded.embedding, graphstore_nodes.embedding)
	`)
	if err != nil {
		return err
	}
	defer upsertNodeStmt.Close()

	deleteFTSStmt, err := tx.PrepareContext(ctx, `DELETE FROM graphstore_nodes_fts WHERE namespace = ? AND node_id = ?`)
	if err != nil {
		return err
	}
	defer deleteFTSStmt.Close()

	insertFTSStmt, err := tx.PrepareContext(ctx, `INSERT INTO graphstore_nodes_fts (namespace, node_id, content) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insertFTSStmt.Close()

	for _, id := range nodeIDs {
		n := mergedNodes[id]
		var propBlob []byte
		if len(n.Properties) > 0 {
			propBlob, err = json.Marshal(n.Properties)
			if err != nil {
				return fmt.Errorf("marshal node properties %s: %w", id, err)
			}
		}
		var embBlob any
		if b, ok := nodeEmbeddings[id]; ok {
			embBlob = b
		}
		if _, err := upsertNodeStmt.ExecContext(ctx, cfg.Namespace, n.ID, n.Label, n.Type, propBlob, embBlob); err != nil {
			return fmt.Errorf("upsert node %s: %w", id, err)
		}
		if _, err := deleteFTSStmt.ExecContext(ctx, cfg.Namespace, n.ID); err != nil {
			return fmt.Errorf("clear fts node %s: %w", id, err)
		}
		if _, err := insertFTSStmt.ExecContext(ctx, cfg.Namespace, n.ID, graphstore.NodeSearchText(n)); err != nil {
			return fmt.Errorf("index fts node %s: %w", id, err)
		}
	}

	upsertEdgeStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO graphstore_edges (namespace, source, target, type, weight, properties, doc_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(namespace, source, target, type, doc_id) DO UPDATE SET
			weight     = excluded.weight,
			properties = excluded.properties
	`)
	if err != nil {
		return err
	}
	defer upsertEdgeStmt.Close()

	for _, e := range stagedEdges {
		var propBlob []byte
		if len(e.Properties) > 0 {
			propBlob, err = json.Marshal(e.Properties)
			if err != nil {
				return fmt.Errorf("marshal edge properties %s->%s: %w", e.Source, e.Target, err)
			}
		}
		if _, err := upsertEdgeStmt.ExecContext(ctx, cfg.Namespace, e.Source, e.Target, e.Type, e.Weight, propBlob, e.DocID); err != nil {
			return fmt.Errorf("upsert edge %s->%s: %w", e.Source, e.Target, err)
		}
	}

	return tx.Commit()
}

// SearchNodes finds up to k nodes matching query via semantic cosine similarity
// (when an embedding model is configured) or FTS5 trigram/BM25 + LIKE fallback.
func (s *Store) SearchNodes(ctx context.Context, query string, k int, opts ...graphstore.SearchOption) ([]graphstore.Node, error) {
	if k <= 0 {
		return nil, nil
	}

	cfg := graphstore.ApplySearchOptions(opts...)
	typeSet := makeTypeSet(cfg.NodeTypes)

	if s.embed != nil {
		return s.searchSemantic(ctx, cfg, typeSet, query, k)
	}
	return s.searchLexical(ctx, cfg, typeSet, query, k)
}

func (s *Store) searchSemantic(ctx context.Context, cfg graphstore.SearchConfig, typeSet map[string]struct{}, query string, k int) ([]graphstore.Node, error) {
	queryVec, err := s.embed.EmbedQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, label, type, properties, loom_vec_cosine(embedding, ?) AS score
		FROM graphstore_nodes
		WHERE namespace = ? AND embedding IS NOT NULL
		ORDER BY score DESC, id ASC
	`, queryVec.Bytes(), cfg.Namespace)
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
		if !matchesNodeConstraints(n, typeSet, cfg.Filter) {
			continue
		}
		if cfg.HasMinScore && float32(score) < cfg.MinScore {
			continue
		}
		out = append(out, n)
		if len(out) == k {
			break
		}
	}
	return out, rows.Err()
}

func (s *Store) searchLexical(ctx context.Context, cfg graphstore.SearchConfig, typeSet map[string]struct{}, query string, k int) ([]graphstore.Node, error) {
	seen := make(map[string]struct{}, k)
	var out []graphstore.Node

	if ftsQuery := buildFTS5Query(query); ftsQuery != "" {
		rows, err := s.db.QueryContext(ctx, `
			SELECT n.id, n.label, n.type, n.properties, -bm25(graphstore_nodes_fts) AS score
			FROM graphstore_nodes_fts f
			JOIN graphstore_nodes n ON n.namespace = f.namespace AND n.id = f.node_id
			WHERE f.namespace = ? AND graphstore_nodes_fts MATCH ?
			ORDER BY bm25(graphstore_nodes_fts) ASC, n.id ASC
		`, cfg.Namespace, ftsQuery)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var n graphstore.Node
			var propBlob []byte
			var score float64
			if err := rows.Scan(&n.ID, &n.Label, &n.Type, &propBlob, &score); err != nil {
				rows.Close()
				return nil, err
			}
			if len(propBlob) > 0 {
				if err := json.Unmarshal(propBlob, &n.Properties); err != nil {
					rows.Close()
					return nil, fmt.Errorf("unmarshal node properties: %w", err)
				}
			}
			if !matchesNodeConstraints(n, typeSet, cfg.Filter) {
				continue
			}
			if cfg.HasMinScore && float32(score) < cfg.MinScore {
				continue
			}
			seen[n.ID] = struct{}{}
			out = append(out, n)
			if len(out) == k {
				break
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	// Fallback substring match for short queries (< 3 chars) or partial matches.
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" || len(out) >= k {
		return out, nil
	}

	likePattern := "%" + needle + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, label, type, properties
		FROM graphstore_nodes
		WHERE namespace = ?
		  AND (LOWER(label) LIKE ? OR LOWER(id) LIKE ? OR instr(?, LOWER(label)) > 0)
		ORDER BY id ASC
	`, cfg.Namespace, likePattern, likePattern, needle)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var n graphstore.Node
		var propBlob []byte
		if err := rows.Scan(&n.ID, &n.Label, &n.Type, &propBlob); err != nil {
			return nil, err
		}
		if _, dup := seen[n.ID]; dup {
			continue
		}
		if len(propBlob) > 0 {
			if err := json.Unmarshal(propBlob, &n.Properties); err != nil {
				return nil, fmt.Errorf("unmarshal node properties: %w", err)
			}
		}
		if !matchesNodeConstraints(n, typeSet, cfg.Filter) {
			continue
		}
		seen[n.ID] = struct{}{}
		out = append(out, n)
		if len(out) == k {
			break
		}
	}
	return out, rows.Err()
}

// Traverse performs a cycle-safe breadth-first expansion up to maxDepth hops
// using a SQLite WITH RECURSIVE CTE.
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

		// Respect MaxNodes cap when adding newly reached endpoints.
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

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if cfg.ByDocID {
		stmt, err := tx.PrepareContext(ctx, `DELETE FROM graphstore_edges WHERE namespace = ? AND doc_id = ?`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, id := range ids {
			if _, err := stmt.ExecContext(ctx, cfg.Namespace, id); err != nil {
				return err
			}
		}
		return tx.Commit()
	}

	delNodeStmt, err := tx.PrepareContext(ctx, `DELETE FROM graphstore_nodes WHERE namespace = ? AND id = ?`)
	if err != nil {
		return err
	}
	defer delNodeStmt.Close()

	delFTSStmt, err := tx.PrepareContext(ctx, `DELETE FROM graphstore_nodes_fts WHERE namespace = ? AND node_id = ?`)
	if err != nil {
		return err
	}
	defer delFTSStmt.Close()

	delEdgeStmt, err := tx.PrepareContext(ctx, `DELETE FROM graphstore_edges WHERE namespace = ? AND (source = ? OR target = ?)`)
	if err != nil {
		return err
	}
	defer delEdgeStmt.Close()

	for _, rawID := range ids {
		id := graphstore.NormalizeID(rawID)
		if id == "" {
			continue
		}
		if _, err := delNodeStmt.ExecContext(ctx, cfg.Namespace, id); err != nil {
			return err
		}
		if _, err := delFTSStmt.ExecContext(ctx, cfg.Namespace, id); err != nil {
			return err
		}
		if _, err := delEdgeStmt.ExecContext(ctx, cfg.Namespace, id, id); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) fetchNodesByID(ctx context.Context, namespace string, ids []string) (map[string]graphstore.Node, error) {
	if len(ids) == 0 {
		return map[string]graphstore.Node{}, nil
	}

	placeholders := makePlaceholders(len(ids))
	args := make([]any, 0, 1+len(ids))
	args = append(args, namespace)
	for _, id := range ids {
		args = append(args, id)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, label, type, properties
		FROM graphstore_nodes
		WHERE namespace = ? AND id IN (`+placeholders+`)
	`, args...)
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
	seedPH := makePlaceholders(len(seeds))
	edgeFilter := ""
	if len(edgeTypes) > 0 {
		edgeFilter = ` AND e.type IN (` + makePlaceholders(len(edgeTypes)) + `)`
	}

	var args []any
	appendStrings := func(vals []string) {
		for _, v := range vals {
			args = append(args, v)
		}
	}

	var sb strings.Builder
	sb.WriteString(`WITH RECURSIVE hop_cte(source, target, type, weight, properties, doc_id, curr_node, depth, visited) AS (`)

	// Base outgoing from seeds.
	sb.WriteString(`
		SELECT e.source, e.target, e.type, e.weight, e.properties, e.doc_id,
		       e.target, 1, ',' || e.source || ',' || e.target || ','
		FROM graphstore_edges e
		WHERE e.namespace = ? AND e.source IN (` + seedPH + `)` + edgeFilter)
	args = append(args, namespace)
	appendStrings(seeds)
	appendStrings(edgeTypes)

	if bidirectional {
		sb.WriteString(`
		UNION ALL
		SELECT e.source, e.target, e.type, e.weight, e.properties, e.doc_id,
		       e.source, 1, ',' || e.target || ',' || e.source || ','
		FROM graphstore_edges e
		WHERE e.namespace = ? AND e.target IN (` + seedPH + `)` + edgeFilter)
		args = append(args, namespace)
		appendStrings(seeds)
		appendStrings(edgeTypes)
	}

	// Recursive outgoing expansion.
	sb.WriteString(`
		UNION ALL
		SELECT e.source, e.target, e.type, e.weight, e.properties, e.doc_id,
		       e.target, h.depth + 1, h.visited || e.target || ','
		FROM graphstore_edges e
		JOIN hop_cte h ON e.source = h.curr_node
		WHERE e.namespace = ? AND h.depth < ? AND instr(h.visited, ',' || e.target || ',') = 0` + edgeFilter)
	args = append(args, namespace, maxDepth)
	appendStrings(edgeTypes)

	if bidirectional {
		sb.WriteString(`
		UNION ALL
		SELECT e.source, e.target, e.type, e.weight, e.properties, e.doc_id,
		       e.source, h.depth + 1, h.visited || e.source || ','
		FROM graphstore_edges e
		JOIN hop_cte h ON e.target = h.curr_node
		WHERE e.namespace = ? AND h.depth < ? AND instr(h.visited, ',' || e.source || ',') = 0` + edgeFilter)
		args = append(args, namespace, maxDepth)
		appendStrings(edgeTypes)
	}

	sb.WriteString(`
	)
	SELECT source, target, type, weight, properties, doc_id, MIN(depth) AS min_depth
	FROM hop_cte
	GROUP BY source, target, type, doc_id
	ORDER BY min_depth ASC, source ASC, target ASC, type ASC, doc_id ASC
	LIMIT ?`)
	args = append(args, maxEdges)

	return sb.String(), args
}

func buildFTS5Query(query string) string {
	tokens := bm25.DefaultTokenizer(query)
	var parts []string
	seen := make(map[string]struct{}, len(tokens))
	for _, tok := range tokens {
		if len([]rune(tok)) < 3 {
			continue
		}
		if _, dup := seen[tok]; dup {
			continue
		}
		seen[tok] = struct{}{}
		escaped := strings.ReplaceAll(tok, `"`, `""`)
		parts = append(parts, `"`+escaped+`"`)
	}
	return strings.Join(parts, " OR ")
}

func makePlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
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
