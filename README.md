# loom 🧵

Loom is a Go library for building AI workflows as graphs. Connect functions and model calls, branch on their results, and add a checkpointer to pause work and resume it across processes.

[Run your first graph](https://masterkeysrd.github.io/loom/guides/quickstart/) · [Documentation](https://masterkeysrd.github.io/loom/) · [Go API reference](https://pkg.go.dev/github.com/masterkeysrd/loom#section-directories)

## Key Features

- **🕸️ Graph-based Workflows**: Define agents as directed graphs with nodes and conditional edges.
- **💾 State Persistence**: Built-in checkpointing with support for PostgreSQL and SQLite.
- **🖥️ Loom Studio**: Built-in Trace Explorer and Metrics Dashboard for real-time observability.
- **📺 Real-time Streaming**: First-class support for token-level streaming and event-based updates.
- **🧠 Advanced Memory Management**:
    - **Automatic Summarization**: Intelligently condense long conversations when token limits are reached.
    - **Precise Trimming**: Flexible message trimming strategies (e.g., sliding window) to fit context windows.
- **🔌 Provider Agnostic**: One interface for OpenAI, Anthropic, Ollama, Google Gemini, and Claude Code CLI.
- **🔌 MCP Support**: Native integration with the Model Context Protocol for tools, resources, and prompts.
- **🛠️ Tool Integration**: Native support for tool calling and tool-use loops.
- **🛡️ Type Safe**: Built from the ground up with Go generics for maximum developer productivity and safety.

## Quick start

From a checkout of this repository with Go 1.26.10 or newer, run the local greeting workflow.

```sh
go run ./examples/quickstart
```

```text
Hello, Ada!
```

To build it in your own project, follow the [quick start](https://masterkeysrd.github.io/loom/guides/quickstart/). To make a model call, [choose a provider](https://masterkeysrd.github.io/loom/providers/).

## 🖥️ Loom Studio

Loom Studio is a built-in control plane for your agents. It provides:

- **Dashboard**: Real-time stats on token usage, model latency (P50), and tool invocations.
- **Trace Explorer**: A high-fidelity waterfall viewer to audit every decision and tool call.
- **Metrics Explorer**: Deep-dive into telemetry with temporal bucketing and attribute filtering.

To start the studio:

```bash
loom studio
```

Then visit `http://localhost:8080`.

### Content Recording (Opt-in)

By default, Loom masks sensitive content (prompts and tool payloads). To see raw messages in the Studio:

```go
// Wrap your context to enable rich content recording
ctx = telemetry.WithContentRecording(ctx)
```

## Core Concepts

### The Graph

A `Graph[State]` is a network of **nodes** connected by **edges**.
- **Nodes**: Units of work that receive the current state and return a `Command`. Wrap functions with `graph.NodeFunc`.
- **Edges**: Transitions between nodes: unconditional (`AddEdge`), boolean predicates (`AddConditionalEdge`), or dynamic key dispatch (`AddRouteEdge`).
- **Commands**: Instructions to the engine on how to update state or control flow (`Update`, `Interrupt`, `InterruptUpdate`).

### Parallel Node Execution (Fan-out / Fan-in)

Loom executes multiple outgoing edges concurrently using goroutines:
- **State Isolation**: Every parallel branch receives an isolated `state.Copy()`.
- **Deterministic Merge**: Commands returned by parallel nodes are applied deterministically in edge slice order.
- **Fan-in Deduplication**: Downstream join nodes with multiple incoming edges are deduplicated and executed exactly once.
- **Interrupt Handling**: If any concurrent node returns an interrupt command, the combined step pauses execution with `InterruptUpdate`.

### Sub-graphs as Nodes (`AsNode` & `MapNode`)

Compiled graphs can be nested as nodes within parent graphs:
- **Same-State Sub-graphs**: Convert directly via `childGraph.AsNode()`.
- **State Mapping**: Adapt different child state structs using `graph.MapNode(childGraph.AsNode(), toChild, fromChild)`.
- **Hierarchical Namespaces**: Checkpoint namespaces follow LangGraph-style segmentation: `<node_name>:<task_id>` joined with `|` across levels (e.g. `research:4f8a...|web_search:9c2d...`).
- **Interrupt Propagation**: Child interrupts bubble up automatically as `InterruptUpdate`, pausing the parent and resuming cleanly from the exact child checkpoint.

### Execution Safety (`WithMaxSteps`)

Guard cyclic agent workflows against runaway execution loops:
- `builder.WithMaxSteps(limit)` or `g.SetMaxSteps(limit)` enforces a hard cap on user node steps.
- If exceeded, execution aborts and returns an error wrapping `graph.ErrMaxStepsExceeded`.
- Loom checks `ctx.Err()` at every step boundary and aborts immediately upon context cancellation or timeout.

### Node Middleware & Retries (`RetryMiddleware`)

Wrap nodes with reusable interceptors via `builder.WithMiddleware(...)`:
- Middlewares execute outermost-first around user nodes.
- Built-in `RetryMiddleware` provides exponential backoff (`WithMaxAttempts`, `WithInitialWait`, `WithMaxWait`, `WithMultiplier`, `WithShouldRetry`) with isolated state copies per attempt.

### Checkpointing & Resumption

Loom saves a "checkpoint" of your state after every node execution. This allows you to:
- Resume interrupted workflows from a saved `Location`.
- Implement human-in-the-loop workflows with `graph.Interrupt()` or `graph.InterruptUpdate[S]()`.
- Audit the entire execution history of an agent.
- Default to `graph.NoopCheckpointer` for zero-overhead in-memory runs.

```go
// Using SQLite for checkpointing
db, _ := sql.Open("sqlite3", "loom.db")
cp, _ := sqlite.NewCheckpointer(db)

g, _ := graph.New[MyState]().
    WithCheckpointer(cp).
    WithMaxSteps(25).
    // ...
    Build()
```

### Advanced Memory

As conversations grow, they can exceed the LLM's context window. `loom` provides automated tools to manage this:

```go
// Automatically summarize the conversation when it hits 4000 tokens
summarizer, _ := memory.NewSummarizer(model, memory.SummarizerConfig{
    TokenCounter: counter,
    Triggers: []memory.SummarizerTrigger{
        memory.TriggerSummaryOnTokenCount(4000),
    },
})

// Or trim messages to fit a window
trimmed, _ := message.TrimMessages(ctx, history, 4000, &message.TrimConfig{
    Strategy:      message.TrimStrategyLast,
    IncludeSystem: true,
})
```

## Examples

Explore runnable end-to-end examples in the [`examples/`](./examples/) directory:

| Example | Path | Description |
|---|---|---|
| **RAG Agent** | [`examples/rag`](./examples/rag) | Lazy Markdown ingestion, hybrid BM25 + cosine vector retrieval, and RAG graph workflow (`go run ./examples/rag -provider ollama`). |
| **Graph Agent** | [`examples/graph_agent`](./examples/graph_agent) | Directed agent loop with tool invocation, conditional routing, and state checkpointing. |
| **Decision Models** | [`examples/decision`](./examples/decision) | Ultra-fast System One probabilistic classification and graph routing without LLM token overhead. |
| **Tool Streaming** | [`examples/tool-streaming`](./examples/tool-streaming) | Real-time token streaming and interactive tool calling. |
| **Tool Search** | [`examples/tool_search`](./examples/tool_search) | Tool-driven web search with RRF fusion across BM25, regex, and server backends. |
| **Claude Code** | [`examples/claudecode`](./examples/claudecode) | Interactive REPL backed by the Claude Code CLI (`claude`) with session snapshot/fork for parallel graph branches. |
| **Codex** | [`examples/codex`](./examples/codex) | Sign in with ChatGPT, list models, and run a streaming chat with a local tool. |
| **GitHub Copilot** | [`examples/copilot`](./examples/copilot) | Interactive REPL backed by the official GitHub Copilot Go SDK (`copilot` CLI or BYOK endpoints). |
| **Mini REPL** | [`examples/mini_repl`](./examples/mini_repl) | Interactive terminal chat REPL with multi-turn conversation memory. |

## Supported Providers

| Provider | Package |
|---|---|
| OpenAI | `github.com/masterkeysrd/loom/providers/openai` |
| Codex | `github.com/masterkeysrd/loom/providers/codex` |
| Anthropic | `github.com/masterkeysrd/loom/providers/anthropic` |
| Google Gemini | `github.com/masterkeysrd/loom/providers/genai` |
| Google Antigravity | `github.com/masterkeysrd/loom/providers/antigravity` |
| Ollama | `github.com/masterkeysrd/loom/providers/ollama` |
| TypeSafe AI | `github.com/masterkeysrd/loom/providers/typesafe` |
| Claude Code | `github.com/masterkeysrd/loom/providers/claudecode` |
| GitHub Copilot | `github.com/masterkeysrd/loom/providers/copilot` |

## License

MIT
