package store

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a requested key does not exist in the store.
var ErrNotFound = errors.New("store: item not found")

// Store is the core key-value interface. Callers pass a pointer and the
// store populates it (GORM/json.Unmarshal style). No json.RawMessage
// leaks into the public API.
type Store interface {
	// Put marshals value to JSON and stores it under namespace/key.
	// If the key already exists, it is overwritten (upsert semantics).
	Put(ctx context.Context, namespace, key string, value any, opts ...PutOption) error

	// Get retrieves a single item by namespace/key and unmarshals into item.
	// item must be a non-nil pointer. Returns ErrNotFound if the key does not exist.
	Get(ctx context.Context, namespace, key string, item any, opts ...GetOption) error

	// Search finds all items within a namespace and unmarshals into items.
	// items must be a pointer to a slice. Options allow filtering (prefix, limit, offset).
	Search(ctx context.Context, namespace string, items any, opts ...SearchOption) error

	// Delete removes an item by namespace/key.
	// Returns nil if the key does not exist (idempotent).
	Delete(ctx context.Context, namespace, key string, opts ...DeleteOption) error
}
