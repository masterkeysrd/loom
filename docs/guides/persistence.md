# Persistence & State 💾

One of Loom's core strengths is its "state-first" architecture. Every time a node executes, Loom saves a **checkpoint** of your workflow's state. This allows you to resume execution after a failure, restart a long-running process, or even pause a graph to wait for human input.

## How Checkpointing Works

Loom uses a `Checkpointer` interface to persist snapshots of the `State`. A snapshot includes:
- The current values in your state struct.
- The execution history (which nodes have run).
- The `Location` (Thread ID and Checkpoint ID) for resumption.

## 1. Setting up a Checkpointer

Loom provides built-in support for SQLite and PostgreSQL.

### SQLite

```go
import (
    "database/sql"
    "github.com/masterkeysrd/loom/checkpoint/sqlite"
    _ "github.com/mattn/go-sqlite3"
)

db, _ := sql.Open("sqlite3", "loom.db")
cp, _ := sqlite.NewCheckpointer(db)

// Attach to the graph builder
builder.WithCheckpointer(cp)
```

### PostgreSQL

```go
import (
    "github.com/masterkeysrd/loom/checkpoint/pg"
)

cp, _ := pg.NewCheckpointer("postgres://user:pass@localhost/dbname")
builder.WithCheckpointer(cp)
```

## 2. Human-in-the-Loop (Interrupts)

You can pause a graph's execution using the `graph.Interrupt()` command. This is essential for workflows that require human approval or external input.

```go
builder.AddNode("Approval", func(ctx context.Context, s MyState) (graph.Command[MyState], error) {
    if !s.IsApproved {
        return graph.Interrupt(), nil // Execution stops here and state is saved
    }
    return graph.Update(func(s MyState) MyState { return s }), nil
})
```

## 3. Resuming a Thread

When a graph is interrupted or finishes, it returns a `Snapshot`. You can use the `snapshot.Location` to resume execution later.

```go
// Initial execution
snapshot, _ := g.Execute(ctx, initialCmd, nil)
threadID := snapshot.Location.ThreadID

// ... Later, when you want to resume ...

// Resume from the last checkpoint using the ThreadID
resumeLocation := &graph.Location{ThreadID: threadID}
nextSnapshot, _ := g.Execute(ctx, nil, resumeLocation)
```

## Best Practices

- **State Serialization**: Ensure all fields in your state struct are JSON-serializable if you are using standard checkpointers.
- **Thread Management**: Use unique `ThreadID`s to separate different user sessions or independent tasks.
- **Idempotency**: Since a node might be retried if a checkpoint fails, try to make your node functions idempotent where possible.
