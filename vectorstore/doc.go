// Package vectorstore provides interfaces and storage backends for persisting and
// querying document embeddings with semantic vector search.
//
// Subpackages provide concrete implementations:
//   - [github.com/masterkeysrd/loom/vectorstore/memory]: In-memory thread-safe vector store.
//   - [github.com/masterkeysrd/loom/vectorstore/sqlite]: SQLite vector store with deterministic cosine similarity scalar function.
//   - [github.com/masterkeysrd/loom/vectorstore/pg]: PostgreSQL vector store with pgvector and HNSW index support.
package vectorstore
