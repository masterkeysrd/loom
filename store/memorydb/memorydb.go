package memorydb

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/store"
)

// entry holds the serialized value, optional embedding vector, and timestamps for a single key.
type entry struct {
	Value     json.RawMessage
	Embedding embedding.Vector
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Option configures a [Store] instance.
type Option func(*Store)

// WithIndex configures semantic vector indexing for the in-memory store.
func WithIndex(cfg store.IndexConfig) Option {
	return func(s *Store) {
		s.index = cfg
	}
}

// Store is an in-memory implementation of the [store.Store] interface.
// It is safe for concurrent use via sync.RWMutex.
type Store struct {
	mu    sync.RWMutex
	items map[string]map[string]entry // namespace → key → entry
	index store.IndexConfig
}

// New creates a new empty in-memory store with optional configuration.
func New(opts ...Option) *Store {
	s := &Store{
		items: make(map[string]map[string]entry),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
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

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.items[namespace] == nil {
		s.items[namespace] = make(map[string]entry)
	}

	existing, has := s.items[namespace][key]
	if has {
		existing.Value = data
		existing.Embedding = vec
		existing.UpdatedAt = now
		s.items[namespace][key] = existing
	} else {
		s.items[namespace][key] = entry{
			Value:     data,
			Embedding: vec,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}

	return nil
}

// Get retrieves a single item by namespace/key and unmarshals into item.
// Returns store.ErrNotFound if the key does not exist.
func (s *Store) Get(ctx context.Context, namespace, key string, item any, opts ...store.GetOption) error {
	s.mu.RLock()
	e, ok := s.items[namespace][key]
	s.mu.RUnlock()

	if !ok {
		return store.ErrNotFound
	}

	return json.Unmarshal(e.Value, item)
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

	var queryVec embedding.Vector
	if cfg.Query != "" {
		var err error
		queryVec, err = store.EmbedQuery(ctx, s.index, cfg.Query)
		if err != nil {
			return err
		}
	}

	s.mu.RLock()
	nsEntries := s.items[namespace]

	// Collect keyed entries, filtering by prefix and semantic query if set.
	type keyed struct {
		key   string
		entry entry
		score float32
	}
	var filtered []keyed
	for k, e := range nsEntries {
		if cfg.Prefix != "" && (len(k) < len(cfg.Prefix) || k[:len(cfg.Prefix)] != cfg.Prefix) {
			continue
		}
		if cfg.Query != "" {
			if len(e.Embedding) == 0 {
				continue
			}
			filtered = append(filtered, keyed{
				key:   k,
				entry: e,
				score: queryVec.CosineSimilarity(e.Embedding),
			})
		} else {
			filtered = append(filtered, keyed{key: k, entry: e})
		}
	}
	s.mu.RUnlock()

	if cfg.Query != "" {
		// Sort by similarity score descending, breaking ties by key ascending.
		slices.SortFunc(filtered, func(a, b keyed) int {
			if a.score > b.score {
				return -1
			}
			if a.score < b.score {
				return 1
			}
			return compare(a.key, b.key)
		})
	} else {
		// Sort by key for deterministic ordering.
		slices.SortFunc(filtered, func(a, b keyed) int {
			return compare(a.key, b.key)
		})
	}

	// Apply offset and limit.
	start := 0
	if cfg.HasOff {
		start = cfg.Offset
	}
	end := len(filtered)
	if cfg.HasLim {
		end = min(start+cfg.Limit, len(filtered))
	}
	if start >= len(filtered) {
		filtered = nil
	} else {
		filtered = filtered[start:end]
	}

	// Unmarshal each entry's JSON value into the caller's slice.
	var result []json.RawMessage
	for _, k := range filtered {
		result = append(result, k.entry.Value)
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
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.items[namespace] != nil {
		delete(s.items[namespace], key)
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
