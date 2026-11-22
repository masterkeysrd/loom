package pg

import (
	"database/sql"
	"fmt"
)

const (
	componentGraphStore = "graphstore"
	componentGraphVec   = "graphstore_vector"

	migrationsTableDDL = `CREATE TABLE IF NOT EXISTS graphstore_migrations (
		component  TEXT NOT NULL DEFAULT 'graphstore',
		version    INT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (component, version)
	);`
)

func baseMigrationSteps() []string {
	return []string{
		`CREATE EXTENSION IF NOT EXISTS pg_trgm;`,
		`CREATE TABLE IF NOT EXISTS graphstore_nodes (
			namespace  TEXT NOT NULL,
			id         TEXT NOT NULL,
			label      TEXT NOT NULL,
			type       TEXT NOT NULL DEFAULT '',
			properties JSONB NOT NULL DEFAULT '{}'::jsonb,
			PRIMARY KEY (namespace, id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_graphstore_nodes_ns_type ON graphstore_nodes (namespace, type);`,
		`CREATE INDEX IF NOT EXISTS idx_graphstore_nodes_label_trgm ON graphstore_nodes USING gin (label gin_trgm_ops);`,
		`CREATE TABLE IF NOT EXISTS graphstore_edges (
			namespace  TEXT NOT NULL,
			source     TEXT NOT NULL,
			target     TEXT NOT NULL,
			type       TEXT NOT NULL,
			weight     REAL NOT NULL DEFAULT 1.0,
			properties JSONB NOT NULL DEFAULT '{}'::jsonb,
			doc_id     TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (namespace, source, target, type, doc_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_graphstore_edges_ns_src ON graphstore_edges (namespace, source);`,
		`CREATE INDEX IF NOT EXISTS idx_graphstore_edges_ns_tgt ON graphstore_edges (namespace, target);`,
		`CREATE INDEX IF NOT EXISTS idx_graphstore_edges_ns_doc ON graphstore_edges (namespace, doc_id);`,
	}
}

func vectorMigrationSteps(dims int) []string {
	colType := "vector"
	if dims > 0 {
		colType = fmt.Sprintf("vector(%d)", dims)
	}
	steps := []string{
		`CREATE EXTENSION IF NOT EXISTS vector;`,
		fmt.Sprintf(`ALTER TABLE graphstore_nodes ADD COLUMN IF NOT EXISTS embedding %s;`, colType),
	}
	if dims > 0 && dims <= 2000 {
		steps = append(steps, `CREATE INDEX IF NOT EXISTS idx_graphstore_nodes_hnsw ON graphstore_nodes USING hnsw (embedding vector_cosine_ops);`)
	}
	return steps
}

// Migrate applies all pending schema migrations for graphstore to db.
// When enableVector is true (or dims > 0), the pgvector extension and embedding column are also provisioned.
func Migrate(db *sql.DB, enableVector bool, dims int) error {
	if _, err := db.Exec(migrationsTableDDL); err != nil {
		return err
	}

	if err := applyComponentMigrations(db, componentGraphStore, baseMigrationSteps()); err != nil {
		return err
	}

	if enableVector || dims > 0 {
		if err := applyComponentMigrations(db, componentGraphVec, vectorMigrationSteps(dims)); err != nil {
			return err
		}
	}

	return nil
}

func applyComponentMigrations(db *sql.DB, component string, steps []string) error {
	var version int
	err := db.QueryRow(
		`SELECT version FROM graphstore_migrations WHERE component = $1 ORDER BY version DESC LIMIT 1`,
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
			`INSERT INTO graphstore_migrations (component, version) VALUES ($1, $2)`,
			component, i,
		); err != nil {
			return fmt.Errorf("record migration %s v%d: %w", component, i, err)
		}
	}

	return nil
}
