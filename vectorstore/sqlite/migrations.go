package sqlite

import "database/sql"

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS vectorstore_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
	);`,
	`PRAGMA journal_mode=WAL;`,
	`CREATE TABLE IF NOT EXISTS vectorstore_documents (
		namespace TEXT NOT NULL,
		id        TEXT NOT NULL,
		content   TEXT NOT NULL,
		metadata  BLOB,
		embedding BLOB NOT NULL,
		PRIMARY KEY (namespace, id)
	);`,
	`CREATE INDEX IF NOT EXISTS idx_vectorstore_documents_ns ON vectorstore_documents (namespace);`,
}

// Migrate applies all pending schema migrations for vectorstore to db.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(migrations[0]); err != nil {
		return err
	}

	var version int
	if err := db.QueryRow(`SELECT version FROM vectorstore_migrations ORDER BY version DESC LIMIT 1`).Scan(&version); err != nil {
		if err != sql.ErrNoRows {
			return err
		}
		version = -1
	}

	for i := version + 1; i < len(migrations); i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			return err
		}

		if _, err := db.Exec(`INSERT INTO vectorstore_migrations (version) VALUES (?)`, i); err != nil {
			return err
		}
	}

	return nil
}
