# Visualization & Observability 🔍

Loom provides built-in tools to help you understand your graph's structure and debug its execution in real-time.

## 1. Graph Visualization (Mermaid)

Loom can automatically generate diagrams of your workflows using [Mermaid](https://mermaid.js.org/) syntax. This is incredibly useful for documentation and for verifying your graph's logic.

### Generating Mermaid Code

```go
g, _ := builder.Build()

// Get the raw Mermaid string
fmt.Println(g.ToMermaid())

// Get a URL to render the diagram immediately
fmt.Println("View Diagram:", g.MermaidURL())
```

### Visualizing Edges
- **Direct Edges**: Shown as solid arrows (`-->`).
- **Conditional Edges**: Shown as dotted arrows (`-.->`).
- **Route Edges**: Shown with labels for each possible path.

## 2. Observability & Tracing

The `trace` package provides a lightweight way to log the execution flow of your agents. By default, it writes logs to `logs/stream_chain.jsonl`.

### Using the Trace Package

You can attach a `SessionID` to a context to group related events.

```go
import "github.com/masterkeysrd/loom/telemetry"

// Initialize telemetry (usually in main)
shutdown, _ := telemetry.Init(ctx, telemetry.Config{ServiceName: "my-agent"})
defer shutdown(ctx)

// Start a span
ctx, span := telemetry.Start(ctx, "my-operation")
defer span.End()

// Log a custom attribute
span.SetAttributes(telemetry.WithLoomThread("thread-123"))
```

### Automatic Tracing

Loom's `Model` and `Graph` packages automatically emit OpenTelemetry spans and metrics for:
- LLM requests (using GenAI semantic conventions).
- Node entry and exit.
- Graph execution durations and node invocations.

### Example Log Entry

```json
{
  "timestamp": "2024-05-31T12:00:00Z",
  "session_id": "user-session-123",
  "component": "model",
  "stage": "invoke_complete",
  "data": { "message_id": "...", "metrics": { ... } }
}
```

## 3. Best Practices

- **Visualize Early**: Use `g.MermaidURL()` during development to ensure your graph matches your mental model.
- **Session IDs**: Always use `WithSession` to make it easier to trace a single user's journey through multiple graph executions.
- **Log Rotation**: Since Loom logs to a file, ensure you have a strategy for log rotation in production environments.
