# Loom 🧵

`loom` is a high-performance, graph-based AI workflow engine for Go. It allows you to build complex agentic systems by defining multi-step workflows as directed graphs, seamlessly connecting them to any large language model, and persisting execution state for long-running or interrupted tasks.

Unlike linear chains, `loom` provides a robust, state-first architecture inspired by state machines, making it easy to build agents that can loop, branch, and resume exactly where they left off.

## Key Features

- **🕸️ Graph-based Workflows**: Define agents as directed graphs with nodes and conditional edges.
- **💾 State Persistence**: Built-in checkpointing with support for PostgreSQL and SQLite.
- **📺 Real-time Streaming**: First-class support for token-level streaming and event-based updates.
- **🧠 Advanced Memory Management**: Intelligently condense long conversations and trim message history.
- **🔌 Provider Agnostic**: One interface for OpenAI, Anthropic, Ollama, and Google Gemini.
- **🔌 MCP Support**: Native integration with the Model Context Protocol for tools, resources, and prompts.
- **🛠️ Tool Integration**: Native support for tool calling and tool-use loops.
- **🛡️ Type Safe**: Built from the ground up with Go generics for maximum developer productivity.

## Why Loom?

Loom is designed for developers who need more control than what simple chains or "black-box" agent frameworks offer. By treating your AI workflow as a state machine, Loom gives you:

1. **Explicit Control**: You define exactly how the state transitions between nodes.
2. **Durability**: Every step is checkpointed, so your agents can survive crashes or wait for human input.
3. **Observability**: Built-in tools to visualize your graphs and trace every token and tool call.

[Get Started with the Quick Start Guide →](./guides/quickstart.md)
