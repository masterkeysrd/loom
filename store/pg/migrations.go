package pg

import (
	"database/sql"
	"fmt"
)

const (
	componentCore   = "core"
	componentVector = "vector"

	migrationsTableDDL = `CREATE TABLE IF NOT EXISTS store_migrations (
		component  TEXT NOT NULL DEFAULT 'core',
		version    INT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (component, version)
	);`
)

var coreMigrations = []string{
	`CREATE TABLE IF NOT EXISTS store_items (
		namespace  TEXT NOT NULL,
		key        TEXT NOT NULL,
		value      JSONB NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (namespace, key)
	);`,
	`CREATE INDEX IF NOT EXISTS idx_store_items_namespace ON store_items (namespace);`,
}

func vectorMigrations(dims int) []string {
	colType := "vector"
	if dims > 0 {
		colType = fmt.Sprintf("vector(%d)", dims)
	}

	steps := []string{
		`CREATE EXTENSION IF NOT EXISTS vector;`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS store_embeddings (
			namespace TEXT NOT NULL,
			key       TEXT NOT NULL,
			embedding %s NOT NULL,
			PRIMARY KEY (namespace, key),
			FOREIGN KEY (namespace, key) REFERENCES store_items(namespace, key) ON DELETE CASCADE
		);`, colType),
		`CREATE INDEX IF NOT EXISTS idx_store_embeddings_namespace ON store_embeddings (namespace);`,
	}

	// pgvector HNSW index on float32 vector supports up to 2000 dimensions.
	if dims > 0 && dims <= 2000 {
		steps = append(steps, `CREATE INDEX IF NOT EXISTS idx_store_embeddings_hnsw ON store_embeddings USING hnsw (embedding vector_cosine_ops);`)
	}

	return steps
}

// Migrate applies all pending core schema migrations to db.
// It maintains a store_migrations table scoped by component to track which
// migrations have already been applied, making it safe to call on every start.
func Migrate(db *sql.DB) error {
	return runMigrations(db, componentCore, coreMigrations)
}

// MigrateVector applies all pending pgvector schema migrations to db under the
// "vector" component in store_migrations.
func MigrateVector(db *sql.DB, dims int) error {
	return runMigrations(db, componentVector, vectorMigrations(dims))
}

func runMigrations(db *sql.DB, component string, steps []string) error {
	if _, err := db.Exec(migrationsTableDDL); err != nil {
		return err
	}

	var version int
	err := db.QueryRow(
		`SELECT version FROM store_migrations WHERE component = $1 ORDER BY version DESC LIMIT 1`,
		component,
	).Scan(&version)
	if err != nil {
		if err != sql.ErrNoRows {
			return err
		}
		version = -1
	}

	for i := version + 1; i < len(steps); i++ {
		if _, err := db.Exec(steps[i]); err != nil {
			return fmt.Errorf("migrate %s v%d: %w", component, i, err)
		}

		if _, err := db.Exec(
			`INSERT INTO store_migrations (component, version) VALUES ($1, $2)`,
			component, i,
		); err != nil {
			return fmt.Errorf("record migration %s v%d: %w", component, i, err)
		}
	}

	return nil
}
