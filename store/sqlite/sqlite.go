package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/masterkeysrd/loom/store"
)

// Store is a SQLite-backed implementation of the [store.Store] interface.
// It is safe for concurrent use via sql.DB's internal locking.
type Store struct {
	db *sql.DB
}

// NewStore opens a connection to db, runs any pending schema migrations,
// and returns a ready-to-use [Store].
func NewStore(db *sql.DB) (*Store, error) {
	if err := Migrate(db); err != nil {
		return nil, err
	}

	return &Store{db: db}, nil
}

// Put marshals value to JSON and stores it under namespace/key.
// If the key already exists, it is overwritten (upsert semantics).
func (s *Store) Put(ctx context.Context, namespace, key string, value any, opts ...store.PutOption) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)

	query := `INSERT INTO store_items (namespace, key, value, created_at, updated_at)
              VALUES (?, ?, ?, ?, ?)
              ON CONFLICT (namespace, key) DO UPDATE
              SET value = EXCLUDED.value,
                  updated_at = EXCLUDED.updated_at`

	_, err = s.db.ExecContext(ctx, query, namespace, key, data, now, now)
	if err != nil {
		return fmt.Errorf("put %s/%s: %w", namespace, key, err)
	}

	return nil
}

// Get retrieves a single item by namespace/key and unmarshals into item.
// Returns store.ErrNotFound if the key does not exist.
func (s *Store) Get(ctx context.Context, namespace, key string, item any, opts ...store.GetOption) error {
	var value []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM store_items WHERE namespace = ? AND key = ?`,
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
// items must be a pointer to a slice. Options allow filtering (prefix, limit, offset).
func (s *Store) Search(ctx context.Context, namespace string, items any, opts ...store.SearchOption) error {
	cfg := &store.SearchConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	query := `SELECT key, value FROM store_items WHERE namespace = ?`
	args := []any{namespace}

	if cfg.Prefix != "" {
		query += ` AND key LIKE ?`
		args = append(args, cfg.Prefix+"%")
	}

	query += ` ORDER BY key ASC`

	if cfg.HasLim {
		query += ` LIMIT ?`
		args = append(args, cfg.Limit)
	}

	if cfg.HasOff {
		query += ` OFFSET ?`
		args = append(args, cfg.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("search %s: %w", namespace, err)
	}
	defer rows.Close()

	type keyed struct {
		key   string
		value []byte
	}
	var filtered []keyed
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		filtered = append(filtered, keyed{key: k, value: v})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Sort by key for deterministic ordering.
	slices.SortFunc(filtered, func(a, b keyed) int {
		return compare(a.key, b.key)
	})

	var result []json.RawMessage
	for _, k := range filtered {
		result = append(result, json.RawMessage(k.value))
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
		`DELETE FROM store_items WHERE namespace = ? AND key = ?`,
		namespace, key,
	)
	if err != nil {
		return fmt.Errorf("delete %s/%s: %w", namespace, key, err)
	}

	return nil
}

// compare returns -1, 0, or 1 comparing strings lexicographically.
func compare(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
