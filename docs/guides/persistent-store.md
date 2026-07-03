# Persistent Store 💾

The `store` package provides a **namespaced key-value store** for persistent, searchable long-term memory. It gives AI agents the ability to read and write structured data that survives across graph executions, process restarts, and conversations.

## Overview

The store follows a clean interface pattern: a core `Store` interface in the parent package, with backend implementations in sub-packages. All values are JSON-serialized internally — callers pass Go pointers and the store marshals/unmarshals automatically.

```
store/
├── store.go        # Core Store interface
├── options.go      # Variadic options (PutOption, SearchOption, etc.)
├── context.go      # WithStore / FromContext integration
├── generic.go      # Generic helpers: Get[T], Search[T]
├── memorydb/       # In-memory backend (dev / testing)
└── sqlite/         # SQLite backend (production)
```

## Core Interface

The `Store` interface exposes four operations over a `namespace/key` address space:

| Method    | Purpose |
|-----------|---------|
| `Put`     | Marshal `value` to JSON and store it under `namespace/key`. Upserts if the key exists. |
| `Get`     | Retrieve a single item by `namespace/key` and unmarshal into a pointer. Returns `store.ErrNotFound` if missing. |
| `Search`  | Find all items within a namespace. Supports filtering by prefix, limit, and offset. |
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

### SQLite Backend (Production)

The `sqlite` backend persists namespaced key-value data to a `store_items` table, enabling durable long-term memory across process restarts.

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

The schema is managed automatically via a sequential migration runner:

```sql
CREATE TABLE IF NOT EXISTS store_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

PRAGMA journal_mode=WAL;

CREATE TABLE IF NOT EXISTS store_items (
    namespace  TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      BLOB NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (namespace, key)
);

CREATE INDEX IF NOT EXISTS idx_store_items_namespace ON store_items (namespace);
```

Call `sqlite.Migrate(db)` manually if you need to run migrations separately from `NewStore`. It is safe to call multiple times (idempotent).

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

The `Search` method supports variadic options for filtering and pagination:

| Option | Purpose |
|--------|---------|
| `WithKeyPrefix(prefix)` | Filter results to keys matching the given prefix |
| `WithLimit(n)` | Cap the number of items returned |
| `WithOffset(n)` | Skip the first N items (pagination) |

```go
var items []Item
err := s.Search(ctx, "user:123", &items,
    store.WithKeyPrefix("mem:"),
    store.WithLimit(10),
    store.WithOffset(20),
)
```

## Best Practices

- **Namespace Convention**: Use hierarchical namespace strings for logical grouping, e.g., `"user:123:data"` or `"agent:knowledge"`.
- **Thread Safety**: Both backends are safe for concurrent use — `memorydb` uses `sync.RWMutex`, `sqlite` relies on `sql.DB` internal locking.
- **Idempotency**: `Delete` is idempotent (returns `nil` for missing keys). Design node functions to be idempotent where possible, since retries may occur.
- **JSON Serialization**: All values are JSON-serialized. Ensure struct fields are JSON-serializable.
- **Production Choice**: Use `sqlite` for production deployments where persistence across restarts is required. Use `memorydb` for development and tests.
