# loom 🧵

`loom` is a high-performance, graph-based AI workflow engine for Go. It allows you to build complex agentic systems by defining multi-step workflows as directed graphs, seamlessly connecting them to any large language model, and persisting execution state for long-running or interrupted tasks.

Unlike linear chains, `loom` provides a robust, state-first architecture inspired by state machines, making it easy to build agents that can loop, branch, and resume exactly where they left off.

## Key Features

- **🕸️ Graph-based Workflows**: Define agents as directed graphs with nodes and conditional edges.
- **💾 State Persistence**: Built-in checkpointing with support for PostgreSQL and SQLite.
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
- [💬 Conversations](./docs/guides/conversations.md): Message roles, multimodal content, and history.
- [🧠 LLM Package](./docs/guides/llm-package.md): Learn about the Model API and Registry.
- [🔌 Providers: OpenAI](./docs/guides/providers/openai.md), [Anthropic](./docs/guides/providers/anthropic.md), [Gemini](./docs/guides/providers/google.md), [Ollama](./docs/guides/providers/ollama.md).
- [🔌 MCP Support](./docs/guides/mcp.md): Connect to MCP servers for tools, resources, and prompts.
- [💾 Persistence & State](./docs/guides/persistence.md): Learn about checkpointing and thread resumption.
- [🤝 Human-in-the-Loop](./docs/guides/hitl.md): Patterns for human approval and input.
- [🛠️ Tools & Streaming](./docs/guides/tools.md): Integrate tools and handle real-time events.
- [🧠 Memory & Context](./docs/guides/memory.md): Manage conversation history and token limits.
- [🔍 Observability](./docs/guides/observability.md): Graph visualization and tracing.
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
    "github.com/masterkeysrd/loom/llm/openai"
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

    snapshot, err := g.Execute(ctx, graph.Update[MyState](func(s MyState) MyState {
        return initialState
    }), nil)

    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(snapshot.State.Messages.Last().GetContent().Text())
}
```

## Core Concepts

### The Graph

A `Graph[State]` is a network of **nodes** connected by **edges**.
- **Nodes**: Units of work that receive the current state and return a `Command`.
- **Edges**: Paths between nodes. They can be direct or **conditional**.
- **Commands**: Instructions to the engine on how to update state or control flow (e.g., `Update`, `Interrupt`).

### Checkpointing & Resumption

Loom saves a "checkpoint" of your state after every node execution. This allows you to:
- Resume interrupted workflows.
- Implement "human-in-the-loop" by interrupting a graph and waiting for external input.
- Audit the entire execution history of an agent.

```go
// Using SQLite for checkpointing
db, _ := sql.Open("sqlite3", "loom.db")
cp, _ := sqlite.NewCheckpointer(db)

g, _ := graph.New[MyState]().
    WithCheckpointer(cp).
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

## Supported Providers

| Provider | Package |
|---|---|
| OpenAI | `github.com/masterkeysrd/loom/llm/openai` |
| Anthropic | `github.com/masterkeysrd/loom/llm/anthropic` |
| Google Gemini | `github.com/masterkeysrd/loom/llm/genai` |
| Ollama | `github.com/masterkeysrd/loom/llm/ollama` |

## License

MIT
