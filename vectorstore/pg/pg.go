// Package pg provides a PostgreSQL-backed implementation of [vectorstore.VectorStore]
// using the pgvector extension.
package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/vectorstore"
)

var _ vectorstore.VectorStore = (*Store)(nil)

// Option configures a PostgreSQL [Store].
type Option func(*Store)

// WithDims configures the expected vector dimensionality for migrations.
func WithDims(dims int) Option {
	return func(s *Store) {
		s.dims = dims
	}
}

// Store is a PostgreSQL-backed vector store using pgvector.
type Store struct {
	db    *sql.DB
	embed *embedding.Model
	dims  int
}

// New creates a new PostgreSQL [Store] and applies pending migrations.
func New(db *sql.DB, embed *embedding.Model, opts ...Option) (*Store, error) {
	if db == nil {
		return nil, errors.New("vectorstore/pg: db cannot be nil")
	}

	s := &Store{
		db:    db,
		embed: embed,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}

	dims := vectorstore.ResolveDims(embed, s.dims)
	s.dims = dims

	if err := Migrate(db, dims); err != nil {
		return nil, err
	}

	return s, nil
}

// Add inserts or updates documents in the vector store under the specified namespace.
func (s *Store) Add(ctx context.Context, docs []document.Document, opts ...vectorstore.AddOption) ([]string, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	if s.embed == nil {
		return nil, errors.New("vectorstore/pg: embedding model is nil")
	}

	cfg := vectorstore.ApplyAddOptions(opts...)

	texts := make([]string, len(docs))
	for i, doc := range docs {
		texts[i] = doc.Content
	}

	vectors, err := s.embed.EmbedDocuments(ctx, texts)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO vectorstore_documents (namespace, id, content, metadata, embedding)
		VALUES ($1, $2, $3, $4, $5::vector)
		ON CONFLICT (namespace, id) DO UPDATE SET
			content   = EXCLUDED.content,
			metadata  = EXCLUDED.metadata,
			embedding = EXCLUDED.embedding
	`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	ids := make([]string, len(docs))
	for i, doc := range docs {
		id := doc.ID
		if id == "" {
			id = uuid.NewString()
		}
		ids[i] = id

		metaBlob := []byte("{}")
		if len(doc.Metadata) > 0 {
			metaBlob, err = json.Marshal(doc.Metadata)
			if err != nil {
				return nil, fmt.Errorf("marshal metadata: %w", err)
			}
		}

		vecStr := FormatVector(vectors[i])

		if _, err := stmt.ExecContext(ctx, cfg.Namespace, id, doc.Content, metaBlob, vecStr); err != nil {
			return nil, fmt.Errorf("upsert document %s: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return ids, nil
}

// Search queries the vector store for the top k documents most similar to query.
func (s *Store) Search(ctx context.Context, query string, k int, opts ...vectorstore.SearchOption) ([]document.Document, error) {
	if k <= 0 {
		return nil, nil
	}
	if s.embed == nil {
		return nil, errors.New("vectorstore/pg: embedding model is nil")
	}

	cfg := vectorstore.ApplySearchOptions(opts...)

	queryVec, err := s.embed.EmbedQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	fetchK := k
	if cfg.UseMMR {
		fetchK = cfg.MMRFetchK
		if fetchK < k {
			fetchK = k * 4
		}
	}

	queryVecStr := FormatVector(queryVec)

	selectCols := `id, content, metadata, 1 - (embedding <=> $2::vector) AS score`
	if cfg.UseMMR {
		selectCols += `, embedding::text`
	}
	querySQL := fmt.Sprintf(`SELECT %s FROM vectorstore_documents WHERE namespace = $1`, selectCols)
	args := []any{cfg.Namespace, queryVecStr}
	argIdx := 3

	if len(cfg.IDs) > 0 {
		querySQL += fmt.Sprintf(` AND id = ANY($%d)`, argIdx)
		args = append(args, pq.Array(cfg.IDs))
		argIdx++
	}

	if len(cfg.Filter) > 0 {
		clause, err := buildPGFilterClause(cfg.Filter, &args, &argIdx)
		if err != nil {
			return nil, err
		}
		querySQL += clause
	}

	if cfg.HasMinScore {
		querySQL += fmt.Sprintf(` AND 1 - (embedding <=> $2::vector) >= $%d`, argIdx)
		args = append(args, cfg.MinScore)
		argIdx++
	}

	querySQL += fmt.Sprintf(` ORDER BY embedding <=> $2::vector ASC, id ASC LIMIT $%d`, argIdx)
	args = append(args, fetchK)

	rows, err := s.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var (
		candidateDocs []document.Document
		candidateVecs []embedding.Vector
	)
	for rows.Next() {
		var id, content string
		var metaBlob []byte
		var score float64
		var embText string

		var scanErr error
		if cfg.UseMMR {
			scanErr = rows.Scan(&id, &content, &metaBlob, &score, &embText)
		} else {
			scanErr = rows.Scan(&id, &content, &metaBlob, &score)
		}
		if scanErr != nil {
			return nil, scanErr
		}

		var meta map[string]any
		if len(metaBlob) > 0 {
			if err := json.Unmarshal(metaBlob, &meta); err != nil {
				return nil, fmt.Errorf("unmarshal metadata: %w", err)
			}
		}

		doc := document.Document{
			ID:       id,
			Content:  content,
			Metadata: meta,
			Score:    float32(score),
		}
		candidateDocs = append(candidateDocs, doc)
		if cfg.UseMMR {
			candidateVecs = append(candidateVecs, ParseVector(embText))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if cfg.UseMMR {
		return vectorstore.SelectMMR(queryVec, candidateDocs, candidateVecs, cfg.MMRLambda, k), nil
	}

	return candidateDocs, nil
}

// Get retrieves documents matching the provided IDs within the specified namespace.
// Returned documents preserve the deduplicated order of ids and satisfy any configured Filter.
func (s *Store) Get(ctx context.Context, ids []string, opts ...vectorstore.SearchOption) ([]document.Document, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	cfg := vectorstore.ApplySearchOptions(opts...)

	seen := make(map[string]struct{}, len(ids))
	uniqueIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}

	querySQL := `SELECT id, content, metadata FROM vectorstore_documents WHERE namespace = $1 AND id = ANY($2)`
	args := []any{cfg.Namespace, pq.Array(uniqueIDs)}
	argIdx := 3

	if len(cfg.Filter) > 0 {
		clause, err := buildPGFilterClause(cfg.Filter, &args, &argIdx)
		if err != nil {
			return nil, err
		}
		querySQL += clause
	}

	rows, err := s.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := make(map[string]document.Document, len(uniqueIDs))
	for rows.Next() {
		var id, content string
		var metaBlob []byte
		if err := rows.Scan(&id, &content, &metaBlob); err != nil {
			return nil, err
		}

		var meta map[string]any
		if len(metaBlob) > 0 {
			if err := json.Unmarshal(metaBlob, &meta); err != nil {
				return nil, fmt.Errorf("unmarshal metadata: %w", err)
			}
		}

		byID[id] = document.Document{
			ID:       id,
			Content:  content,
			Metadata: meta,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	results := make([]document.Document, 0, len(byID))
	for _, id := range uniqueIDs {
		if doc, ok := byID[id]; ok {
			results = append(results, doc)
		}
	}
	return results, nil
}

func buildPGFilterClause(filter map[string]any, args *[]any, argIdx *int) (string, error) {
	keys := slices.Sorted(maps.Keys(filter))
	var sb strings.Builder
	for _, k := range keys {
		want := filter[k]
		keyIdx := *argIdx
		*args = append(*args, k)
		*argIdx++

		if sliceVals, ok := sliceFilterValues(want); ok {
			if len(sliceVals) == 0 {
				sb.WriteString(fmt.Sprintf(` AND metadata->$%d = '[]'::jsonb`, keyIdx))
				continue
			}
			valJSON, err := json.Marshal(sliceVals)
			if err != nil {
				return "", fmt.Errorf("marshal filter slice %q: %w", k, err)
			}
			valIdx := *argIdx
			*args = append(*args, valJSON)
			*argIdx++
			sb.WriteString(fmt.Sprintf(
				` AND ((jsonb_typeof(metadata->$%d) = 'array' AND EXISTS (SELECT 1 FROM jsonb_array_elements(metadata->$%d) AS elem WHERE EXISTS (SELECT 1 FROM jsonb_array_elements($%d::jsonb) AS w WHERE elem = w))) OR (jsonb_typeof(metadata->$%d) != 'array' AND EXISTS (SELECT 1 FROM jsonb_array_elements($%d::jsonb) AS w WHERE metadata->$%d = w)))`,
				keyIdx, keyIdx, valIdx, keyIdx, valIdx, keyIdx,
			))
			continue
		}

		valJSON, err := json.Marshal(want)
		if err != nil {
			return "", fmt.Errorf("marshal filter %q: %w", k, err)
		}
		valIdx := *argIdx
		*args = append(*args, valJSON)
		*argIdx++
		sb.WriteString(fmt.Sprintf(
			` AND ((jsonb_typeof(metadata->$%d) = 'array' AND metadata->$%d @> jsonb_build_array($%d::jsonb)) OR (metadata->$%d = $%d::jsonb))`,
			keyIdx, keyIdx, valIdx, keyIdx, valIdx,
		))
	}
	return sb.String(), nil
}

func sliceFilterValues(v any) ([]any, bool) {
	if v == nil {
		return nil, false
	}
	switch s := v.(type) {
	case []byte:
		return nil, false
	case []any:
		return s, true
	case []string:
		out := make([]any, len(s))
		for i, item := range s {
			out[i] = item
		}
		return out, true
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice {
		return nil, false
	}
	out := make([]any, rv.Len())
	for i := range rv.Len() {
		out[i] = rv.Index(i).Interface()
	}
	return out, true
}

// Delete removes documents matching the provided IDs from the specified namespace.
func (s *Store) Delete(ctx context.Context, ids []string, opts ...vectorstore.DeleteOption) error {
	if len(ids) == 0 {
		return nil
	}

	cfg := vectorstore.ApplyDeleteOptions(opts...)

	_, err := s.db.ExecContext(ctx,
		`DELETE FROM vectorstore_documents WHERE namespace = $1 AND id = ANY($2)`,
		cfg.Namespace, pq.Array(ids),
	)
	return err
}

// FormatVector formats an [embedding.Vector] into pgvector's text literal syntax (e.g., "[0.1,0.2,0.3]").
func FormatVector(vec embedding.Vector) string {
	if len(vec) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.Grow(len(vec)*10 + 2)
	b.WriteByte('[')
	for i, f := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// ParseVector parses a pgvector text representation (e.g., "[0.1,0.2,0.3]") into an [embedding.Vector].
func ParseVector(s string) embedding.Vector {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	vec := make(embedding.Vector, len(parts))
	for i, part := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(part), 32)
		if err != nil {
			return nil
		}
		vec[i] = float32(f)
	}
	return vec
}
