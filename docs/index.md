# Loom 🧵

`loom` is a high-performance, graph-based AI workflow engine for Go. It allows you to build complex agentic systems by defining multi-step workflows as directed graphs, seamlessly connecting them to any large language model, and persisting execution state for long-running or interrupted tasks.

Unlike linear chains, `loom` provides a robust, state-first architecture inspired by state machines, making it easy to build agents that can loop, branch, and resume exactly where they left off.

## Key Features

- **🕸️ Graph-based Workflows**: Define agents as directed graphs with nodes, conditional branching, and route edges.
- **🔀 Parallel Execution (Fan-out / Fan-in)**: Concurrently run multiple downstream branches with isolated state copies and deterministic command resolution.
- **🧩 Composable Sub-graphs**: Nest graphs as first-class nodes using `AsNode` and state adapters with `MapNode`, complete with hierarchical LangGraph-style checkpoint namespaces.
- **🛡️ Execution Safety & Middleware**: Guard against runaway cycles with `WithMaxSteps` / `ErrMaxStepsExceeded` and inject resilience with `RetryMiddleware` and custom node middleware.
- **💾 State Persistence**: Built-in checkpointing with support for PostgreSQL and SQLite.
- **📺 Real-time Streaming**: First-class support for token-level streaming and event-based updates.
- **🧠 Advanced Memory Management**: Intelligently condense long conversations and trim message history.
- **⚖️ Decision & Embedding Models**: First-class System One probabilistic decision models (`decision`) and multimodal vector embeddings (`embedding`).
- **📚 RAG & Semantic Memory**: Built-in document loaders and splitters (`document`), vector stores (`vectorstore`), composable retrievers (`retriever`), and semantic KV stores (`store`).
- **🔌 Provider Agnostic**: One interface for OpenAI, Anthropic, Ollama, Google Gemini, and TypeSafe AI.
- **🔌 MCP Support**: Native integration with the Model Context Protocol for tools, resources, and prompts.
- **🛠️ Tool Integration**: Native support for tool calling and tool-use loops.
- **🛡️ Type Safe**: Built from the ground up with Go generics for maximum developer productivity.

## Documentation Overview

| Guide | Description |
|---|---|
| [🚀 Quick Start](./guides/quickstart.md) | Build your first directed agent graph in minutes. |
| [🕸️ Graph Workflows](./guides/graphs.md) | Deep dive into graph state, commands, parallel fan-out/fan-in, sub-graphs (`AsNode`/`MapNode`), execution safety (`WithMaxSteps`), and middleware (`RetryMiddleware`). |
| [💬 Conversations](./guides/conversations.md) | Manage multimodal messages and conversation history. |
| [🧠 LLM Package](./guides/llm-package.md) | Work with the provider-agnostic Model API and Registry. |
| [⚖️ Decision Models](./guides/decision.md) | Fast System One probabilistic classifiers and routers. |
| [🧭 Embedding Models](./guides/embedding.md) | Dense text and multimodal embeddings. |
| [💾 Persistent Store](./guides/persistent-store.md) | Namespaced key-value storage with semantic vector search. |
| [📚 RAG Pipelines](./guides/rag.md) | Document loaders, splitters, vector stores, and rankers. |
| [💾 Persistence & State](./guides/persistence.md) | Durable checkpoints with SQLite and PostgreSQL. |
| [🤝 Human-in-the-Loop](./guides/hitl.md) | Interrupt patterns, state updates, and review workflows. |
| [🛠️ Tools & Streaming](./guides/tools.md) | Register tools and consume token-level event streams. |
| [🔍 Observability](./guides/observability.md) | Visualize workflows and inspect execution metrics in Loom Studio. |

## Why Loom?

Loom is designed for developers who need more control than what simple chains or "black-box" agent frameworks offer. By treating your AI workflow as a state machine, Loom gives you:

1. **Explicit Control**: You define exactly how the state transitions between nodes.
2. **Durability**: Every step is checkpointed, so your agents can survive crashes or wait for human input.
3. **Observability**: Built-in tools to visualize your graphs and trace every token and tool call.

[Get Started with the Quick Start Guide →](./guides/quickstart.md)
