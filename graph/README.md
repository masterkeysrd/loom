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
