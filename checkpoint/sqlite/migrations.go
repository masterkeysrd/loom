package sqlite

import "database/sql"

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS checkpoint_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
	);`,
	`PRAGMA journal_mode=WAL;`,
	`CREATE TABLE IF NOT EXISTS checkpoints (
		thread_id TEXT NOT NULL,
		checkpoint_ns TEXT NOT NULL DEFAULT '',
		checkpoint_id TEXT NOT NULL,
		parent_checkpoint_id TEXT,
		checkpoint BLOB,
		metadata BLOB,
		PRIMARY KEY (thread_id, checkpoint_ns, checkpoint_id)
	);`,
	`CREATE TABLE IF NOT EXISTS checkpoint_blobs (
		thread_id TEXT NOT NULL,
		checkpoint_ns TEXT NOT NULL DEFAULT '',
		blob_id TEXT NOT NULL,
		value BLOB,
		PRIMARY KEY (thread_id, checkpoint_ns, blob_id)
	);`,
	`CREATE TABLE IF NOT EXISTS checkpoint_writes (
		thread_id TEXT NOT NULL,
		checkpoint_ns TEXT NOT NULL DEFAULT '',
		checkpoint_id TEXT NOT NULL,
		channel TEXT NOT NULL,
		blob_id TEXT NOT NULL,
		PRIMARY KEY (thread_id, checkpoint_ns, checkpoint_id, channel)
	);`,
}

// Migrate applies all pending schema migrations to db.
// It maintains a checkpoint_migrations table to track which migrations have
// already been applied, making it safe to call on every application start.
func Migrate(db *sql.DB) error {
	// Ensure the migrations table exists.
	if _, err := db.Exec(migrations[0]); err != nil {
		return err
	}

	var version int
	if err := db.QueryRow(`SELECT version FROM checkpoint_migrations ORDER BY version DESC LIMIT 1`).Scan(&version); err != nil {
		if err != sql.ErrNoRows {
			return err
		}

		version = -1
	}

	for i := version + 1; i < len(migrations); i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			return err
		}

		if _, err := db.Exec(`INSERT INTO checkpoint_migrations (version) VALUES (?)`, i); err != nil {
			return err
		}
	}

	return nil
}
