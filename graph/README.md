# graph

The `graph` package is the core workflow engine of Loom. It provides the primitives for defining, executing, and persisting multi-step AI agent workflows as directed graphs.

## Architecture

Loom's engine is built on a "State-First" architecture. Instead of managing complex control flow inside application logic, you define a graph where each node transforms a shared state.

### Key Components

| Component | Responsibility |
|---|---|
| `Graph[State]` | The immutable execution engine. |
| `Builder[State]` | A fluent API for wiring nodes and edges. |
| `Node[State]` | A unit of work that processes state and returns a `Command`. |
| `Edge` | A connection between nodes, can be direct or conditional. |
| `Command[State]` | Instructions to the engine (e.g., `Update` state, `Interrupt` execution). |
| `Snapshot[State]` | The complete state of a graph run at any given point. |

## Defining a Graph

Graphs are built using a `Builder` and a user-defined `State` struct.

```go
type MyState struct {
    Query    string
    Response string
}

builder := graph.New[MyState]().
    WithName("search-agent").
    AddNode("search", searchNode).
    AddNode("summarize", summarizeNode).
    AddEdge(graph.START, "search").
    AddEdge("search", "summarize").
    AddEdge("summarize", graph.END)

g, err := builder.Build()
```

## Conditional Edges

Conditional edges allow for branching logic based on the current state.

```go
builder.AddConditionalEdge("think", "act", func(s MyState) bool {
    return s.Confidence > 0.8
})
builder.AddConditionalEdge("think", "refine", func(s MyState) bool {
    return s.Confidence <= 0.8
})
```

## Checkpointing and Resumption

One of Loom's most powerful features is the ability to persist execution state. By attaching a `Checkpointer`, every node transition is saved.

```go
g, _ := graph.New[MyState]().
    WithCheckpointer(myCheckpointer).
    // ...
    Build()

// Execute and get the final location
snapshot, _ := g.Execute(ctx, initialUpdate, nil)

// Resume later from the same location
newSnapshot, _ := g.Execute(ctx, nil, &snapshot.Location)
```

## Real-time Streaming

`Graph.Stream` produces an iterator of `StreamEvent` values, allowing you to stream LLM tokens, tool progress, or custom node events to a client.

```go
events, _ := g.Stream(ctx, input, nil)
for event, err := range events {
    // event.Source tells you which model or tool produced the event (e.g. "llm:gpt-4o")
    if event.Event == graph.EventLLMChunk {
        chunk := event.Data.(message.AssistantChunk)
        fmt.Print(chunk.Content.Text())
    }
}
```

## Execution Safety

Loom provides built-in safety controls to prevent runaway workflows and respect context deadlines:

- **Max Steps Limit**: Configure `WithMaxSteps(limit)` on the builder or dynamically with `g.SetMaxSteps(limit)`. If execution exceeds this limit (virtual `START` node is not counted), `Graph.Execute` aborts and returns an error wrapping `ErrMaxStepsExceeded`.
- **Context Cancellation**: `Graph.Execute` checks `ctx.Err()` at the start of every execution step and aborts immediately if canceled or timed out.

```go
g, err := graph.New[MyState]().
    WithMaxSteps(25). // Abort if execution exceeds 25 node steps
    // ...
    Build()

snapshot, err := g.Execute(ctx, input, nil)
if errors.Is(err, graph.ErrMaxStepsExceeded) {
    log.Printf("workflow exceeded step limit: %v", err)
}
```

## Node Middleware & Retries

Middlewares allow intercepting and wrapping node executions. Middlewares are executed in order (the first middleware is outermost) and apply only to real user nodes (virtual `START` and `END` nodes are not wrapped).

```go
loggingMiddleware := func(next graph.Node[MyState]) graph.Node[MyState] {
    return graph.NodeFunc(func(ctx context.Context, state MyState) (graph.Command[MyState], error) {
        log.Println("entering node")
        cmd, err := next.Execute(ctx, state)
        log.Println("exiting node")
        return cmd, err
    })
}

builder.WithMiddleware(loggingMiddleware)
```

### Retry Middleware

`RetryMiddleware` provides exponential backoff retries for transient node failures. Before each retry attempt, it passes an isolated `state.Copy()` to ensure failed attempts do not corrupt state.

```go
retryPolicy := graph.RetryMiddleware[MyState](
    graph.WithMaxAttempts(3),                  // Total attempts including initial call (default: 3)
    graph.WithInitialWait(100*time.Millisecond), // Initial backoff (default: 100ms)
    graph.WithMaxWait(5*time.Second),          // Max backoff duration (default: 5s)
    graph.WithMultiplier(2.0),                 // Exponential backoff multiplier (default: 2.0)
    graph.WithShouldRetry(func(err error) bool {
        // Optional filter: return true for retryable errors
        return !errors.Is(err, ErrInvalidInput)
    }),
)

g, _ := graph.New[MyState]().
    WithMiddleware(retryPolicy).
    // ...
    Build()
```

If context cancellation or deadline expiration occurs, `RetryMiddleware` aborts immediately without waiting or retrying.

## Sub-graphs (`AsNode` and `MapNode`)

Loom allows entire compiled graphs to be embedded as nodes within other graphs using `AsNode()`. This enables modular workflow composition, reusable agent building blocks, and nested hierarchical architectures.

### Same State Embedding

When a sub-graph operates on the same state type as the parent graph, convert it directly with `childGraph.AsNode()`:

```go
childGraph, _ := childBuilder.Build()

parentBuilder := graph.New[MyState]().
    AddNode("setup", setupNode).
    AddNode("subgraph", childGraph.AsNode()).
    AddNode("teardown", teardownNode).
    AddEdge(graph.START, "setup").
    AddEdge("setup", "subgraph").
    AddEdge("subgraph", "teardown").
    AddEdge("teardown", graph.END)
```

- **Namespace Scoping**: When executing inside a parent graph with a checkpoint location, the sub-graph automatically scopes its checkpoint namespace using LangGraph-style hierarchical segmentation: `<node_name>:<task_id>` (joined across nesting levels with `|`, e.g., `parent_ns|subgraph:<task_id>` or `sub1:<task_id_1>|sub2:<task_id_2>`).
  - `task_id` is deterministically derived from `(ThreadID, CheckpointNS, Step, NodeName)` using SHA-1.
  - On interrupt and resume, the step counter is preserved, guaranteeing the resumed sub-graph receives the **exact same `CheckpointNS`** and resumes from its paused checkpoint.
  - On loops across distinct steps, different task IDs are minted, preventing namespace collisions across iterations.
- **Node Name Validation**: User-defined node names cannot contain `|` or `:` as these characters are reserved for sub-graph namespace delimiters (`|` as `NSSep` and `:` as `NSEnd`). Slashes (e.g. `"tools/search"`) and other alphanumeric characters are fully supported.
- **Interrupt Propagation**: If a node in the sub-graph returns an interrupt (such as `InterruptCmd` or `InterruptUpdate`), the sub-graph execution pauses, and `AsNode` returns an `InterruptUpdate` command that pauses the parent graph while preserving all state modifications made up to that point.

### State Mapping with `MapNode`

When a sub-graph operates on a different, specialized child state, adapt it to the parent graph's state using `MapNode`:

```go
type ParentState struct {
    UserID string
    Result int
}

type ChildState struct {
    Inputs []int
    Total  int
}

// Map parent state to child state and back
subNode := graph.MapNode(
    childGraph.AsNode(),
    func(parent ParentState) ChildState {
        return ChildState{Inputs: []int{10, 20, 30}}
    },
    func(parent ParentState, child ChildState) ParentState {
        parent.Result = child.Total
        return parent
    },
)

parentBuilder.AddNode("calculator", subNode)
```

`MapNode` seamlessly propagates interrupts and state mutations from the child node back to the parent state.

## Parallel Node Execution (Fan-out / Fan-in)

Loom natively supports concurrent node execution. When multiple edges originate from the same node (or from `START`), the engine executes all target nodes concurrently in separate goroutines.

```go
builder := graph.New[MyState]()

// Fan-out: START branches into three concurrent tasks
builder.AddEdge(graph.START, "fetchDocs")
builder.AddEdge(graph.START, "fetchUserHistory")
builder.AddEdge(graph.START, "checkPermissions")

// Fan-in: all branches converge on a single join node
builder.AddEdge("fetchDocs", "aggregate")
builder.AddEdge("fetchUserHistory", "aggregate")
builder.AddEdge("checkPermissions", "aggregate")
builder.AddEdge("aggregate", graph.END)
```

### Key Semantics:
- **State Isolation**: Each parallel node receives its own deep copy of the state (`state.Copy()`), preventing concurrent data races during execution.
- **Deterministic Merge**: Commands returned by parallel nodes are applied in deterministic slice order upon completion.
- **Fan-in Deduplication**: When multiple parallel edges converge on the same downstream node, Loom deduplicates the next-node list while preserving encounter order, ensuring the downstream join node is executed only once.
- **Error Propagation**: If any parallel branch returns an error, the execution aborts and returns the first encountered error wrapped as `failed to execute node: ...`.
- **Interrupt Handling**: If any concurrent node returns an interrupt command, the combined command is treated as an interrupt, pausing execution and saving the merged state.
- **Step Counting**: All executed parallel nodes are accounted for when checking `WithMaxSteps`.

