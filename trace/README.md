# trace

The `trace` package provides execution logging for Loom graphs and LLM interactions. It is designed to capture detailed snapshots of execution stages, making it easier to debug complex agentic workflows.

## Usage

Trace entries are appended to a JSONL file (defaulting to `logs/stream_chain.jsonl`).

```go
// Add a trace entry with arbitrary data
trace.Append(ctx, "my-component", "processing-start", map[string]any{
    "input": myInput,
})
```

## Session Identification

Traces can be associated with a `SessionID` stored in the context, allowing you to group related logs across multiple node executions.

```go
ctx = trace.WithSession(ctx, "session-123")
trace.Append(ctx, "agent", "started", nil)
```

## Log Format

Each line in the log is a JSON object:

```json
{
  "timestamp": "2024-03-20T10:00:00.123456Z",
  "session_id": "session-123",
  "component": "agent",
  "stage": "started",
  "data": null
}
```
