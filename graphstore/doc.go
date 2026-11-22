// Package graphstore defines Loom's Knowledge Graph storage abstractions,
// domain models ([Node], [Edge], [GraphDocument], [Subgraph]), and
// schema-guided LLM [Extractor] for GraphRAG workflows.
//
// Implementations live in subpackages mirroring [vectorstore]:
//   - graphstore/memory: in-memory adjacency graph with BM25 and optional vector search.
//   - graphstore/sqlite: pure-Go SQLite backend with FTS5 trigram search and recursive CTE traversal.
//   - graphstore/pg: PostgreSQL backend with pg_trgm, optional pgvector, and PG 14+ SEARCH/CYCLE CTEs.
package graphstore
