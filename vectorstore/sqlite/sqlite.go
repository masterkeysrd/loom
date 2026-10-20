// Package sqlite provides a SQLite-backed implementation of [vectorstore.VectorStore]
// using modernc.org/sqlite and a custom deterministic cosine similarity scalar function.
package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"modernc.org/sqlite"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/vectorstore"
)

var _ vectorstore.VectorStore = (*Store)(nil)

var registerVecOnce sync.Once

func ensureVecFunctionRegistered() {
	registerVecOnce.Do(func() {
		_ = sqlite.RegisterDeterministicScalarFunction(
			"loom_vec_cosine",
			2,
			func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
				if len(args) != 2 {
					return float64(-1), nil
				}
				a, ok1 := args[0].([]byte)
				b, ok2 := args[1].([]byte)
				if !ok1 || !ok2 {
					return float64(-1), nil
				}
				return embedding.CosineSimilarityBytes(a, b), nil
			},
		)
	})
}

// Store is a SQLite-backed vector store.
type Store struct {
	db    *sql.DB
	embed *embedding.Model
}

// New creates a new SQLite [Store] and applies pending migrations.
func New(db *sql.DB, embed *embedding.Model) (*Store, error) {
	if db == nil {
		return nil, errors.New("vectorstore/sqlite: db cannot be nil")
	}

	ensureVecFunctionRegistered()

	if err := Migrate(db); err != nil {
		return nil, err
	}

	return &Store{
		db:    db,
		embed: embed,
	}, nil
}

// Add inserts or updates documents in the vector store under the specified namespace.
func (s *Store) Add(ctx context.Context, docs []document.Document, opts ...vectorstore.AddOption) ([]string, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	if s.embed == nil {
		return nil, errors.New("vectorstore/sqlite: embedding model is nil")
	}

	cfg := vectorstore.ApplyAddOptions(opts...)

	texts := make([]string, len(docs))
	for i, doc := range docs {
		texts[i] = doc.Content
	}

	vectors, err := s.embed.EmbedDocuments(ctx, texts)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO vectorstore_documents (namespace, id, content, metadata, embedding)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(namespace, id) DO UPDATE SET
			content   = excluded.content,
			metadata  = excluded.metadata,
			embedding = excluded.embedding
	`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	ids := make([]string, len(docs))
	for i, doc := range docs {
		id := doc.ID
		if id == "" {
			id = uuid.NewString()
		}
		ids[i] = id

		var metaBlob []byte
		if len(doc.Metadata) > 0 {
			metaBlob, err = json.Marshal(doc.Metadata)
			if err != nil {
				return nil, fmt.Errorf("marshal metadata: %w", err)
			}
		}

		embBlob := vectors[i].Bytes()

		if _, err := stmt.ExecContext(ctx, cfg.Namespace, id, doc.Content, metaBlob, embBlob); err != nil {
			return nil, fmt.Errorf("upsert document %s: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return ids, nil
}

// Search queries the vector store for the top k documents most similar to query.
func (s *Store) Search(ctx context.Context, query string, k int, opts ...vectorstore.SearchOption) ([]document.Document, error) {
	if k <= 0 {
		return nil, nil
	}
	if s.embed == nil {
		return nil, errors.New("vectorstore/sqlite: embedding model is nil")
	}

	cfg := vectorstore.ApplySearchOptions(opts...)

	queryVec, err := s.embed.EmbedQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	fetchK := k
	if cfg.UseMMR {
		fetchK = cfg.MMRFetchK
		if fetchK < k {
			fetchK = k * 4
		}
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, content, metadata, embedding, loom_vec_cosine(embedding, ?) AS score
		FROM vectorstore_documents
		WHERE namespace = ?
		ORDER BY score DESC, id ASC
	`, queryVec.Bytes(), cfg.Namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var (
		candidateDocs []document.Document
		candidateVecs []embedding.Vector
	)
	for rows.Next() {
		var id, content string
		var metaBlob, embBlob []byte
		var score float64

		if err := rows.Scan(&id, &content, &metaBlob, &embBlob, &score); err != nil {
			return nil, err
		}

		var meta map[string]any
		if len(metaBlob) > 0 {
			if err := json.Unmarshal(metaBlob, &meta); err != nil {
				return nil, fmt.Errorf("unmarshal metadata: %w", err)
			}
		}

		if !vectorstore.MatchesFilter(meta, cfg.Filter) {
			continue
		}

		if cfg.HasMinScore && float32(score) < cfg.MinScore {
			continue
		}

		doc := document.Document{
			ID:       id,
			Content:  content,
			Metadata: meta,
			Score:    float32(score),
		}
		candidateDocs = append(candidateDocs, doc)
		if cfg.UseMMR {
			candidateVecs = append(candidateVecs, embedding.DecodeVector(embBlob))
		}
		if len(candidateDocs) == fetchK {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if cfg.UseMMR {
		return vectorstore.SelectMMR(queryVec, candidateDocs, candidateVecs, cfg.MMRLambda, k), nil
	}

	return candidateDocs, nil
}

// Delete removes documents matching the provided IDs in the specified namespace.
func (s *Store) Delete(ctx context.Context, ids []string, opts ...vectorstore.DeleteOption) error {
	if len(ids) == 0 {
		return nil
	}

	cfg := vectorstore.ApplyDeleteOptions(opts...)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `DELETE FROM vectorstore_documents WHERE namespace = ? AND id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, cfg.Namespace, id); err != nil {
			return err
		}
	}

	return tx.Commit()
}
