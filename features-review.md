# Loom Feature Gap & Architecture Review Tracker

This living document tracks proposed features and architectural gaps across Loom so we can review them **one by one**, debate trade-offs against Loom's core philosophy (type safety, composability, minimal bloat, zero magic), and record a clear decision for each.

**Status Legend:**
- `⏳ Pending` — Not yet reviewed
- `✅ Accepted` — Approved for roadmap/implementation
- `🛠️ Scoped / Modified` — Approved with specific design constraints
- `⏸️ Deferred` — Valid idea, not a priority right now
- `❌ Rejected` — Out of scope or conflicts with Loom's design philosophy

---

## Summary Index

| ID | Area | Feature | Status |
|---|---|---|---|
| **F-01** | `graph` | Dynamic Map-Reduce (`MapSliceNode` / `BatchNode`) | 🛠️ Scoped / Modified |
| **F-02** | `graph` | Explicit Interrupt Metadata & Parallel Interrupt Handling | 🛠️ Scoped / Modified |
| **F-03** | `graph` | Dynamic & Compile-Time Breakpoints (`InterruptBefore` / `After`) | ✅ Accepted |
| **F-04** | `graph` | Command-Driven Routing (`Goto` / Handoffs) | ❌ Rejected |
| **F-05** | `graph` | Error Recovery (`RecoverMiddleware` & `CatchNode`) | 🛠️ Scoped / Modified |
| **F-06** | `checkpoint` | In-Memory Checkpointer (`checkpoint/memory`) | ✅ Accepted |
| **F-07** | `agent` | Flat `agent` Package (`New`, `NewSupervisor`, `NewDeep`, `NewToolNode`, `AsTool`) | 🛠️ Scoped / Modified |
| **F-08** | `eval` | `go test`-Native Evaluation + OTel `SpanTree` & Judges | ✅ Accepted |
| **F-09** | `llm` | Resilience Middlewares (Fallback, Rate Limit, Caching) | ❌ Rejected |
| **F-10** | `llm` / `agent` | Guardrails & Typed Output (Fold into `agent`; hold `Model` generics for Go 1.27) | 🛠️ Scoped / Modified |
| **F-11** | `providers/openai` | `WithName` / `NewCompatibleProvider` (No heavy Cloud SDK bloat) | 🛠️ Scoped / Modified |
| **F-12** | `retriever` / `graphstore` | GraphRAG (Metadata Traversal + Knowledge Graph Store) | ✅ Accepted |
| **F-13** | `retriever` | Parent-Document, Multi-Query, HyDE & Batched `DecisionReranker` | 🛠️ Scoped / Modified |
| **F-14** | `document` | Context-Aware `Splitter` + `GoASTSplitter`, `CodeSplitter` & `SemanticSplitter` | 🛠️ Scoped / Modified |
| **F-15** | `memory` / `store` | Long-Term Memory Tools (`search_memory` / `manage_memory`) & Extractor | ✅ Accepted |
| **F-16** | `vectorstore` | External Vector Store Connectors (Qdrant, Pinecone, etc.) | ⏸️ Deferred |
| **F-17** | `mcp` | MCP Server Adapter (Defer A2A; leave HTTP routes to Studio/apps) | 🛠️ Scoped / Modified |
| **F-18** | `tool` / `openapi` | OpenAPI 3.x Automatic Tool Generation | ⏸️ Deferred |
| **F-19** | `profile` / `providers` | Strict Modality & Capability Validation (Zero Silent Drops) | ✅ Accepted |

---

## 1. Graph Engine & HITL (`graph`, `checkpoint`)

### F-01: Dynamic Map-Reduce (`MapSliceNode` / `BatchNode`)
- **Status:** `🛠️ Scoped / Modified`
- **Current State:** [`graph/builder.go`](graph/builder.go) supports parallel fan-out/fan-in across *statically declared* nodes (`worker_a`, `worker_b`), and [`graph/subgraph.go`](graph/subgraph.go) provides `MapNode[P, C]` for adapting a single child `Node[C]` (or `subGraph.AsNode()`) to parent state `P`.
- **Proposal (Updated):** Instead of LangGraph-style dynamic `Send(node, anyPayload)` edges, provide a statically typed **`MapSliceNode[P, C]`** (or `BatchNode`) combinator in `graph`—matching CloudWeGo Eino (`BatchNode`) and Mastra (`.foreach()`):
  ```go
  func MapSliceNode[P State[P], C State[C]](
      worker Node[C],        // Any NodeFunc[C] or subGraph.AsNode()
      scatter func(P) []C,   // Extract dynamic N items from parent state
      gather func(P, []C) P, // Deterministically reduce ordered results into parent state
      opts ...BatchOption,   // e.g., WithConcurrency(n)
  ) Node[P]
  ```
- **Decision / Notes:**
  - **Reject** untyped edge-level `Send` (breaks `Graph[S]` compile-time type safety, static `Build()` topology validation, and `visualize.go`).
  - **Accept** node-level `MapSliceNode[P, C]` with concurrency bounds (`WithConcurrency`), indexed `Runtime.TaskID` scoping for nested subgraphs, and interrupt propagation (`InterruptUpdate`).

---

### F-02: Explicit Interrupt Metadata & Parallel Interrupt Handling
- **Status:** `🛠️ Scoped / Modified`
- **Current State:** [`graph.Interrupt()`](graph/command.go) and [`graph.InterruptUpdate`](graph/command.go) pause execution cleanly, keeping `Snapshot.Next` un-advanced. However, `CheckpointMetadata.Source` still records `"loop"` (making an interrupted checkpoint indistinguishable from a crashed mid-run checkpoint on `Load()`), `Snapshot` carries no per-node interrupt descriptor for UIs/Loom Studio, and parallel fan-out keeps *all* sibling nodes in `Next` even if only one sibling interrupted.
- **Proposal (Updated):** Keep Loom's exact step-boundary `Command` + checkpoint architecture (**zero SQL schema migrations** and **zero mid-function replay/counting magic**):
  1. Allow `Interrupt` / `InterruptUpdate` variants to optionally attach a JSON-serializable `Value` (`Interrupt{TaskID, Node, Value}`).
  2. When a step interrupts, set `CheckpointMetadata.Source = "interrupt"` and persist `Interrupts []Interrupt` inside `CheckpointMetadata` (hydrating `Snapshot.Interrupts` on `Execute` and `Load`).
  3. In parallel fan-out (`["worker_a", "worker_b"]`), advance/complete finished siblings so resuming only re-enters the interrupted node(s) rather than re-running completed siblings.
  4. For tool HITL, handle approval at the `Tools` node boundary keyed deterministically by `ToolCall.ID` (appending completed safe tool outputs to `State` before returning `InterruptUpdate` for pending tools).
- **Decision / Notes:**
  - **Reject** LangGraph-style mid-function `interrupt()` call-counting/replay magic.
  - **Accept** step-boundary `Snapshot.Interrupts` stored in `CheckpointMetadata` (`Source: "interrupt"`), keeping `Command[State]` resume semantics intact while unblocking Loom Studio HITL UI and parallel branch resume correctness.

---

### F-03: Dynamic & Compile-Time Breakpoints (`InterruptBefore` / `InterruptAfter`)
- **Status:** `✅ Accepted`
- **Current State:** Interrupts must be returned explicitly by node implementations (`graph.Interrupt()`), which requires dummy review nodes or guard flags if you want to pause *after* an expensive node (`"planner"`) without re-running it on resume.
- **Proposal (Updated):** Keep `Graph.Execute(ctx, input, loc)`'s signature untouched and support both compile-time (`Builder`) and per-run (`ctx`) breakpoints on top of F-02's `Interrupt` metadata:
  1. **Compile-time (`Builder`):** `WithInterruptBefore(nodes ...string)` and `WithInterruptAfter(nodes ...string)`—validated against registered `b.nodes` during `Build()`.
  2. **Per-run (`ctx`):** `graph.WithBreakpoints(ctx, graph.BreakBefore(...), graph.BreakAfter(...))` (including wildcard `"*"` for single-step debugging in tests and the redesigned Loom Studio).
  3. **Engine mechanics:**
     - `Before`: Pauses before running `Next`, recording `Interrupt{Node, Kind: "before"}` (`Source: "interrupt"`). On resume (`isFirstLoop`), consumes the `"before"` interrupt once so execution steps cleanly into the node without looping.
     - `After`: Runs the node, advances `Next` via `transition()`, records `Interrupt{Node, Kind: "after"}` (`Source: "interrupt"`), and pauses—so resuming enters downstream `Next` directly without re-executing the completed node.
- **Decision / Notes:**
  - **Accept** unified `Builder` + `ctx` breakpoints backed by F-02's `Interrupt` metadata.

---

### F-04: Command-Driven Routing (`Goto` / Handoffs)
- **Status:** `❌ Rejected`
- **Current State:** [`Command[State]`](graph/command.go) only mutates state (`Apply(State) State`) or interrupts; all control-flow transitions live strictly in [`Edge[State]`](graph/edge.go) definitions on `Builder`.
- **Proposal:** Allow a node (or a handoff tool) to return a command specifying both a state update and the next target node(s) (`Goto("billing_agent")`).
- **Decision / Notes:**
  - **Reject.** Keep strict separation between state mutation (`Node -> Command`) and control-flow topology (`Edge`), matching CloudWeGo Eino. This guarantees `Builder.Build()` validates 100% of possible transitions at compile/build time and keeps static visualization (`visualize.go` / Studio) completely honest without hidden imperative `Goto` jumps. Use `AddRouteEdge` / `AddConditionalEdge` for handoffs.

---

### F-05: Error Recovery (`RecoverMiddleware` & `CatchNode`)
- **Status:** `🛠️ Scoped / Modified`
- **Current State:** Node errors (after any `RetryMiddleware` attempts) abort `Graph.Execute` immediately and return the error.
- **Proposal (Updated):** Add `RecoverMiddleware[S]` and `CatchNode[S]` in `graph` to catch domain errors (never swallowing `context.Canceled` or `context.DeadlineExceeded`, with optional `WithShouldCatch(func(error) bool)` filtering) and translate them into explicit `State` updates (`onErr func(S, error) S`). Downstream recovery then uses normal strict `Edge`s (`AddConditionalEdge` / `AddRouteEdge`).
- **Decision / Notes:**
  - **Accept** `RecoverMiddleware[S]` + `CatchNode[S]`.
  - **Reject** separate engine-level `AddErrorEdge` tables—keeps `Graph.Execute` and `Edge` topology pure and unambiguous during parallel fan-out.

---

### F-06: In-Memory Checkpointer (`checkpoint/memory`)
- **Status:** `✅ Accepted`
- **Current State:** [`checkpoint`](checkpoint/README.md) provides `sqlite`, `pg`, and `NoopCheckpointer`. (`NoopCheckpointer` cannot resume interrupts or time-travel.)
- **Proposal (Updated):** Add a lightweight, thread-safe `checkpoint/memory` implementation (mirroring `store/memorydb` and `vectorstore/memory`) supporting full `ChannelWrites` (`WriteOpSet` / `WriteOpAppend`) lineage reconstruction and optional `WithMaxThreads` / TTL eviction.
- **Decision / Notes:**
  - **Accept** `checkpoint/memory` for zero-SQL unit tests, quickstarts, and ephemeral CLI/HITL workflows.

---

## 2. Agent Ergonomics & Harnesses (`agent`)

### F-07: Flat `agent` Package (`New`, `NewSupervisor`, `NewDeep`, `NewToolNode`, `AsTool`)
- **Status:** `🛠️ Scoped / Modified`
- **Current State:** Users wire `State`, `Model.Stream`/`Invoke`, sequential `tool.Container` execution, and conditional tool-call edges manually in every graph (e.g., [`examples/graph_agent/main.go`](examples/graph_agent/main.go)). Meanwhile, Loom already has core building blocks needed for long-horizon agents (`memory.Summarizer`, `tool/search.go`, `DeferredToolsMiddleware`, `store.Store`, and hierarchical `CheckpointNS`).
- **Proposal (Updated):** Provide a single flat **`agent`** package (`github.com/masterkeysrd/loom/agent`, matching Loom's flat package convention in `retriever` and `document`) keeping core `graph` unopinionated:
  1. **Composable Primitives (`agent.NewToolNode`, `agent.AsTool`):**
     - Parallel **`NewToolNode`** (`WithConcurrency`, `tool.Runtime` bridging, automatic Go `error` $\to$ `*message.Tool{IsError: true}` conversion, and F-02 HITL tool-approval hooks).
     - **`AsTool` (`TaskTool`):** Wrap any `Agent` or `*graph.Graph` as a context-isolated `*tool.Tool` with nested `CheckpointNS` and unified OTel trace parenting.
  2. **Agent Harnesses (`agent.New`, `agent.NewSupervisor`, `agent.NewDeep`):**
     - **`agent.New`:** Clean ReAct / tool-calling agent compiled onto a standard `*graph.Graph` (`AgentOption`), supporting optional input/output guardrails (e.g., via `decision.Model`) and structured output validation retries inside the agent loop.
     - **`agent.NewSupervisor`:** Centralized multi-agent coordinator routing across specialized sub-agents via LLM or fast System One `decision.Model` (`SupervisorOption`).
     - **`agent.NewDeep` (DeepAgent):** Long-horizon harness combining Planning (`write_todos`), explicit compile-time-typed **`WithFilesystem(fs Filesystem)`** (file tools only, e.g., virtual `StoreFilesystem` or `os.Root` `LocalFilesystem` + large-tool-output offloading—never exposing `exec`) vs. **`WithSandbox(sb Sandbox)`** (explicitly opting into isolated `Exec` + filesystem at compile time, with zero hidden `.(Sandbox)` runtime assertions), Context-Isolated Sub-Agents (`AsTool`), `DeferredToolsMiddleware`, and `memory.Summarizer` (`DeepOption`).
- **Decision / Notes:**
  - **Accept** flat `agent` package (`agent.go`, `supervisor.go`, `deep.go`, `tool_node.go`, `fs.go`, `sandbox.go`) with strictly separate `WithFilesystem` and `WithSandbox` options (no `WithBackend` runtime interface sniffing); reserve subpackages strictly for optional external container/sandbox SDK drivers (e.g., Docker/E2B).

---

## 3. Evaluation & Regression Testing (`eval`)

### F-08: `go test`-Native Evaluation + OTel `SpanTree` & Judges (`eval`)
- **Status:** `✅ Accepted`
- **Current State:** Runtime OTel traces (`telemetry`) + `profile` cost estimation + `decision.Model` exist, **but** there is no programmatic eval harness for CI/regression testing.
- **Proposal (Updated):** Build a lean, code-first `eval` package (`github.com/masterkeysrd/loom/eval`, inspired by `pydantic-evals`'s `SpanTree` model) integrated directly with `testing.T` and Loom's existing OpenTelemetry instrumentation:
  - Typed `Case[In, Expected]` & `Dataset[In, Expected]` (in-code or JSONL) executed concurrently (`WithConcurrency`).
  - Per-case isolated OTel **`SpanTree`** capture so evaluators can inspect internal execution trajectories (nodes visited, tool calls + args, retrieved RAG `Document`s, latency, tokens, and USD cost) without polluting user return types.
  - Pluggable `Evaluator` interface supporting deterministic Go checks, fast calibrated [`decision.Model`](decision/model.go) scorers, and structured `llm.Model` judges.
- **Decision / Notes:**
  - **Accept** `go test`-native + OTel `SpanTree` `eval` package (zero external SaaS dependency; naturally compatible with OTel-based Studio).

---

## 4. Model Resilience, Guardrails & Providers (`llm`, `providers`)

### F-09: LLM Resilience Middlewares (Fallbacks, Rate Limiting, Caching)
- **Status:** `❌ Rejected`
- **Current State:** [`llm.Middleware`](llm/model.go) wraps `Streamer`; provider SDKs already handle transient HTTP `429`/`5xx` retries, and `graph.RetryMiddleware` + `CatchNode` (F-05) handle graph-level retries and explicit fallback routing.
- **Proposal:** Built-in cross-model `Fallback`, in-process token-bucket rate limiter, and exact response cache.
- **Decision / Notes:**
  - **Reject.** Cross-provider model fallback is brittle in Loom because conversation histories carry provider-specific artifacts (signed `ThinkingBlock`s, `encrypted_content`, and stateful session state in `claudecode` / `copilot`) and incompatible `ModelConfig` / `Extensions`. Single-process rate limiters and response caches belong in an external AI Gateway (LiteLLM / Envoy / Cloudflare) or trivial 10-line user middlewares, while deliberate model failover should be modeled explicitly in the graph via F-05 (`CatchNode` + strict `Edge`).

---

### F-10: Input/Output Guardrails & Typed Structured Output
- **Status:** `🛠️ Scoped / Modified`
- **Current State:** [`decision`](decision/model.go) provides calibrated classification, and [`llm.Model.WithStructuredOutput`](llm/model.go) passes schemas to providers; raw `graph` users can already wire nodes, `decision` edges, and middlewares directly.
- **Decision / Notes:**
  - **Hold on `llm.Model` generic methods until Go 1.27:** Avoid adding awkward top-level `llm.InvokeTyped[T](ctx, model, ...)` functions in Go 1.26 just to deprecate them once Go 1.27 generic methods (`m.Invoke[T](...)`) land.
  - **No guardrail/retry bloat in `llm`:** Raw `graph` / `llm` users can trivially express this as a graph step, `decision` edge, or middleware.
  - **Fold into `agent` (`F-07`):** Input/output guardrails and structured-output validation loops belong as options inside the higher-level `agent` package where turnkey agent loops benefit from them.

---

### F-11: OpenAI-Compatible Endpoints & Cloud Provider Coverage
- **Status:** `🛠️ Scoped / Modified`
- **Current State:** [`providers/openai`](providers/openai/provider.go) already accepts custom `*openai.Client` (`option.WithBaseURL`) and `WithAPIMode(APIModeChatCompletions)`, `providers/genai` already covers Vertex AI (`BackendVertexAI`), and `openai-go` covers Azure—except `loomopenai.Provider.Name()` is hardcoded to `"openai"`, causing collisions in `llm.Registry` and OTel attributes when registering `OpenRouter`/`vLLM`/`LiteLLM` alongside OpenAI.
- **Proposal (Updated):** Add `WithName(name)` and a convenience `NewCompatibleProvider(name, baseURL, apiKey, opts...)` helper directly in `providers/openai` (defaulting to `APIModeChatCompletions` without triggering OpenAI-specific `/v1/responses` routing).
- **Decision / Notes:**
  - **Accept** ~20-line `WithName` / `NewCompatibleProvider` addition in `providers/openai`.
  - **Reject / Defer** adding heavy cloud SDKs (such as `aws-sdk-go-v2` for Bedrock) to root `go.mod`.

---

## 5. RAG, Loaders & Memory (`retriever`, `document`, `memory`)

### F-12: GraphRAG (`retriever.GraphRetriever` + `graphstore`)
- **Status:** `✅ Accepted` *(Detailed specification in [`graph.md`](graph.md))*
- **Current State:** Dense vector + Okapi BM25 + RRF + `DecisionReranker`.
- **Proposal (Updated):** Two-tier rollout per [`graph.md`](graph.md): (1) Metadata-edge `GraphRetriever` over `VectorStore` (`TraversalEager` + `TraversalMMR`), and (2) `graphstore` (`memory`, `sqlite` FTS5/`JSON1`/`loom_vec_cosine`/CTE, `pg` `pg_trgm`/`pgvector`/PG14+ `SEARCH`/`CYCLE` CTE) + schema-guided `Extractor` + `KnowledgeGraphRetriever`.
- **Decision / Notes:**
  - **Accept** phased GraphRAG design as specified in [`graph.md`](graph.md).

---

### F-13: Parent-Document, Multi-Query, HyDE & Batched `DecisionReranker`
- **Status:** `🛠️ Scoped / Modified`
- **Current State:** `VectorStoreRetriever`, `BM25Retriever`, `MergerRetriever`, and sequential per-doc `DecisionReranker`; `document.EnrichChunkMetadata` already populates `Metadata["parent_id"]`.
- **Proposal (Updated):**
  1. **`ParentDocumentRetriever`:** Query fine-grained child chunks in a base `Retriever`, deduplicate `Metadata["parent_id"]` preserving max child score, and hydrate full parent `Document`s from `store.Store`.
  2. **`MultiQueryRetriever` & `HyDERetriever`:** Opt-in query-transformation retrievers wrapping a base `Retriever` + `*llm.Model` (parallel multi-query expansion fused via RRF, and Hypothetical Document Embeddings).
  3. **Batched `DecisionReranker`:** Upgrade [`DecisionReranker`](retriever/retrievers.go) to evaluate multiple candidate documents in a single `decision.Model.Evaluate(ctx, input, questions...)` request (with configurable batch size) instead of $N$ sequential `Check` round-trips.
- **Decision / Notes:**
  - **Accept** `ParentDocumentRetriever`, `MultiQueryRetriever`, `HyDERetriever`, and batched `DecisionReranker`.
  - **Reject** third-party cross-encoder reranker SDKs (Cohere/Jina/Voyage)—batched `decision.Model.Evaluate` covers fast calibrated reranking natively.

---

### F-14: Context-Aware `Splitter` + `GoASTSplitter`, `CodeSplitter` & `SemanticSplitter`
- **Status:** `🛠️ Scoped / Modified`
- **Current State:** `document.Splitter` is synchronous/infallible (`SplitDocuments(docs) []Document`), unlike `Loader` (`Load(ctx) iter.Seq2[Document, error]`); built-in splitters are `RecursiveCharacterSplitter` and `MarkdownSplitter`.
- **Proposal (Updated):**
  1. **Upgrade `document.Splitter` interface (clean pre-1.0 breaking change):** Update `SplitText(ctx, text) ([]string, error)`, `SplitDocuments(ctx, docs) ([]Document, error)`, and `SplitIter(ctx, seq, splitter)` so model-backed and AST splitters satisfy the exact same `Splitter` contract with `ctx` cancellation and error handling.
  2. **`GoASTSplitter` (`go/ast`):** Stdlib AST chunker for Go source files, splitting cleanly by top-level declarations (`func`, `type`, `const`, `var`) and stamping `Metadata["symbol"]`, `Metadata["kind"]`, and `Metadata["imports"]`.
  3. **`CodeSplitter`:** Language-aware separator presets (Python, TS/JS, Rust, Java, C++, SQL) built on `RecursiveCharacterSplitter` without CGO `tree-sitter`.
  4. **`SemanticSplitter`:** Embedding-driven breakpoint splitter (`*embedding.Model`) implementing the updated `document.Splitter` interface.
- **Decision / Notes:**
  - **Accept** `document.Splitter` `(ctx, ...) (..., error)` signature update + `GoASTSplitter`, `CodeSplitter`, and `SemanticSplitter` (and also added `Get` directly onto `vectorstore.VectorStore` in [`graph.md`](graph.md)).
  - **Reject** bundling heavy/brittle pure-Go PDF/Office binary parsers in core `go.mod`.

---

### F-15: Cross-Thread Long-Term Memory Tools & Fact Extractor (`memory` + `store`)
- **Status:** `✅ Accepted`
- **Current State:** [`memory.Summarizer`](memory/summarizer.go) compacts sliding conversation windows; [`store`](store/store.go) provides persistent KV + semantic vector search (`memorydb`, `sqlite`, `pg`) with `WithIndexFields` and `WithKeyPrefix`.
- **Proposal (Updated):**
  1. **`memory.Item` Schema:** `{ID, Content, Category, Tags, Metadata, CreatedAt, UpdatedAt}`, stored under `<category>:<uuidv7>` keys with `store.WithIndexFields("content", "tags")` so embeddings remain free of UUID/timestamp noise.
  2. **Caller-Controlled Namespace Isolation:** Never expose `namespace` to the LLM; resolve via static string or `WithNamespaceFunc(func(ctx context.Context) (string, error))` using `store.FromContext(ctx)` (or an explicit `store.Store`).
  3. **Two-Tool Pattern (`memory.NewTools` & `memory.NewTypedTools[T]`):**
     - `search_memory` (`IsReadOnly: true`, `IsIdempotent: true`): Semantic `store.WithQuery` + `WithKeyPrefix` search, with automatic [`internal/bm25`](internal/bm25/bm25.go) fallback when `store.ErrIndexNotConfigured` (works even without an embedding model!).
     - `manage_memory` (`action: "create" | "update" | "delete"`): Single unified mutation tool encouraging fact updates over duplicate appends; plus generic `NewTypedTools[T]` for custom domain schemas.
  4. **Explicit `memory.Extractor`:** Opt-in callable helper (`Reconcile(ctx, ns, messages)`) for structured post-turn fact extraction/reconciliation without hidden background goroutines.
- **Decision / Notes:**
  - **Accept** `memory.Item` + `search_memory`/`manage_memory` tools (`NewTools` / `NewTypedTools[T]`) with `NamespaceFunc` isolation, BM25 fallback, and explicit `Extractor`.

---

### F-16: External Vector Store Connectors (Qdrant, Pinecone, Weaviate, Milvus)
- **Status:** `⏸️ Deferred` *(Listed on [`ROADMAP.md`](ROADMAP.md))*
- **Current State:** `vectorstore/memory`, `vectorstore/sqlite`, `vectorstore/pg` (`pgvector` HNSW).
- **Decision / Notes:**
  - **Defer.** `vectorstore/pg` (`pgvector` HNSW) + `sqlite` + `memory` cover the vast majority of Go production workloads without pulling third-party vector DB SDKs into `go.mod`. Revisit later (via stdlib `net/http` REST or separate contrib modules) only if concrete demand arises.

---

## 6. Serving, Protocols & Tool Generation (`mcp`, `openapi`, `server`)

### F-17: Exposing Loom Tools & Agents as an MCP Server (`mcp`)
- **Status:** `🛠️ Scoped / Modified`
- **Current State:** [`mcp`](mcp/mcp.go) is an MCP *client* already built on `github.com/modelcontextprotocol/go-sdk/mcp` and `github.com/google/jsonschema-go/jsonschema`.
- **Proposal (Updated):** Add `mcp.NewServer` inside `mcp` to expose any `tool.Container` (including Loom agents/graphs wrapped via `agent.AsTool`) over MCP Stdio and Streamable HTTP (`http.Handler`) with zero new dependencies.
- **Decision / Notes:**
  - **Accept** MCP Server adapter in `mcp`.
  - **Defer** A2A protocol until concrete ecosystem demand emerges.
  - **Reject** generic opinionated HTTP/REST graph server framework—leave custom HTTP routes to user `http.Handler`s and the upcoming Loom Studio redesign.

---

### F-18: OpenAPI 3.x Automatic Tool Generation
- **Status:** `⏸️ Deferred` *(Listed on [`ROADMAP.md`](ROADMAP.md))*
- **Current State:** Tools are defined in Go via `tool.New` or discovered dynamically from MCP servers.
- **Decision / Notes:**
  - **Defer.** MCP (`mcp` client + upcoming `mcp.NewServer` in F-17) covers dynamic external tool ecosystems much more cleanly than maintaining complex `$ref` / `oneOf` / auth edge cases across arbitrary OpenAPI specs.

---

### F-19: Strict Modality & Capability Validation Across Providers
- **Status:** `✅ Accepted` *(In Progress / Implemented)*
- **Current State:** Several providers (`openai`, `anthropic`, CLI providers, etc.) previously dropped unsupported media blocks (`AudioBlock`, `VideoBlock`, URL-only sources, or `System`/`Tool` media) silently or substituted placeholder text (`"[Image attached]"`), risking silent context loss.
- **Proposal (Updated):**
  1. **Shared Errors & Profile Helpers ([`profile/validation.go`](profile/validation.go)):** `ErrUnsupportedModality` (`*UnsupportedModalityError`), `ErrUnsupportedCapability` (`*UnsupportedCapabilityError`), `ValidateMessagesModalities`, `ValidateContentModalities`, and `ValidateToolCallCapability`.
  2. **Profile Gate (When Known):** When `GetProfile(model)` resolves a profile with declared modalities/capabilities, fail fast before network I/O; when unknown (`ok == false`), pass through to wire validation.
  3. **Wire Gate (Always Strict):** Eliminate silent drops on `ImageBlock`, `AudioBlock`, `VideoBlock`, and `DocumentBlock` across `System`, `User`, `Assistant`, `Tool`, `embedding.Input`, and `decision.Input`, while keeping historical `ThinkingBlock` replay lenient.
- **Decision / Notes:**
  - **Accept** and enforce across all providers (`openai`, `anthropic`, `genai`, `copilot`, `claudecode`, `antigravity`, `typesafe`, matching `ollama`).

---

## Approved Implementation Waves

With all 18 items reviewed (`5 ✅ Accepted`, `9 🛠️ Scoped / Modified`, `2 ⏸️ Deferred`, `2 ❌ Rejected`), the approved work groups cleanly into **4 sequential waves**:

### Wave 1: Core `graph`, `checkpoint` & Provider Foundations
1. **`F-06` (`checkpoint/memory`):** Thread-safe in-memory checkpointer with full `ChannelWrites` reconstruction + optional retention bounds.
2. **`F-02` (`graph` Interrupt Metadata & Parallel Resume):** `Snapshot.Interrupts` stored in `CheckpointMetadata` (`Source: "interrupt"`), optional JSON reason/value on `Interrupt`/`InterruptUpdate`, and parallel fan-out fix so completed siblings don't re-run on resume.
3. **`F-03` (`graph` Breakpoints):** Compile-time `WithInterruptBefore`/`WithInterruptAfter` (validated in `Build()`) + per-run `WithBreakpoints(ctx, ...)` (including `"*"` single-step).
4. **`F-01` & `F-05` (`graph` Combinators):** `MapSliceNode[P, C]` (`WithConcurrency`), `RecoverMiddleware[S]`, and `CatchNode[S]`.
5. **`F-11` (`providers/openai`):** `WithName` and `NewCompatibleProvider` for `OpenRouter` / `vLLM` / `LiteLLM`.

### Wave 2: Memory, `agent` Package & MCP Server
1. **`F-15` (`memory` + `store`):** `memory.Item` schema, `search_memory` + `manage_memory` tools (`NewTools` / `NewTypedTools[T]`) with `NamespaceFunc` isolation and BM25 fallback, plus explicit `memory.Extractor`.
2. **`F-07` & `F-10` (`agent`):** Flat `agent` package (`NewToolNode`, `AsTool`, `New` with guardrails/output validation, `NewSupervisor`, and `NewDeep` with strictly separate `WithFilesystem` and `WithSandbox`).
3. **`F-17` (`mcp`):** `mcp.NewServer` exposing `tool.Container` / agents over Stdio and Streamable HTTP.

### Wave 3: RAG, Splitters & GraphRAG
1. **`F-14` (`document`):** Breaking pre-1.0 `Splitter` update `(ctx, ...) (..., error)` + `GoASTSplitter`, `CodeSplitter`, and `SemanticSplitter`.
2. **`F-13` (`retriever`):** Batched `DecisionReranker` (`decision.Model.Evaluate`), `ParentDocumentRetriever`, `MultiQueryRetriever`, and `HyDERetriever`.
3. **`F-12` (`retriever` + `graphstore`):** Phase 1 Metadata `GraphRetriever` + `VectorStore.Get` $\to$ Phase 2 `graphstore/{memory,sqlite,pg}` + `Extractor` + `KnowledgeGraphRetriever` per [`graph.md`](graph.md).

### Wave 4: Evaluation & Regression Suite
1. **`F-08` (`eval`):** `go test`-native `Dataset[In, Expected]` runner with per-case OTel `SpanTree` capture and deterministic / `decision.Model` / `llm.Model` evaluators.
