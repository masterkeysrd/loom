# Loom v0.0.1 - Initial Release 🧵

We are excited to announce the first official release of `loom`, a high-performance, graph-based AI workflow engine for Go. Loom is designed to provide the most robust, type-safe, and persistent foundation for building production-grade AI agents and multi-step LLM workflows.

## Key Features

### 🕸️ Graph-Based Orchestration
*   Define complex agentic logic as directed graphs with nodes and edges.
*   Support for **Conditional Edges** and **Dynamic Routing** for flexible execution paths.
*   State-first architecture where a shared, typed state is transformed at every step.

### 💾 Persistent State & Checkpointing
*   Built-in support for **PostgreSQL** and **SQLite** checkpointers.
*   Automatic state persistence after every node execution.
*   **Human-In-The-Loop (HITL)**: Use `graph.Interrupt()` to pause execution for approval or input and resume perfectly from the saved state.

### 🖼️ Full Multimodal Support
*   First-class support for **Images, Audio, Video, and Documents** across all providers.
*   Consistent, provider-agnostic representation for media blocks (inline data or URLs).

### 🛡️ Structured Output Enforcement
*   Strict **JSON Schema** enforcement for LLM responses.
*   Native mapping to OpenAI Structured Outputs, Gemini response schemas, and Ollama constrained decoding.

### 🔌 Multi-Provider Support
*   Unified interface for **OpenAI**, **Anthropic**, **Google Gemini**, and **Ollama**.
*   A thread-safe `Registry` for managing multiple provider instances dynamically.

### 🧠 Advanced Memory Management
*   **Automated Summarization**: Condense long histories based on token-count triggers.
*   **Precise Trimming**: sliding-window and role-based message trimming to fit context limits.

### 📺 Real-time Observability & Streaming
*   Unified streaming of LLM tokens and custom node-level events.
*   Detailed **Token Metrics** and **Execution Timing** (Prompt, Completion, Total) for performance analysis.

## Getting Started

```bash
go get github.com/masterkeysrd/loom
```

Check out the [README.md](README.md) for a quick start guide and the [loom-developer.md](loom-developer.md) for expert guidance on building with the framework.

---
*Loom: Weaving durable AI workflows in Go.*
