# graphstore

The `graphstore` package provides Loom’s **Knowledge Graph Storage** abstractions, schema-guided LLM `Extractor`, and backend implementations (`memory`, `sqlite`, `pg`) for multi-hop **GraphRAG** workflows.

See the full [Graph retrieval and Knowledge Graph RAG guide](../docs/guides/graph-rag.md) for architecture details and end-to-end hybrid retrieval examples.

---

## Core Interface

```go
type GraphStore interface {
    // Upsert atomically merges nodes, edges, and document provenance links.
    Upsert(ctx context.Context, docs []GraphDocument, opts ...UpsertOption) error

    // SearchNodes finds seed nodes matching a text query and optional filters.
    SearchNodes(ctx context.Context, query string, k int, opts ...SearchOption) ([]Node, error)

    // Traverse performs a cycle-safe breadth-first expansion up to maxDepth hops.
    Traverse(ctx context.Context, seedNodeIDs []string, maxDepth int, opts ...TraverseOption) (Subgraph, error)

    // Delete removes nodes (and incident edges) or provenance edges matching ids.
    Delete(ctx context.Context, ids []string, opts ...DeleteOption) error
}
```

### Options

| Option | Target | Description |
|---|---|---|
| `WithUpsertNamespace(ns)` | `Upsert` | Namespace to store nodes and edges under (default: `"default"`). |
| `WithNamespace(ns)` | `SearchNodes` | Namespace to query (default: `"default"`). |
| `WithNodeTypes(types...)` | `SearchNodes` | Filter candidate nodes by entity class. |
| `WithFilter(map[string]any)` | `SearchNodes` | Filter candidate nodes by property metadata (supports slice/set overlap). |
| `WithMinScore(score)` | `SearchNodes` | Minimum relevance/similarity score threshold. |
| `WithTraverseNamespace(ns)` | `Traverse` | Namespace to traverse (default: `"default"`). |
| `WithEdgeTypes(types...)` | `Traverse` | Restrict traversal to specific relationship predicates. |
| `WithBidirectional(bool)` | `Traverse` | Traverse incoming and outgoing edges (default: `true`). |
| `WithMaxEdges(n)` | `Traverse` | Maximum edges returned (default: `50`). |
| `WithMaxNodes(n)` | `Traverse` | Maximum nodes returned (default: `100`). |
| `WithDeleteNamespace(ns)` | `Delete` | Namespace to delete from (default: `"default"`). |
| `WithDeleteByDocID(bool)` | `Delete` | Delete edges/provenance by source `doc_id` instead of node ID. |

---

## Schema-Guided `Extractor`

Compile an ontology-constrained JSON Schema via `llm.Model.WithStructuredOutput` to extract typed `GraphDocument`s concurrently across chunks:

```go
extractor, err := graphstore.NewExtractor(
    chatModel,
    graphstore.WithAllowedNodeTypes("Service", "Infrastructure"),
    graphstore.WithAllowedEdgeTypes("CALLS", "DEPENDS_ON"),
    graphstore.WithAllowedTriples(
        graphstore.TripleRule{SourceType: "Service", EdgeType: "CALLS", TargetType: "Service"},
        graphstore.TripleRule{SourceType: "Service", EdgeType: "DEPENDS_ON", TargetType: "Infrastructure"},
    ),
    graphstore.WithConcurrency(4),
)

graphDocs, err := extractor.Extract(ctx, chunks)
```

---

## Backends

### 1. In-Memory (`graphstore/memory`)
Thread-safe adjacency maps with lazy `internal/bm25` + substring lexical grounding and optional `WithEmbedding(embedModel)` semantic node search.

```go
gs := memory.New(memory.WithEmbedding(embedModel))
```

### 2. SQLite (`graphstore/sqlite`)
Pure-Go `modernc.org/sqlite` (`CGO_ENABLED=0`) backend with WAL mode, `FTS5` `trigram` + `bm25()` lexical grounding, optional `loom_vec_cosine` semantic scoring, and cycle-safe `WITH RECURSIVE` CTE traversal.

```go
db, _ := sql.Open("sqlite", "knowledge.db")
gs, err := sqlite.New(db, sqlite.WithEmbedding(embedModel))
```

### 3. PostgreSQL (`graphstore/pg`)
Managed-cloud-compatible PostgreSQL backend using `pg_trgm` (`GIN (label gin_trgm_ops)`), optional `pgvector` HNSW node embeddings (`WithEmbedding` / `WithDims`), and PostgreSQL 14+ `WITH RECURSIVE ... SEARCH BREADTH FIRST ... CYCLE` CTE traversal.

```go
db, _ := sql.Open("postgres", dsn)
gs, err := pg.New(db, pg.WithEmbedding(embedModel), pg.WithDims(1536))
```
