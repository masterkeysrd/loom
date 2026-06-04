# Loom Roadmap 🧵

This document outlines the planned evolution of the `loom` framework. Our goal is to provide the most robust, type-safe, and high-performance graph-based AI engine for the Go ecosystem.

## 🟢 Core Engine & Orchestration
- [ ] **Parallel Node Execution**: Support for executing independent nodes concurrently (fan-out/fan-in) within a single step.
- [ ] **Sub-graphs**: Enable nesting graphs within nodes to promote modularity and reuse of complex logic.
- [ ] **Execution Safety**: Implement configurable bounds for graph execution, such as `MaxIterations` and `ExecutionTimeout`, to prevent runaway loops.
- [ ] **Middleware/Interceptors**: Add support for global hooks that run before or after node execution for cross-cutting concerns like logging, auth, and validation.

## 🤝 Human-in-the-Loop (HITL)
- [ ] **Dynamic Breakpoints**: Allow users to interrupt execution before or after specific nodes without hardcoding logic into the nodes themselves.
- [ ] **State Forking (Time Travel)**: APIs to clone an existing thread/checkpoint and resume from a modified state, enabling "what-if" scenarios and easier debugging.
- [ ] **Specialized Input Nodes**: Formalized patterns for nodes that pause execution specifically to wait for external human input.

## 🧠 LLM & Memory Enhancements
- [ ] **Semantic Memory**: Integration with Vector Databases (e.g., Pinecone, Weaviate, Milvus) directly into the `memory` package for RAG-based long-term context.
- [ ] **Structured Output Enforcement**: Native support for schema-constrained LLM responses (e.g., OpenAI's `response_format` or json-schema constraints).
- [ ] **Cost & Usage Tracking**: Centralized aggregation of token usage and estimated costs across entire execution threads.

## 🔌 Ecosystem & Integration
- [ ] **OpenAPI & MCP Support**: Automatic tool generation from OpenAPI specs and native support for the Model Context Protocol (MCP).
- [ ] **OpenTelemetry (OTel) Integration**: First-class support for OTel spans and traces to integrate with modern observability stacks (Jaeger, Honeycomb, etc.).

## 🛠️ Developer Experience (DX)
- [ ] **Loom Studio (Live Visualization)**: A local development UI to visualize graph execution in real-time, inspect state changes, and debug transitions.
- [ ] **Automated Retries**: Configurable retry strategies at the node level for handling transient LLM errors or rate limits.
- [ ] **Enhanced Error Recovery**: Better patterns for "error nodes" that can gracefully handle and recover from failures in other parts of the graph.

---
*Note: This roadmap is a living document and will be updated based on community feedback and project needs.*
