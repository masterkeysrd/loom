# Loom Roadmap 🧵

This document outlines the planned evolution of the `loom` framework. Our goal is to provide the most robust, type-safe, and high-performance graph-based AI engine for the Go ecosystem.

## 🟢 Core Engine & Orchestration
- [x] **Parallel Node Execution**: Support for executing independent nodes concurrently (fan-out/fan-in) within a single step.
- [x] **Sub-graphs**: Enable nesting graphs within nodes (`AsNode` / `MapNode`) to promote modularity and reuse of complex logic.
- [x] **Execution Safety**: Implement configurable bounds for graph execution (`WithMaxSteps` and context timeout/cancellation checks) to prevent runaway loops.
- [x] **Middleware/Interceptors**: Add support for global hooks that run before or after node execution for cross-cutting concerns like logging, auth, and validation.
- [x] **Compile-Time Topology Validation**: Validate node names, dead-ends, unreachable targets, and edge predicates at `Build()` time.

## 🤝 Human-in-the-Loop (HITL)
- [ ] **Dynamic Breakpoints**: Allow users to interrupt execution before or after specific nodes without hardcoding logic into the nodes themselves.
- [x] **State Forking (Time Travel)**: Resume from any historical checkpoint ID with modified state (`Interrupt` / `InterruptUpdate`), enabling "what-if" scenarios and debugging.
- [ ] **Specialized Input Nodes**: Formalized patterns for nodes that pause execution specifically to wait for external human input.

## 🧠 LLM, Decision & Memory Enhancements
- [x] **Semantic Memory & RAG**: Native `embedding`, `document`, `vectorstore`, `retriever` (BM25, MMR, RRF, System One reranking), and semantic `store` backends (`memorydb`, `sqlite`, `pgvector`).
- [x] **Probabilistic Decision Models (System One)**: Fast, calibrated probabilistic classification and routing (`decision` package).
- [x] **Structured Output Enforcement**: Native support for schema-constrained LLM responses (`WithJSON` and `WithStructuredOutput` via `jsonschema.Schema`).
- [x] **Cost & Usage Tracking**: Model profile catalog (`profile`), tiered token cost estimation, and thread-level telemetry aggregation.
- [ ] **External Vector Store Connectors**: Additional vector store integrations (e.g., Qdrant, Pinecone, Weaviate, Milvus).

## 🔌 Ecosystem & Integration
- [x] **Model Context Protocol (MCP) Support**: Native MCP client supporting tools, resources, prompts, OAuth, Stdio, SSE, and Streamable HTTP.
- [ ] **OpenAPI Tool Generation**: Automatic tool generation from OpenAPI specifications.
- [x] **OpenTelemetry (OTel) Integration**: First-class support for OTel GenAI spans, traces, and metrics across graphs, models, and tools.

## 🛠️ Developer Experience (DX)
- [x] **Loom Studio (Live Visualization)**: Local development control plane (`loom studio`) with Dashboard, Trace Explorer, Metrics Explorer, and Playground.
- [x] **Automated Retries**: Configurable exponential backoff retry strategies (`graph.RetryMiddleware`) with isolated state copies per attempt.
- [ ] **Enhanced Error Recovery**: Better patterns for "error nodes" that can gracefully handle and recover from failures in other parts of the graph.

---
*Note: This roadmap is a living document and will be updated based on community feedback and project needs.*
