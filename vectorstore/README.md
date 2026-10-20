# vectorstore

The `vectorstore` package provides a unified **Vector Store** interface and backend implementations (`memory`, `sqlite`, `pg`) for indexing and searching [`document.Document`](../document/README.md) chunks by semantic similarity.

While [`store.Store`](../docs/guides/persistent-store.md) is a key-value store for structured agent state (`Put`/`Get`), `vectorstore.VectorStore` is purpose-built for **Retrieval-Augmented Generation (RAG)**: batch document embedding via [`embedding.Model.EmbedDocuments`](../embedding/README.md), relevance score ranking, and metadata filtering.

---

## Core Interface

```go
type VectorStore interface {
    // Add embeds and stores a batch of documents, returning their IDs.
    Add(ctx context.Context, docs []document.Document, opts ...AddOption) ([]string, error)

    // Search returns the top-k most similar documents with Score populated.
    Search(ctx context.Context, query string, k int, opts ...SearchOption) ([]document.Document, error)

    // Delete removes documents by ID.
    Delete(ctx context.Context, ids []string, opts ...DeleteOption) error
}
```

### Options

| Option | Target | Description |
|---|---|---|
| `WithAddNamespace(ns)` | `Add` | Namespace to store documents under (default: `"default"`). |
| `WithNamespace(ns)` | `Search` | Namespace to search within (default: `"default"`). |
| `WithMinScore(score)` | `Search` | Minimum cosine similarity score threshold in $[-1, 1]$. |
| `WithFilter(map[string]any)` | `Search` | Exact key-value metadata filter (with numeric type normalization). |
| `WithDeleteNamespace(ns)` | `Delete` | Namespace to delete document IDs from (default: `"default"`). |

---

## Backends

### 1. In-Memory (`vectorstore/memory`)
Zero-dependency, thread-safe in-memory vector store ideal for local development, testing, and lightweight RAG workflows.

```go
import (
    "github.com/masterkeysrd/loom/document"
    "github.com/masterkeysrd/loom/embedding"
    loomopenai "github.com/masterkeysrd/loom/providers/openai"
    "github.com/masterkeysrd/loom/vectorstore"
    "github.com/masterkeysrd/loom/vectorstore/memory"
)

provider, _ := loomopenai.NewDefaultProvider()
embedModel, _ := embedding.NewModel(provider, "text-embedding-3-small", nil)

vs := memory.New(embedModel)

// Add documents (embeds in a single batch call)
ids, err := vs.Add(ctx, []document.Document{
    document.New("Loom supports stateful graph workflows.", map[string]any{"topic": "graph"}),
    document.New("Checkpointers persist state to SQLite and Postgres.", map[string]any{"topic": "persistence"}),
})

// Search with metadata filter and minimum score
results, err := vs.Search(ctx, "How do I save workflow state?", 5,
    vectorstore.WithFilter(map[string]any{"topic": "persistence"}),
    vectorstore.WithMinScore(0.5),
)
```

### 2. SQLite (`vectorstore/sqlite`)
Single-node persistent vector store backed by `modernc.org/sqlite` (pure Go, zero CGO) using a deterministic cosine similarity SQL scalar function (`loom_vec_cosine`).

```go
import (
    "database/sql"
    "github.com/masterkeysrd/loom/vectorstore/sqlite"
    _ "modernc.org/sqlite"
)

db, err := sql.Open("sqlite", "vectors.db")
if err != nil {
    log.Fatal(err)
}

vs, err := sqlite.New(db, embedModel)
```

### 3. PostgreSQL `pgvector` (`vectorstore/pg`)
Production-grade distributed vector store using PostgreSQL's `pgvector` extension, HNSW cosine similarity indexing (`vector_cosine_ops`), and native `JSONB @>` containment filtering on document metadata.

```go
import (
    "database/sql"
    _ "github.com/lib/pq"
    "github.com/masterkeysrd/loom/vectorstore/pg"
)

db, err := sql.Open("postgres", os.Getenv("POSTGRES_DSN"))
if err != nil {
    log.Fatal(err)
}

// Vector dimensions are resolved automatically from embedModel's config/profile,
// or can be set explicitly via pg.WithDims(1536).
vs, err := pg.New(db, embedModel)
```
