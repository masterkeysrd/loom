// Package store provides a namespaced key-value store for persistent,
// searchable long-term memory. It follows the GORM/json.Unmarshal pattern:
// callers pass a pointer and the store populates it.
//
// # Core Interface
//
// The [Store] interface exposes Put, Get, Search, and Delete operations
// over a namespace/key address space. All values are JSON-serialized
// internally; callers pass Go pointers and the store marshals/unmarshals
// automatically.
//
// # Usage
//
//	s := store.FromContext(ctx)
//
//	var prefs UserPrefs
//	if err := s.Get(ctx, "user:123", "preferences", &prefs); err != nil { ... }
//
//	// Generic-style helper
//	memories, err := store.Get[Memories](ctx, s, "user:123", "memories")
//
// # Backends
//
// Two backend implementations are provided:
//
//   - [memorydb.New] — in-memory store for development and testing.
//   - [sqlite.NewStore] — persistent SQLite backend for production.
package store
