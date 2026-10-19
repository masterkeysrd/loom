package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/store"
)

// Option configures a [Store] instance.
type Option func(*Store)

// WithIndex configures semantic vector indexing for the PostgreSQL store using pgvector.
func WithIndex(cfg store.IndexConfig) Option {
	return func(s *Store) {
		s.index = cfg
	}
}

// Store is a PostgreSQL-backed implementation of the [store.Store] interface.
// When configured with [WithIndex], it uses the pgvector extension to store and
// query embeddings by cosine similarity.
type Store struct {
	db    *sql.DB
	index store.IndexConfig
}

// NewStore opens a connection to db, runs any pending schema migrations,
// and returns a ready-to-use [Store].
func NewStore(db *sql.DB, opts ...Option) (*Store, error) {
	if err := Migrate(db); err != nil {
		return nil, err
	}

	s := &Store{db: db}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}

	if store.IndexEnabled(s.index) {
		if err := MigrateVector(db, store.ResolveIndexDims(s.index)); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// Put marshals value to JSON and stores it under namespace/key.
// If the key already exists, it is overwritten (upsert semantics).
func (s *Store) Put(ctx context.Context, namespace, key string, value any, opts ...store.PutOption) error {
	putCfg := &store.PutConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(putCfg)
		}
	}

	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	var vec embedding.Vector
	if store.IndexEnabled(s.index) {
		vec, err = store.EmbedValue(ctx, s.index, data, putCfg)
		if err != nil {
			return err
		}
	}

	now := time.Now().UTC()

	query := `INSERT INTO store_items (namespace, key, value, created_at, updated_at)
	          VALUES ($1, $2, $3, $4, $5)
	          ON CONFLICT (namespace, key) DO UPDATE
	          SET value = EXCLUDED.value,
	              updated_at = EXCLUDED.updated_at`

	if _, err := s.db.ExecContext(ctx, query, namespace, key, data, now, now); err != nil {
		return fmt.Errorf("put %s/%s: %w", namespace, key, err)
	}

	if store.IndexEnabled(s.index) {
		if len(vec) > 0 {
			vecQuery := `INSERT INTO store_embeddings (namespace, key, embedding)
			             VALUES ($1, $2, $3::vector)
			             ON CONFLICT (namespace, key) DO UPDATE
			             SET embedding = EXCLUDED.embedding`
			if _, err := s.db.ExecContext(ctx, vecQuery, namespace, key, FormatVector(vec)); err != nil {
				return fmt.Errorf("put embedding %s/%s: %w", namespace, key, err)
			}
		} else {
			if _, err := s.db.ExecContext(ctx, `DELETE FROM store_embeddings WHERE namespace = $1 AND key = $2`, namespace, key); err != nil {
				return fmt.Errorf("delete stale embedding %s/%s: %w", namespace, key, err)
			}
		}
	}

	return nil
}

// Get retrieves a single item by namespace/key and unmarshals into item.
// Returns store.ErrNotFound if the key does not exist.
func (s *Store) Get(ctx context.Context, namespace, key string, item any, opts ...store.GetOption) error {
	var value []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM store_items WHERE namespace = $1 AND key = $2`,
		namespace, key,
	).Scan(&value)

	if err != nil {
		if err == sql.ErrNoRows {
			return store.ErrNotFound
		}
		return fmt.Errorf("get %s/%s: %w", namespace, key, err)
	}

	return json.Unmarshal(value, item)
}

// Search finds all items within a namespace and unmarshals into items.
// items must be a pointer to a slice. Options allow filtering (prefix, query, limit, offset).
func (s *Store) Search(ctx context.Context, namespace string, items any, opts ...store.SearchOption) error {
	cfg := &store.SearchConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}

	var (
		query  string
		args   []any
		argIdx int
	)

	if cfg.Query != "" {
		vec, err := store.EmbedQuery(ctx, s.index, cfg.Query)
		if err != nil {
			return err
		}
		queryVecStr := FormatVector(vec)

		query = `SELECT i.key, i.value
		         FROM store_embeddings e
		         JOIN store_items i ON i.namespace = e.namespace AND i.key = e.key
		         WHERE e.namespace = $1`
		args = []any{namespace}
		argIdx = 2

		if cfg.Prefix != "" {
			query += fmt.Sprintf(` AND e.key LIKE $%d`, argIdx)
			args = append(args, cfg.Prefix+"%")
			argIdx++
		}

		query += fmt.Sprintf(` ORDER BY e.embedding <=> $%d::vector ASC, e.key ASC`, argIdx)
		args = append(args, queryVecStr)
		argIdx++
	} else {
		query = `SELECT key, value FROM store_items WHERE namespace = $1`
		args = []any{namespace}
		argIdx = 2

		if cfg.Prefix != "" {
			query += fmt.Sprintf(` AND key LIKE $%d`, argIdx)
			args = append(args, cfg.Prefix+"%")
			argIdx++
		}

		query += ` ORDER BY key ASC`
	}

	if cfg.HasLim {
		query += fmt.Sprintf(` LIMIT $%d`, argIdx)
		args = append(args, cfg.Limit)
		argIdx++
	}

	if cfg.HasOff {
		query += fmt.Sprintf(` OFFSET $%d`, argIdx)
		args = append(args, cfg.Offset)
		argIdx++
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("search %s: %w", namespace, err)
	}
	defer rows.Close()

	var result []json.RawMessage
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		result = append(result, json.RawMessage(v))
	}
	if err := rows.Err(); err != nil {
		return err
	}

	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}

	return json.Unmarshal(raw, items)
}

// Delete removes an item by namespace/key.
// Returns nil if the key does not exist (idempotent).
func (s *Store) Delete(ctx context.Context, namespace, key string, opts ...store.DeleteOption) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM store_items WHERE namespace = $1 AND key = $2`,
		namespace, key,
	)
	if err != nil {
		return fmt.Errorf("delete %s/%s: %w", namespace, key, err)
	}

	return nil
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
