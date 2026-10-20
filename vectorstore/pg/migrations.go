package pg

import (
	"database/sql"
	"fmt"
)

const (
	componentVectorStore = "vectorstore"

	migrationsTableDDL = `CREATE TABLE IF NOT EXISTS vectorstore_migrations (
		component  TEXT NOT NULL DEFAULT 'vectorstore',
		version    INT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (component, version)
	);`
)

func migrationSteps(dims int) []string {
	colType := "vector"
	if dims > 0 {
		colType = fmt.Sprintf("vector(%d)", dims)
	}

	steps := []string{
		`CREATE EXTENSION IF NOT EXISTS vector;`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS vectorstore_documents (
			namespace TEXT NOT NULL,
			id        TEXT NOT NULL,
			content   TEXT NOT NULL,
			metadata  JSONB,
			embedding %s NOT NULL,
			PRIMARY KEY (namespace, id)
		);`, colType),
		`CREATE INDEX IF NOT EXISTS idx_vectorstore_documents_ns ON vectorstore_documents (namespace);`,
	}

	if dims > 0 && dims <= 2000 {
		steps = append(steps, `CREATE INDEX IF NOT EXISTS idx_vectorstore_documents_hnsw ON vectorstore_documents USING hnsw (embedding vector_cosine_ops);`)
	}

	return steps
}

// Migrate applies all pending schema migrations for vectorstore to db.
func Migrate(db *sql.DB, dims int) error {
	if _, err := db.Exec(migrationsTableDDL); err != nil {
		return err
	}

	var version int
	err := db.QueryRow(
		`SELECT version FROM vectorstore_migrations WHERE component = $1 ORDER BY version DESC LIMIT 1`,
		componentVectorStore,
	).Scan(&version)
	if err != nil {
		if err != sql.ErrNoRows {
			return err
		}
		version = -1
	}

	steps := migrationSteps(dims)
	for i := version + 1; i < len(steps); i++ {
		if _, err := db.Exec(steps[i]); err != nil {
			return fmt.Errorf("migrate vectorstore v%d: %w", i, err)
		}

		if _, err := db.Exec(
			`INSERT INTO vectorstore_migrations (component, version) VALUES ($1, $2)`,
			componentVectorStore, i,
		); err != nil {
			return fmt.Errorf("record migration vectorstore v%d: %w", i, err)
		}
	}

	return nil
}
