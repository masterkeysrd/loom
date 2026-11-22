package sqlite

import (
	"database/sql"
	"fmt"
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS graphstore_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
	);`,
	`PRAGMA journal_mode=WAL;`,
	`CREATE TABLE IF NOT EXISTS graphstore_nodes (
		namespace  TEXT NOT NULL,
		id         TEXT NOT NULL,
		label      TEXT NOT NULL,
		type       TEXT NOT NULL DEFAULT '',
		properties BLOB,
		embedding  BLOB,
		PRIMARY KEY (namespace, id)
	);`,
	`CREATE INDEX IF NOT EXISTS idx_graphstore_nodes_ns_type ON graphstore_nodes (namespace, type);`,
	`CREATE TABLE IF NOT EXISTS graphstore_edges (
		namespace  TEXT NOT NULL,
		source     TEXT NOT NULL,
		target     TEXT NOT NULL,
		type       TEXT NOT NULL,
		weight     REAL NOT NULL DEFAULT 1.0,
		properties BLOB,
		doc_id     TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (namespace, source, target, type, doc_id)
	);`,
	`CREATE INDEX IF NOT EXISTS idx_graphstore_edges_ns_src ON graphstore_edges (namespace, source);`,
	`CREATE INDEX IF NOT EXISTS idx_graphstore_edges_ns_tgt ON graphstore_edges (namespace, target);`,
	`CREATE INDEX IF NOT EXISTS idx_graphstore_edges_ns_doc ON graphstore_edges (namespace, doc_id);`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS graphstore_nodes_fts USING fts5(
		namespace UNINDEXED,
		node_id UNINDEXED,
		content,
		tokenize='trigram'
	);`,
}

// Migrate applies all pending schema migrations for graphstore to db.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(migrations[0]); err != nil {
		return err
	}

	var version int
	if err := db.QueryRow(`SELECT version FROM graphstore_migrations ORDER BY version DESC LIMIT 1`).Scan(&version); err != nil {
		if err != sql.ErrNoRows {
			return err
		}
		version = -1
	}

	for i := version + 1; i < len(migrations); i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			return fmt.Errorf("migrate graphstore sqlite v%d: %w", i, err)
		}
		if _, err := db.Exec(`INSERT INTO graphstore_migrations (version) VALUES (?)`, i); err != nil {
			return fmt.Errorf("record graphstore sqlite migration v%d: %w", i, err)
		}
	}

	return nil
}
