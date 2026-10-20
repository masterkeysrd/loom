# loom 🧵

`loom` is a high-performance, graph-based AI workflow engine for Go. It allows you to build complex agentic systems by defining multi-step workflows as directed graphs, seamlessly connecting them to any large language model, and persisting execution state for long-running or interrupted tasks.

Unlike linear chains, `loom` provides a robust, state-first architecture inspired by state machines, making it easy to build agents that can loop, branch, and resume exactly where they left off.

## Key Features

- **🕸️ Graph-based Workflows**: Define agents as directed graphs with nodes and conditional edges.
- **💾 State Persistence**: Built-in checkpointing with support for PostgreSQL and SQLite.
- **🖥️ Loom Studio**: Built-in Trace Explorer and Metrics Dashboard for real-time observability.
- **📺 Real-time Streaming**: First-class support for token-level streaming and event-based updates.
- **🧠 Advanced Memory Management**:
    - **Automatic Summarization**: Intelligently condense long conversations when token limits are reached.
    - **Precise Trimming**: Flexible message trimming strategies (e.g., sliding window) to fit context windows.
- **🔌 Provider Agnostic**: One interface for OpenAI, Anthropic, Ollama, and Google Gemini.
- **🔌 MCP Support**: Native integration with the Model Context Protocol for tools, resources, and prompts.
- **🛠️ Tool Integration**: Native support for tool calling and tool-use loops.
- **🛡️ Type Safe**: Built from the ground up with Go generics for maximum developer productivity and safety.

## Documentation

Detailed guides on how to use Loom's features:

- [🚀 Quick Start Guide](./docs/guides/quickstart.md): Build your first graph-based agent.
- [🕸️ Graph Workflows](./docs/guides/graphs.md): Parallel fan-out/fan-in, sub-graphs (`AsNode`/`MapNode`), retries, and step limits.
- [🖥️ Loom Studio](./docs/guides/observability.md): The built-in visualization and debugging dashboard.
- [💬 Conversations](./docs/guides/conversations.md): Message roles, multimodal content, and history.
- [🧠 LLM Package](./docs/guides/llm-package.md): Learn about the Model API and Registry.
- [⚖️ Decision Models](./docs/guides/decision.md): Fast, calibrated System One probabilistic evaluations and routing.
- [🧭 Embedding Models](./docs/guides/embedding.md): Dense text and multimodal vector embeddings.
- [💾 Persistent Store](./docs/guides/persistent-store.md): Namespaced key-value store with semantic vector indexing (`memorydb`, `sqlite`, `pg`).
- [📚 RAG (Documents, VectorStore & Retrievers)](./docs/guides/rag.md): Lazy document loaders, text splitters, vector stores, RRF fusion, and System One reranking.
- [🔌 Providers: OpenAI](./docs/guides/providers/openai.md), [Anthropic](./docs/guides/providers/anthropic.md), [Gemini](./docs/guides/providers/google.md), [Antigravity](./docs/guides/providers/antigravity.md), [Ollama](./docs/guides/providers/ollama.md), [TypeSafe AI](./docs/guides/providers/typesafe.md).
- [🔌 MCP Support](./docs/guides/mcp.md): Connect to MCP servers for tools, resources, and prompts.
- [💾 Persistence & State](./docs/guides/persistence.md): Learn about checkpointing and thread resumption.
- [🤝 Human-in-the-Loop](./docs/guides/hitl.md): Patterns for human approval and input.
- [🛠️ Tools & Streaming](./docs/guides/tools.md): Integrate tools and handle real-time events.
- [🧠 Memory & Context](./docs/guides/memory.md): Manage conversation history and token limits.
- [🔍 Observability](./docs/guides/observability.md): Deep-dive into metrics and tracing.
- [🏗️ Custom Providers](./docs/guides/custom-providers.md): Extend Loom with new LLM backends.

## Installation

```bash
go get github.com/masterkeysrd/loom
```

For development and documentation setup, see the [Installation Guide](./docs/installation.md).

## Quick Start

Create a simple chat agent that loops until a specific condition is met.

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/masterkeysrd/loom/graph"
    "github.com/masterkeysrd/loom/llm"
    "github.com/masterkeysrd/loom/providers/openai"
    "github.com/masterkeysrd/loom/message"
)

type MyState struct {
    Messages message.MessageList
}

func main() {
    ctx := context.Background()

    // 1. Initialize an LLM provider
    provider, _ := loomopenai.NewDefaultProvider()
    model := llm.NewModel(provider, "gpt-4o").
        WithTemperature(0.7).
        WithMaxTokens(1000)

    // 2. Build the workflow graph
    builder := graph.New[MyState]().
        WithName("chat-agent").
        AddNode("llm", func(ctx context.Context, s MyState) (graph.Command[MyState], error) {
            resp, err := model.Invoke(ctx, s.Messages)
            if err != nil {
                return nil, err
            }
            return graph.Update[MyState](func(s MyState) MyState {
                s.Messages = append(s.Messages, resp)
                return s
            }), nil
        })

    builder.AddEdge(graph.START, "llm")
    builder.AddEdge("llm", graph.END)

    g, _ := builder.Build()

    // 3. Execute the graph
    initialState := MyState{
        Messages: message.MessageList{
            message.NewUserText("What is the best way to weave a loom?"),
        },
    }

    // Pass a unique ThreadID for telemetry correlation
    snapshot, err := g.Execute(ctx, graph.Update[MyState](func(s MyState) MyState {
        return initialState
    }), &graph.Location{ThreadID: "first-weave"})

    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(snapshot.State.Messages.Last().GetContent().Text())
}
```

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
| **Mini REPL** | [`examples/mini_repl`](./examples/mini_repl) | Interactive terminal chat REPL with multi-turn conversation memory. |

## Supported Providers

| Provider | Package |
|---|---|
| OpenAI | `github.com/masterkeysrd/loom/providers/openai` |
| Anthropic | `github.com/masterkeysrd/loom/providers/anthropic` |
| Google Gemini | `github.com/masterkeysrd/loom/providers/genai` |
| Google Antigravity | `github.com/masterkeysrd/loom/providers/antigravity` |
| Ollama | `github.com/masterkeysrd/loom/providers/ollama` |
| TypeSafe AI | `github.com/masterkeysrd/loom/providers/typesafe` |

## License

MIT
