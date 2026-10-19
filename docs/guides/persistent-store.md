# Persistent Store 💾

The `store` package provides a **namespaced key-value store** for persistent, searchable long-term memory. It gives AI agents the ability to read and write structured data that survives across graph executions, process restarts, and conversations.

## Overview

The store follows a clean interface pattern: a core `Store` interface in the parent package, with backend implementations in sub-packages. All values are JSON-serialized internally — callers pass Go pointers and the store marshals/unmarshals automatically.

```
store/
├── store.go        # Core Store interface
├── options.go      # Variadic options (PutOption, SearchOption, etc.)
├── index.go        # Semantic indexing (IndexConfig, Embedder, vector helpers)
├── context.go      # WithStore / FromContext integration
├── generic.go      # Generic helpers: Get[T], Search[T]
├── memorydb/       # In-memory backend (dev / testing)
├── sqlite/         # SQLite backend (pure-Go vector search via scalar function)
└── pg/             # PostgreSQL backend (pgvector HNSW similarity search)
```

## Core Interface

The `Store` interface exposes four operations over a `namespace/key` address space:

| Method    | Purpose |
|-----------|---------|
| `Put`     | Marshal `value` to JSON and store it under `namespace/key`. Upserts if the key exists. |
| `Get`     | Retrieve a single item by `namespace/key` and unmarshal into a pointer. Returns `store.ErrNotFound` if missing. |
| `Search`  | Find items within a namespace. Supports filtering by prefix, semantic query (`WithQuery`), limit, and offset. |
| `Delete`  | Remove an item. Idempotent — returns `nil` if the key does not exist. |

### GORM-Style API

The API follows the GORM / `json.Unmarshal` pattern: callers pass a pointer and the store populates it. No `json.RawMessage` leaks into the public API.

```go
s := store.FromContext(ctx)

var prefs UserPrefs
if err := s.Get(ctx, "user:123", "preferences", &prefs); err != nil { ... }
```

### Generic Package-Level Helpers

For callers who prefer typed returns over pointer passing:

```go
memories, err := store.Get[Memories](ctx, s, "user:123", "memories")

items, err := store.Search[Item](ctx, s, "user:123", store.WithLimit(10))
```

## Setting Up the Store

### In-Memory Backend (Development)

The `memorydb` backend is a zero-dependency, concurrent-safe in-memory store. Ideal for development, testing, and unit tests.

```go
import "github.com/masterkeysrd/loom/store/memorydb"

s := memorydb.New()
builder.WithStore(s)
```

### SQLite Backend (Single-Node Production)

The `sqlite` backend persists namespaced key-value data to a `store_items` table, enabling durable long-term memory across process restarts with zero CGO dependencies.

```go
import (
    "database/sql"
    "github.com/masterkeysrd/loom/store/sqlite"
    _ "modernc.org/sqlite"
)

db, err := sql.Open("sqlite", "loom.db")
if err != nil { ... }

s, err := sqlite.NewStore(db)
if err != nil { ... }

builder.WithStore(s)
```

### PostgreSQL Backend (Distributed Production)

The `pg` backend persists items in PostgreSQL using `JSONB` (and `pgvector` with an HNSW index when semantic search is configured).

```go
import (
    "database/sql"
    "github.com/masterkeysrd/loom/store/pg"
    _ "github.com/lib/pq"
)

db, err := sql.Open("postgres", os.Getenv("POSTGRES_DSN"))
if err != nil { ... }

s, err := pg.NewStore(db)
if err != nil { ... }

builder.WithStore(s)
```

## Semantic Vector Search

All three store backends (`memorydb`, `sqlite`, and `pg`) support automatic semantic indexing and similarity search. Pass a `store.IndexConfig` when constructing the store:

```go
import (
    "github.com/masterkeysrd/loom/embedding"
    loomopenai "github.com/masterkeysrd/loom/providers/openai"
    "github.com/masterkeysrd/loom/store"
    "github.com/masterkeysrd/loom/store/memorydb"
)

provider, _ := loomopenai.NewDefaultProvider()
embedModel, _ := embedding.NewModel(provider, "text-embedding-3-small", nil)

// Initialize the store with semantic search
s := memorydb.New(memorydb.WithIndex(store.IndexConfig{
    Embed:  embedModel,
    Dims:   1536,             // Optional if embedModel has profile/configured dimensions
    Fields: []string{"text"}, // gjson field paths to vectorize (empty embeds whole payload)
}))

type Document struct {
    Text string `json:"text"`
}

// Store an item (automatically embeds the "text" field)
_ = s.Put(ctx, "docs", "doc1", Document{Text: "Python tutorial for agents"})

// Search semantically
results, err := store.Search[Document](ctx, s, "docs",
    store.WithQuery("programming guides"),
    store.WithLimit(5),
)
```

### How Each Backend Stores & Searches Embeddings

| Backend | Vector Storage | Similarity Engine |
|---------|----------------|-------------------|
| `memorydb` | `embedding.Vector` on in-memory entry | `embedding.Vector.CosineSimilarity` |
| `sqlite` | Separate `store_embeddings` table (`embedding BLOB`) | Registered pure-Go deterministic SQLite scalar function (`loom_vec_cosine`) |
| `pg` | Separate `store_embeddings` table (`vector(dims)`) | Native `pgvector` cosine distance (`<=>`) with `hnsw (embedding vector_cosine_ops)` index |

### Per-Put Indexing Controls

You can disable indexing or override indexed `gjson` field paths for an individual `Put` call:

```go
// Skip embedding generation for this item
_ = s.Put(ctx, "docs", "raw-config", cfg, store.WithIndexDisabled())

// Embed specific nested fields or array queries for this item
_ = s.Put(ctx, "docs", "article-1", article, store.WithIndexFields("title", "meta.summary", "sections.#.heading"))
```

## Usage Patterns

### Retrieving the Store

The store is injected into the context. Retrieve it with `store.FromContext`:

```go
func myNode(ctx context.Context, s MyState) (graph.Command[MyState], error) {
    store := store.FromContext(ctx)
    if store == nil {
        return nil, fmt.Errorf("store not configured")
    }
    // ...
}
```

### Per-User Scoping

Use namespaced keys to isolate data per user, session, or agent:

```go
s := store.FromContext(ctx)

// Per-user preferences
ns := fmt.Sprintf("%s:user-data", userID)
var prefs UserPrefs
if err := s.Get(ctx, ns, "preferences", &prefs); err != nil { ... }

// Per-agent knowledge
ns = fmt.Sprintf("%s:knowledge", agentID)
s.Put(ctx, ns, "latest-finding", result)
```

### Inside a Graph Node

```go
func myNode(ctx context.Context, s MyState) (graph.Command[MyState], error) {
    store := store.FromContext(ctx)

    // GORM-style
    var prefs UserPrefs
    err := s.Get(ctx, fmt.Sprintf("%s:user-data", s.UserID), "preferences", &prefs)

    // Generic-style
    memories, err := store.Get[Memories](ctx, store, fmt.Sprintf("%s:user-data", s.UserID), "memories")

    return graph.Update(func(s MyState) MyState { return s }), nil
}
```

### Inside a Tool

```go
func myTool(ctx context.Context, args MyArgs) (string, error) {
    s := store.FromContext(ctx)
    if err := s.Put(ctx, "agent:knowledge", "latest-finding", result); err != nil {
        return "", err
    }
    return "saved", nil
}
```

## Search Options

The `Search` method supports variadic options for filtering, semantic search, and pagination:

| Option | Purpose |
|--------|---------|
| `WithQuery(query)` | Rank results by semantic similarity to `query` |
| `WithKeyPrefix(prefix)` | Filter results to keys matching the given prefix |
| `WithLimit(n)` | Cap the number of items returned |
| `WithOffset(n)` | Skip the first N items (pagination) |

```go
var items []Item
err := s.Search(ctx, "user:123", &items,
    store.WithKeyPrefix("mem:"),
    store.WithQuery("recent project deadlines"),
    store.WithLimit(10),
    store.WithOffset(0),
)
```

## Best Practices

- **Namespace Convention**: Use hierarchical namespace strings for logical grouping, e.g., `"user:123:data"` or `"agent:knowledge"`.
- **Thread Safety**: All backends are safe for concurrent use — `memorydb` uses `sync.RWMutex`, while `sqlite` and `pg` rely on `sql.DB` connection pooling.
- **Idempotency**: `Delete` is idempotent (returns `nil` for missing keys). Design node functions to be idempotent where possible, since retries may occur.
- **JSON Serialization**: All values are JSON-serialized. Ensure struct fields are JSON-serializable.
- **Backend Selection**: Use `memorydb` for development and unit tests, `sqlite` for single-node deployments (up to ~50k–100k vectors per namespace), and `pg` with `pgvector` for large-scale or multi-instance production deployments.
