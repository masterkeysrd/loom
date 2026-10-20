# retriever

The `retriever` package provides a composable **Retriever** abstraction for fetching relevant [`document.Document`](../document/README.md) items given a natural-language query, complete with OpenTelemetry tracing, real-time stream events (`on_retrieve`), Reciprocal Rank Fusion (RRF), System One decision reranking, and agent tool integration.

---

## Core Abstractions

| Type / Function | Description |
|---|---|
| `Retriever` | Interface (`Retrieve(ctx, query, opts...) ([]document.Document, error)`). |
| `RetrieverFunc` | Function adapter implementing `Retriever`. |
| `Middleware` / `Chain` | Composable middleware wrapper around any `Retriever`. |
| `TelemetryMiddleware` | Records OpenTelemetry client spans (`retrieve {name}`) and emits `"on_retrieve"` events to `stream.Writer`. |
| `FromVectorStore` | Adapts any [`vectorstore.VectorStore`](../vectorstore/README.md) into a `Retriever`. |
| `NewMerger` | Combines multiple retrievers using **Reciprocal Rank Fusion (RRF)**. |
| `NewDecisionReranker` | Uses a [`decision.Model`](../decision/README.md) (System One) to score, filter, and rerank candidate documents. |
| `NewTool` | Wraps any `Retriever` as a read-only, idempotent [`*tool.Tool`](../tool/README.md) for LLM agents. |

---

## Built-in Retrievers

### 1. Vector Store Retriever (`FromVectorStore`)
Wraps a `vectorstore.VectorStore` with default options (`WithTopK`, `WithNamespace`, `WithMinScore`, `WithFilter`) and automatic telemetry:

```go
import (
    "github.com/masterkeysrd/loom/retriever"
)

ret := retriever.FromVectorStore(vs,
    retriever.WithTopK(5),
    retriever.WithMinScore(0.6),
)

docs, err := ret.Retrieve(ctx, "How does checkpointing work?")
```

### 2. Hybrid / Ensemble Merger (`NewMerger` — Reciprocal Rank Fusion)
Combines rankings from multiple retrievers (e.g., dense vector search + keyword search, or multiple namespaces/stores) using Reciprocal Rank Fusion ($\sum \frac{1}{k + \text{rank}_i + 1}$):

```go
hybrid := retriever.NewMerger(
    []retriever.Retriever{vectorRetriever, keywordRetriever},
    retriever.WithRRFConstant(60),
)

docs, err := hybrid.Retrieve(ctx, "OAuth PKCE token refresh", retriever.WithTopK(5))
```

### 3. System One Decision Reranker (`NewDecisionReranker`)
Uses Loom's [`decision.Model`](../decision/README.md) to evaluate each candidate document against a relevance criterion, filtering out noisy chunks below a probability threshold and sorting by calibrated relevance probability:

```go
import (
    "github.com/masterkeysrd/loom/decision"
    "github.com/masterkeysrd/loom/retriever"
    loomtypesafe "github.com/masterkeysrd/loom/providers/typesafe"
)

tsProvider, _ := loomtypesafe.NewDefaultProvider()
decModel, _ := decision.NewModel(tsProvider, loomtypesafe.ModelJevLatest, nil)

reranked := retriever.NewDecisionReranker(
    ret,
    decModel,
    "Does this document directly answer or provide facts needed for the query?",
    retriever.WithThreshold(0.65),
)

docs, err := reranked.Retrieve(ctx, "How do I configure HNSW indexing in Postgres?", retriever.WithTopK(3))
// Each returned doc has doc.Score set to the decision probability and
// doc.Metadata["decision_confidence"] populated.
```

### 4. Exposing a Retriever as an Agent Tool (`NewTool`)
Turn any `Retriever` into a `*tool.Tool` that an LLM agent can invoke during graph execution:

```go
searchTool, err := retriever.NewTool(
    "search_docs",
    "Search the technical documentation for relevant guides and API references",
    reranked,
    retriever.WithTopK(4),
)

container := tool.NewContainer(searchTool)
model = model.BindToolDefs(container.Definitions()...)
```
