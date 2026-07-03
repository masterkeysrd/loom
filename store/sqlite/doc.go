// Package sqlite provides a SQLite-backed implementation of [store.Store].
//
// [Store] persists namespaced key-value data in a store_items table,
// enabling durable long-term memory across process restarts. Schema management
// is handled automatically via a sequential migration runner ([Migrate]) that
// tracks applied versions in a store_migrations table.
//
// # Schema
//
// The store_items table uses a composite primary key of (namespace, key),
// with separate columns for value (JSON blob), created_at, and updated_at
// timestamps. An index on namespace supports efficient namespace-scoped
// search queries.
//
// # Usage
//
//	db, err := sql.Open("sqlite", dsn)
//	if err != nil { ... }
//
//	s, err := sqlite.NewStore(db)
//	if err != nil { ... }
//
//	var prefs UserPrefs
//	if err := s.Get(ctx, "user:123", "preferences", &prefs); err != nil { ... }
package sqlite
