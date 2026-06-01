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
*   **Routing Edges**: Use `AddRouteEdge` for dynamic, multi-destination branching based on state.

### Example: Advanced Graph Construction
```go
builder := graph.New[MyState]().
    AddNode("llm", llmNode).
    AddNode("human_approval", func(ctx context.Context, s MyState) (graph.Command[MyState], error) {
        return graph.Interrupt(), nil // Pause for human check
    }).
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

## 2. Commands (`graph` package)
Commands are the instructions returned by nodes to the Loom engine. They define state mutations and execution control.

### Types of Commands
*   **`graph.Update[State]`**: The primary way to mutate state. It takes a transformation function: `func(State) State`.
*   **`graph.Interrupt()`**: Immediately pauses graph execution. Loom saves the current state to the checkpointer and returns the `Snapshot` to the caller. Essential for Human-In-The-Loop (HITL) workflows.

### Example: Using Commands
```go
func myNode(ctx context.Context, s MyState) (graph.Command[MyState], error) {
    if s.Score < 0.5 {
        return graph.Interrupt(), nil // Pause for review
    }
    return graph.Update(func(s MyState) MyState {
        s.Score += 0.1
        return s
    }), nil
}
```

## 3. LLM Interaction & Registry (`llm` package)
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
    return graph.Update(func(s MyState) MyState {
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

## 4. Tool Development & Container (`tool` package)
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

## 5. Memory & Context Management (`memory` & `message` package)
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

## 6. Streaming & Events (`graph` package)
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
Nodes can emit arbitrary events via the `StreamWriter` in the context.
```go
func myNode(ctx context.Context, s MyState) (graph.Command[MyState], error) {
    if writer, ok := graph.StreamWriterFromContext(ctx); ok {
        _ = writer.WriteEvent(ctx, "started", nil)
    }
    return graph.Update(func(s MyState) MyState { return s }), nil
}
```

## 7. Persistence & Resumption (`checkpoint` package)
Enable durable workflows that can resume after an interruption.

### Example: SQLite Checkpointing
```go
import "github.com/masterkeysrd/loom/checkpoint/sqlite"

db, _ := sql.Open("sqlite3", "agent.db")
cp, _ := sqlite.NewCheckpointer(db)

g, _ := builder.WithCheckpointer(cp).Build()

// Execute and get thread location
snapshot, _ := g.Execute(ctx, graph.Update(initial), nil)
threadID := snapshot.Location.ThreadID

// Resume later from checkpoint
nextSnapshot, _ := g.Execute(ctx, nil, &snapshot.Location)
```

## 8. Provider Implementation (Core Extension)
To update a provider (e.g., `llm/openai/mappings.go`):
1.  **Request Mapping**: Translate `llm.Request` to the provider's `Params` struct.
2.  **Multimodal**: Map media blocks (Image, Audio, Document).
3.  **Structured Output**: Implement native schema enforcement (e.g., `json_schema`).

## Engineering Standards
*   **Immutability**: Always call `.clone()` before modifying shared configs.
*   **Validation**: Every feature change must be verified with `go test ./...`.
*   **Traceability**: Use `trace.Append(ctx, "domain", "op", data)` for observability.
