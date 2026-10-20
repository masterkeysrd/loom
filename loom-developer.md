---
apiVersion: warp/v1alpha1
kind: Skill
metadata:
  name: loom-developer
  description: Expert guidance for developing applications and extending the Loom graph-based AI engine. Use it when you need to build stateful AI agents, implement directed graphs with conditional logic, manage conversation memory, or add support for new multimodal LLM providers.
  displayName: Loom Developer
---

# Loom Developer Skill

This skill provides expert guidance and concrete examples for building applications with Loom and maintaining the core framework.

## 1. Graph Orchestration (`graph` package)
Loom agents are directed graphs where shared state is transformed by nodes.

### Core Workflow Concepts
*   **START & END**: Use `graph.START` and `graph.END` as virtual entry/exit points for your builder.
*   **Node Functions**: Wrap Go functions using `graph.NodeFunc[State]` to implement `graph.Node[State]`.
*   **Routing Edges**: Use `AddRouteEdge` for dynamic, multi-destination branching based on state.
*   **Parallel Execution (Fan-out / Fan-in)**: Multiple outgoing edges branch into concurrent goroutines. Each node receives an isolated `state.Copy()`. Commands are applied deterministically in edge order, and converging fan-in join nodes are deduplicated to run only once.
*   **Sub-graphs as Nodes**:
    *   **Same State**: Embed child graphs directly using `childGraph.AsNode()`.
    *   **Different State**: Adapt states using `graph.MapNode(childGraph.AsNode(), toChild, fromChild)`.
    *   **Hierarchical Checkpoint Namespaces**: Namespaces follow LangGraph-style segmentation `<node_name>:<task_id>` joined by `|` (e.g. `research:4f8a...|web_search:9c2d...`). Task IDs are deterministically derived via SHA-1 of `(ThreadID, CheckpointNS, Step, NodeName)`. User node names cannot contain `|` or `:` (`/` is permitted).

### Example: Advanced Graph Construction
```go
builder := graph.New[MyState]().
    AddNode("llm", llmNode).
    AddNode("human_approval", graph.NodeFunc[MyState](func(ctx context.Context, s MyState) (graph.Command[MyState], error) {
        return graph.Interrupt(), nil // Pause for human check
    })).
    // Route to different nodes based on state
    AddRouteEdge("llm", func(s MyState) (string, error) {
        if s.NeedsTool { return "tools", nil }
        if s.NeedsApproval { return "human_approval", nil }
        return graph.END, nil
    }, map[string]string{
        "tools":          "tool_node",
        "human_approval": "human_approval",
        graph.END:       graph.END,
    })
```

### Example: Parallel Fan-out / Fan-in & Sub-graphs
```go
// Fan-out: START branches to concurrent workers
builder.AddEdge(graph.START, "worker_a")
builder.AddEdge(graph.START, "worker_b")

// Subgraph embedded as a worker node
builder.AddNode("worker_b", subGraph.AsNode())

// Fan-in: both branches converge on single join node (deduplicated)
builder.AddEdge("worker_a", "join")
builder.AddEdge("worker_b", "join")
builder.AddEdge("join", graph.END)
```

## 2. Commands (`graph` package)
Commands are the instructions returned by nodes to the Loom engine. They define state mutations and execution control.

### Types of Commands
*   **`graph.Update[State]`**: The primary way to mutate state. It takes a transformation function: `func(State) State`.
*   **`graph.Interrupt()`**: Immediately pauses graph execution. Loom saves the current state to the checkpointer and returns the `Snapshot` to the caller. Essential for Human-In-The-Loop (HITL) workflows.
*   **`graph.InterruptUpdate[State]`**: Atomically modifies state and pauses execution with an interrupt. Used when recording intermediate outputs or propagating child subgraph interrupts.

### Example: Using Commands
```go
func myNode(ctx context.Context, s MyState) (graph.Command[MyState], error) {
    if s.Score < 0.5 {
        // Mutate status and pause atomically
        return graph.InterruptUpdate[MyState](func(state MyState) MyState {
            state.Status = "requires_review"
            return state
        }), nil
    }
    return graph.Update[MyState](func(s MyState) MyState {
        s.Score += 0.1
        return s
    }), nil
}
```

## 3. Execution Safety & Middleware (`graph` package)

### Step Limiting (`WithMaxSteps` & `ErrMaxStepsExceeded`)
Prevent infinite execution loops in cyclic or self-reflecting graphs:
```go
builder.WithMaxSteps(25) // Abort if user steps exceed limit

g, _ := builder.Build()
snapshot, err := g.Execute(ctx, initialCmd, nil)
if errors.Is(err, graph.ErrMaxStepsExceeded) {
    log.Printf("step limit exceeded: %v", err)
}
```
Loom also checks `ctx.Err()` at the start of each step to guarantee immediate cancellation on context timeouts.

### Node Middleware & Retries (`RetryMiddleware`)
Middlewares wrap user nodes in registered order (outermost first):
```go
retryPolicy := graph.RetryMiddleware[MyState](
    graph.WithMaxAttempts(3),                  // Total attempts including initial call
    graph.WithInitialWait(100*time.Millisecond), // Initial backoff duration
    graph.WithMaxWait(5*time.Second),          // Max backoff duration
    graph.WithMultiplier(2.0),                 // Exponential multiplier
    graph.WithShouldRetry(func(err error) bool {
        return !errors.Is(err, ErrInvalidInput) // Custom retry predicate
    }),
)

builder.WithMiddleware(retryPolicy)
```
`RetryMiddleware` passes a cloned `state.Copy()` before each retry to ensure failed executions do not pollute state.

## 4. LLM Interaction & Registry (`llm` package)
Loom provides a high-level `Model` wrapper with a fluent API for cross-provider compatibility.

### Usage in Graph Nodes
Nodes typically use `model.Invoke` for a blocking response or `model.Stream` for real-time updates.
*   **Dependency Injection**: Pass the `Model` or `Provider` into the node function via a closure when registering it with the builder.

```go
func llmNode(ctx context.Context, s MyState) (graph.Command[MyState], error) {
    // 1. Invoke the model
    resp, err := model.Invoke(ctx, s.Messages)
    if err != nil { return nil, err }

    // 2. Access metrics if needed
    if resp.Metrics != nil {
        fmt.Printf("Used %d tokens\n", resp.Metrics.TotalTokens)
    }

    // 3. Update state with the response
    return graph.Update[MyState](func(s MyState) MyState {
        s.Messages = append(s.Messages, resp)
        return s
    }), nil
}
```

### Token Metrics
Usage statistics are captured in `message.TokenMetrics` and attached to the final `Assistant` message.
*   **Fields**: `Tokens.Input`, `Tokens.Output`, `TotalTokens`, `Tokens.Reasoning`, etc.
*   **Cost**: `TotalCost`, `Cost.Input`, `Cost.Output` (Estimated USD).
*   **Timing**: `Timing.Total`, `Timing.Processing`, `Timing.Generation`.

### Message Roles
Loom supports the following standard roles in `message.Role`:
*   `RoleSystem`: Instructions for the model.
*   `RoleUser`: Input from the human.
*   `RoleAssistant`: Output from the LLM.
*   `RoleTool`: Result of a tool execution.

### Provider Registry
The `Registry` provides a decoupled way to instantiate providers.
```go
registry := llm.NewRegistry()
registry.Register("openai", func() (llm.Provider, error) {
    return loomopenai.NewDefaultProvider()
})
provider, _ := registry.Get("openai")
```

## 5. Tool Development & Container (`tool` package)
Tools allow LLMs to interact with the real world. Loom uses reflection to infer schemas and supports security annotations.

### Tools Container
Use the `Container` to group tools and execute them safely.
```go
container := tool.NewContainer(searchTool, calculatorTool)

// Bind definitions to model
model = model.BindToolDefs(container.Definitions()...)

// In a node, execute a tool call
result, err := container.Call(ctx, assistantToolCall)
```

### Example: Creating a Tool
```go
type SearchInput struct { Query string `json:"query"` }
type SearchOutput struct { Results []string `json:"results"` }

searchTool, _ := tool.New("web_search", "Web Search", "Searches the internet",
    func(ctx context.Context, in SearchInput) (SearchOutput, error) {
        return SearchOutput{Results: []string{"Result 1"}}, nil
    },
    tool.WithAnnotation(tool.Annotation{
        IsReadOnly: true,
        UserHint:   "Searching the web...",
    }),
)
```

## 6. Memory & Context Management (`memory` & `message` package)
Manage token limits with automated trimming and summarization.

### Example: Automated Summarizer
```go
summarizer, _ := memory.NewSummarizer(model, memory.SummarizerConfig{
    TokenCounter: modelProvider.(llm.TokenCounter),
    Triggers: []memory.SummarizerTrigger{
        memory.TriggerSummaryOnTokenCount(4000),
    },
})

// In a node
history, _ := summarizer.Summarize(ctx, s.Messages)
```

## 7. Streaming & Events (`graph` package)
Loom supports real-time streaming of LLM tokens and custom node events.

### Graph Streaming
Use `g.Stream` instead of `g.Execute` to receive an iterator of `StreamEvent`.
```go
events, _ := g.Stream(ctx, initialCommand, nil)
for event, err := range events {
    if event.Event == graph.EventLLMChunk {
        chunk := event.Data.(message.AssistantChunk)
        fmt.Print(chunk.Content.Text())
    }
}
```

### Emitting Events from Nodes
Nodes can emit arbitrary events via the `stream.Writer` in the context.
```go
func myNode(ctx context.Context, s MyState) (graph.Command[MyState], error) {
    if writer, ok := stream.WriterFromContext(ctx); ok {
        _ = writer.Write(ctx, stream.Event{Name: "started", Data: nil})
    }
    return graph.Update[MyState](func(s MyState) MyState { return s }), nil
}
```

## 8. Persistence & Resumption (`checkpoint` package)
Enable durable workflows that can resume after an interruption.

### Example: SQLite Checkpointing
```go
import "github.com/masterkeysrd/loom/checkpoint/sqlite"

db, _ := sql.Open("sqlite3", "agent.db")
cp, _ := sqlite.NewCheckpointer(db)

g, _ := builder.WithCheckpointer(cp).Build()

// Execute and get thread location
snapshot, _ := g.Execute(ctx, graph.Update[MyState](initial), nil)
threadID := snapshot.Location.ThreadID

// Resume later from checkpoint
nextSnapshot, _ := g.Execute(ctx, nil, &snapshot.Location)
```

## 9. RAG: Documents, Vector Stores & Retrievers (`document`, `vectorstore`, `retriever`)
Loom provides lazy document ingestion (`iter.Seq2`), chunking, vector indexing, and composable retrieval.

```go
// 1. Load & split documents lazily
loader := document.NewFSLoader(os.DirFS("./docs"), "*.md")
splitter := document.NewMarkdownSplitter(document.WithChunkSize(800))
chunks, _ := document.LoadAll(ctx, document.LoaderFunc(func(ctx context.Context) iter.Seq2[document.Document, error] {
    return document.SplitIter(loader.Load(ctx), splitter)
}))

// 2. Index in a VectorStore (memory, sqlite, or pg)
vs := memory.New(embedModel)
_, _ = vs.Add(ctx, chunks)

// 3. Retrieve (or expose as a Tool via retriever.NewTool)
ret := retriever.FromVectorStore(vs, retriever.WithTopK(4))
docs, _ := ret.Retrieve(ctx, "How do checkpoints work?")
```

See [`examples/rag`](./examples/rag) for a complete runnable RAG workflow (`go run ./examples/rag -provider ollama`).

## 10. Provider Implementation (Core Extension)
To update a provider (e.g., `providers/openai/mappings.go`):
1.  **Request Mapping**: Translate `llm.Request` to the provider's `Params` struct.
2.  **Multimodal**: Map media blocks (Image, Audio, Document).
3.  **Structured Output**: Implement native schema enforcement (e.g., `json_schema`).

## 11. End-to-End Examples (`examples/`)
Loom includes fully functional examples demonstrating framework features:
*   `examples/rag`: Complete RAG pipeline with lazy Markdown loading, BM25 + cosine hybrid retrieval with RRF, and graph generation. Run via `go run ./examples/rag -provider ollama`.
*   `examples/graph_agent`: Stateful agent loop with tool container execution and SQLite checkpointing.
*   `examples/decision`: System One fast probabilistic decision routing without LLM token generation overhead.
*   `examples/tool-streaming`: Real-time streaming with interactive tool call progress.
*   `examples/mini_repl`: Terminal chat agent with context trimming and summarization.

## Engineering Standards
*   **Immutability**: Always call `.clone()` before modifying shared configs.
*   **Validation**: Every feature change must be verified with `go test ./...`.
*   **Traceability**: Use `trace.Append(ctx, "domain", "op", data)` for observability.
