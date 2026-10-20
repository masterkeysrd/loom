// Package memory provides an in-memory, thread-safe implementation of [vectorstore.VectorStore].
package memory

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/google/uuid"
	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/vectorstore"
)

var _ vectorstore.VectorStore = (*Store)(nil)

type entry struct {
	doc document.Document
	vec embedding.Vector
}

// Store is an in-memory thread-safe vector store.
type Store struct {
	embed *embedding.Model
	mu    sync.RWMutex
	items map[string]map[string]entry // namespace -> (id -> entry)
}

// New creates a new in-memory [Store] using the provided embedding model.
func New(embed *embedding.Model) *Store {
	return &Store{
		embed: embed,
		items: make(map[string]map[string]entry),
	}
}

// Add adds documents to the vector store under the specified namespace (defaulting to "default").
// Each document is embedded in batch via the configured embedding model. If a document lacks an ID,
// a UUID is generated.
func (s *Store) Add(ctx context.Context, docs []document.Document, opts ...vectorstore.AddOption) ([]string, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	if s.embed == nil {
		return nil, errors.New("vectorstore/memory: embedding model is nil")
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

	ids := make([]string, len(docs))
	s.mu.Lock()
	defer s.mu.Unlock()

	nsMap := s.items[cfg.Namespace]
	if nsMap == nil {
		nsMap = make(map[string]entry)
		s.items[cfg.Namespace] = nsMap
	}

	for i, doc := range docs {
		id := doc.ID
		if id == "" {
			id = uuid.NewString()
		}
		ids[i] = id

		storedDoc := doc
		storedDoc.ID = id
		storedDoc.Metadata = document.CloneMetadata(doc.Metadata)

		nsMap[id] = entry{
			doc: storedDoc,
			vec: vectors[i],
		}
	}

	return ids, nil
}

// Search retrieves up to k documents most similar to the query string within the specified namespace.
func (s *Store) Search(ctx context.Context, query string, k int, opts ...vectorstore.SearchOption) ([]document.Document, error) {
	if k <= 0 {
		return nil, nil
	}
	if s.embed == nil {
		return nil, errors.New("vectorstore/memory: embedding model is nil")
	}

	cfg := vectorstore.ApplySearchOptions(opts...)

	queryVec, err := s.embed.EmbedQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	nsMap := s.items[cfg.Namespace]
	if len(nsMap) == 0 {
		s.mu.RUnlock()
		return nil, nil
	}

	type matchItem struct {
		doc document.Document
		vec embedding.Vector
	}

	var matched []matchItem
	for _, item := range nsMap {
		if !vectorstore.MatchesFilter(item.doc.Metadata, cfg.Filter) {
			continue
		}

		score := queryVec.CosineSimilarity(item.vec)
		if cfg.HasMinScore && score < cfg.MinScore {
			continue
		}

		docCopy := item.doc.WithScore(score)
		docCopy.Metadata = document.CloneMetadata(item.doc.Metadata)
		matched = append(matched, matchItem{doc: docCopy, vec: item.vec})
	}
	s.mu.RUnlock()

	slices.SortFunc(matched, func(a, b matchItem) int {
		if a.doc.Score > b.doc.Score {
			return -1
		}
		if a.doc.Score < b.doc.Score {
			return 1
		}
		if a.doc.ID < b.doc.ID {
			return -1
		}
		if a.doc.ID > b.doc.ID {
			return 1
		}
		return 0
	})

	if cfg.UseMMR {
		fetchK := cfg.MMRFetchK
		if fetchK < k {
			fetchK = k * 4
		}
		if len(matched) > fetchK {
			matched = matched[:fetchK]
		}
		candidateDocs := make([]document.Document, len(matched))
		candidateVecs := make([]embedding.Vector, len(matched))
		for i, m := range matched {
			candidateDocs[i] = m.doc
			candidateVecs[i] = m.vec
		}
		return vectorstore.SelectMMR(queryVec, candidateDocs, candidateVecs, cfg.MMRLambda, k), nil
	}

	if len(matched) > k {
		matched = matched[:k]
	}

	results := make([]document.Document, len(matched))
	for i, m := range matched {
		results[i] = m.doc
	}

	return results, nil
}

// Delete removes documents matching ids from the specified namespace.
func (s *Store) Delete(ctx context.Context, ids []string, opts ...vectorstore.DeleteOption) error {
	if len(ids) == 0 {
		return nil
	}

	cfg := vectorstore.ApplyDeleteOptions(opts...)

	s.mu.Lock()
	defer s.mu.Unlock()

	nsMap := s.items[cfg.Namespace]
	if nsMap == nil {
		return nil
	}

	for _, id := range ids {
		delete(nsMap, id)
	}

	return nil
}
