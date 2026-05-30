package pg

import "database/sql"

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS checkpoint_migrations (
		version INT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	`,
	`
	CREATE TABLE IF NOT EXISTS checkpoints (
		thread_id TEXT NOT NULL,
		checkpoint_ns TEXT NOT NULL,
		checkpoint_id TEXT NOT NULL,
		parent_checkpoint_id TEXT,
		state JSONB NOT NULL,
		next JSONB NOT NULL,
		timestamp TIMESTAMPTZ NOT NULL,
		PRIMARY KEY (thread_id, checkpoint_ns, checkpoint_id)
	);
	`,
}

// Migrate applies all pending schema migrations to db.
// It maintains a checkpoint_migrations table to track which migrations have
// already been applied, making it safe to call on every application start.
func Migrate(db *sql.DB) error {
	// Ensure the migrations table exists
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

		if _, err := db.Exec(`INSERT INTO checkpoint_migrations (version) VALUES ($1)`, i); err != nil {
			return err
		}
	}

	return nil
}
