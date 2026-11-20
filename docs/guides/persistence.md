---
title: Resume a workflow
description: Save a checkpoint with SQLite and resume a Loom graph across process restarts.
---

Run a draft review in two processes. The first one writes a draft to SQLite and pauses for approval. The second one loads that draft, applies your approval, and finishes the graph. Loom keeps checkpoints across restarts when you attach a durable checkpointer to your builder.

![The first process saves a pending draft to SQLite and exits. The second uses the same thread ID to load the draft and apply approval.](../diagrams/resume-checkpoint.svg)

## Start a review

From a checkout of the Loom repository, use Go 1.26.10 or newer and a fresh SQLite file. The example creates the database in your working directory. To try the tutorial again, choose a new filename for both commands.

```sh
go run ./examples/resume -db review.db
```

```text
Awaiting approval: Ship the new search
```

The process has exited. The SQLite database holds the draft and the name of the node waiting to run.

## Resume after the process exits

From the same directory, run the example again and approve the draft.

```sh
go run ./examples/resume -db review.db -approve
```

```text
Approved: Ship the new search
```

The second invocation supplies only the approval. The draft text comes from the first process's checkpoint. Repeating the approval prints `Already approved: Ship the new search`.

## Use this pattern in your graph

Attach a checkpointer before calling `Build`. Give each independent workflow a stable `ThreadID`. In the example, the review node returns `graph.InterrupCmd[Review]{}` while it waits. When another process starts, it opens the same database and calls `Execute` with the same thread ID and a state update that sets `Approved`. Loom reloads the latest checkpoint and re-enters the waiting node. [Read the complete example](https://github.com/masterkeysrd/loom/blob/main/examples/resume/main.go).

## How Checkpointing Works

Loom uses a `Checkpointer` interface to persist snapshots of the `State`. A snapshot includes:
- The current values in your state struct decomposed into channel keys.
- The execution history and next nodes to run.
- The `Location` (`ThreadID`, `CheckpointNS`, `CheckpointID`) for resumption.

```go
type Checkpointer interface {
    Load(context.Context, Location) (*Checkpoint, error)
    Record(context.Context, Checkpoint) error
}
```

### Default: `graph.NoopCheckpointer`

If you do not configure a checkpointer via `builder.WithCheckpointer(...)`, Loom automatically defaults to `graph.NoopCheckpointer{}`:
- `Load` always returns `nil, nil`.
- `Record` is an instantaneous no-op.
- Graphs run fully in-memory with zero disk or database overhead, while retaining identical execution semantics.

---

## 1. Setting up a Checkpointer

Loom provides production-ready checkpointers for SQLite and PostgreSQL.

### SQLite

```go
import (
    "database/sql"

    "github.com/masterkeysrd/loom/checkpoint/sqlite"
    _ "modernc.org/sqlite"
)

db, err := sql.Open("sqlite", "loom.db")
if err != nil {
    log.Fatal(err)
}

cp, err := sqlite.NewCheckpointer(db)
if err != nil {
    log.Fatal(err)
}

// Attach to the graph builder
builder.WithCheckpointer(cp)
```

### PostgreSQL

The PostgreSQL checkpointer uses an efficient three-table schema (`checkpoints`, `checkpoint_blobs`, and `checkpoint_writes`) with content-addressable deduplication. `pg.NewCheckpointer` accepts an initialized `*sql.DB`:

```go
import (
    "database/sql"

    "github.com/masterkeysrd/loom/checkpoint/pg"
    _ "github.com/lib/pq"
)

db, err := sql.Open("postgres", "postgres://user:pass@localhost:5432/loom?sslmode=disable")
if err != nil {
    log.Fatal(err)
}

// NewCheckpointer automatically executes pending migrations
cp, err := pg.NewCheckpointer(db)
if err != nil {
    log.Fatal(err)
}

builder.WithCheckpointer(cp)
```

---

## 2. Checkpoint Locations, Pre-Minted IDs & Deterministic Task IDs

### Pre-Minted Checkpoint IDs
Before executing any step's nodes, Loom pre-mints a time-ordered UUIDv7 `CheckpointID` and passes it inside the execution location. This ensures:
- Nodes know the upcoming checkpoint ID in advance.
- Telemetry spans, events, and writes are tied to the exact checkpoint before it is written.
- Inspecting `graph.RuntimeFromContext(ctx)` exposes:
  - `rt.RunID` (the pre-minted `CheckpointID`)
  - `rt.Step` (current zero-based step index)
  - `rt.NodeName` (name of the executing node)
  - `rt.Location` (`ThreadID`, `CheckpointNS`, `CheckpointID`)

### Deterministic `TaskID`
For every node invocation, Loom derives a deterministic `TaskID` using SHA-1 over `(ThreadID, CheckpointNS, Step, NodeName)`:

```go
taskID := computeTaskID(threadID, checkpointNS, step, nodeName)
```

This predictable identity guarantees idempotency and enables safe correlation between node writes and checkpoint states.

---

## 3. Hierarchical Subgraph Namespaces

When embedding sub-graphs via `childGraph.AsNode()` or `graph.MapNode()`, child checkpoints are stored within a segmented, hierarchical namespace modeled after LangGraph:

`CheckpointNS = parent_ns | node_name:task_id`

Where:
- `|` (`NSSep`) separates nesting levels.
- `:` (`NSEnd`) separates the node name from its deterministic task ID.

### Example Namespace Hierarchy:
- **Root Graph**: `""` (empty string)
- **Child Subgraph Node** named `"researcher"`:  
  `"researcher:4f8a12bc..."`
- **Nested Child Subgraph Node** named `"web_search"`:  
  `"researcher:4f8a12bc...|web_search:9c2d34ef..."`

### Key Namespace Properties:
1. **Resume Stability**: When an interrupt occurs inside a sub-graph, the execution step counter is preserved. When resumed, the child sub-graph receives the exact same `CheckpointNS` and resumes directly from its paused state.
2. **Loop Isolation**: When a graph loops back to a node across distinct execution steps, the incremented step counter generates a distinct `TaskID`, preventing namespace collision between iterations.
3. **Node Name Restrictions**: Because `|` and `:` are reserved delimiters for namespace segmentation, node names **must not** contain `|` or `:`. Slashes (e.g., `"search/web"`, `"agent/writer"`) and standard alphanumeric characters are permitted.

---

## 4. Human-in-the-Loop & Resuming Execution

### Pausing with `graph.Interrupt` or `graph.InterruptUpdate`

You can pause a graph's execution using `graph.Interrupt()` or `graph.InterruptUpdate[S]()`. Loom immediately stops execution, persists the state to durable storage, and returns the snapshot:

```go
builder.AddNode("Approval", graph.NodeFunc[MyState](func(ctx context.Context, s MyState) (graph.Command[MyState], error) {
    if !s.IsApproved {
        // Stop execution and save checkpoint
        return graph.InterrupCmd[MyState]{}, nil
    }
    return graph.Update[MyState](func(s MyState) MyState { return s }), nil
}))
```

### Resuming from a Checkpoint

When a graph is interrupted or finishes, it returns a `Snapshot[State]`. You can resume execution from the returned `snapshot.Location` by providing the resume location to `Execute`:

```go
// 1. Initial execution
snapshot, err := g.Execute(ctx, initialCmd, &graph.Location{ThreadID: "order-1234"})
threadID := snapshot.Location.ThreadID

// ... Later (e.g. human approves the order via HTTP webhook) ...

// 2. Resume using the thread's last checkpoint location
resumeLocation := &graph.Location{
    ThreadID:     threadID,
    CheckpointNS: snapshot.Location.CheckpointNS,
}

approvalUpdate := graph.Update[MyState](func(s MyState) MyState {
    s.IsApproved = true
    return s
})

resumedSnapshot, err := g.Execute(ctx, approvalUpdate, resumeLocation)
```

---

## Best Practices

- **State Serialization**: Ensure all fields in your state struct have appropriate JSON tags.
- **Thread Management**: Use unique `ThreadID`s to isolate different user sessions or independent tasks.
- **Idempotency**: Since a node might be re-executed upon retry or resumption, ensure node functions are idempotent.
- **Sub-graph Hygiene**: Avoid using `|` or `:` in node names to prevent namespace delimiter conflicts.
