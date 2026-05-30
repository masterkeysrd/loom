// Package sqlite provides a SQLite-backed implementation of [graph.Checkpointer].
//
// [Checkpointer] persists graph execution snapshots in a checkpoints table,
// enabling durable pause-and-resume workflows across process restarts or
// planned interrupts. Schema management is handled automatically via a
// sequential migration runner ([Migrate]) that tracks applied versions in a
// checkpoint_migrations table.
//
// # Schema
//
// The checkpoints table uses a composite primary key of
// (thread_id, checkpoint_ns, checkpoint_id), where checkpoint_id is a
// UUIDv7 that encodes creation time. This ordering makes it possible to
// retrieve the latest checkpoint for a thread/namespace in a single
// ORDER BY + LIMIT query without a separate timestamp index.
//
// # Usage
//
//	db, err := sql.Open("sqlite3", dsn)
//	if err != nil { ... }
//
//	cp, err := sqlite.NewCheckpointer(db)
//	if err != nil { ... }
//
//	g, err := graph.New[MyState]().
//	    WithCheckpointer(cp).
//	    // ... add nodes and edges ...
package sqlite
