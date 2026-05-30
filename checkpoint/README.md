# checkpoint

The `checkpoint` package provides implementations of the `graph.Checkpointer` interface for persistent storage of graph execution state.

## Available Implementations

| Implementation | Package Path | Backend |
|---|---|---|
| PostgreSQL | `github.com/masterkeysrd/loom/checkpoint/pg` | PostgreSQL with JSONB support |
| SQLite | `github.com/masterkeysrd/loom/checkpoint/sqlite` | Local SQLite database |

## Features

- **Automatic Migrations**: Both implementations handle their own schema setup and migrations.
- **Durable Resumption**: Allows a graph to be paused (interrupted) and resumed exactly where it left off, even after a process restart.
- **Efficient Retrieval**: Uses UUIDv7 for checkpoint IDs, allowing fast retrieval of the latest state for any thread.

## Usage (PostgreSQL)

```go
import "github.com/masterkeysrd/loom/checkpoint/pg"

db, _ := sql.Open("postgres", dsn)
cp, _ := pg.NewCheckpointer(db)

// Attach to your graph
g, _ := graph.New[MyState]().
    WithCheckpointer(cp).
    Build()
```

## Usage (SQLite)

```go
import "github.com/masterkeysrd/loom/checkpoint/sqlite"

db, _ := sql.Open("sqlite3", "loom.db")
cp, _ := sqlite.NewCheckpointer(db)

// Attach to your graph
g, _ := graph.New[MyState]().
    WithCheckpointer(cp).
    Build()
```
