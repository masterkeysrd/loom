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
| `FromVectorStore` | Adapts any [`vectorstore.VectorStore`](../vectorstore/README.md) into a `Retriever` (supporting `WithMMR`, `WithFilter`, `WithMinScore`, `WithNamespace`). |
| `NewBM25` | In-memory **Okapi BM25** lexical keyword retriever (`WithBM25K1`, `WithBM25B`, `WithBM25Tokenizer`, dynamic `Add`). |
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
    retriever.WithMMR(0.5, 20), // Optional: balance relevance (0.5) and diversity over top 20 candidates
)

docs, err := ret.Retrieve(ctx, "How does checkpointing work?")
```

### 2. BM25 Lexical Retriever (`NewBM25`) & Hybrid Search (`NewMerger` — RRF)
`BM25Retriever` implements the Okapi BM25 keyword ranking algorithm in pure Go and pairs naturally with `VectorStoreRetriever` inside `NewMerger` for hybrid (lexical + semantic) search:

```go
// 1. Keyword BM25 retriever
bm25 := retriever.NewBM25(chunks,
    retriever.WithBM25K1(1.5),
    retriever.WithBM25B(0.75),
)

// 2. Fuse BM25 + VectorStore using Reciprocal Rank Fusion
hybrid := retriever.NewMerger(
    []retriever.Retriever{ret, bm25},
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
