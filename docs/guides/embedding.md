# Embedding Models 🧭

The `embedding` package provides a unified abstraction for generating dense vector representations (`embedding.Vector`) from text and multimodal content across cloud and local providers.

Embedding models map inputs—such as sentences, document chunks, images, or audio—into a shared vector space where semantically related items sit close together. These vectors power semantic search, Retrieval-Augmented Generation (RAG), classification, clustering, and long-term agent memory.

---

## 1. Core Abstractions

- **`Provider`**: The backend implementation that executes embedding requests (`loomopenai`, `loomgenai`, `loomollama`).
- **`Model`**: The high-level wrapper around a `Provider` and model name. Provides single-query and batch-document helpers, dimension and task configuration, automatic token cost calculation, and middleware chains.
- **`Vector`**: A `[]float32` slice with built-in vector math methods (`CosineSimilarity`, `Dot`, `EuclideanDistance`, `Normalize`, `Float64`, `Dimension`).
- **`Input`**: A single item to embed. Supports plain text (`embedding.Text`, `embedding.Texts`) as well as multimodal [`message.Block`](conversations.md) parts (`embedding.Image`, `embedding.ImageURL`, `embedding.Multimodal`, `Input.WithParts`).
- **`TaskType`**: Optional hint informing task-aware models how vectors will be used (`TaskRetrievalQuery`, `TaskRetrievalDocument`, `TaskSemanticSimilarity`, `TaskClassification`, `TaskClustering`).

---

## 2. Supported Providers

### OpenAI Embeddings API
OpenAI provides hosted text embedding models (`text-embedding-3-small`, `text-embedding-3-large`, `text-embedding-ada-002`) with Matryoshka dimension reduction support.

```go
import (
    "github.com/masterkeysrd/loom/embedding"
    loomopenai "github.com/masterkeysrd/loom/providers/openai"
)

// Uses OPENAI_API_KEY from environment
provider, err := loomopenai.NewDefaultProvider()
if err != nil {
    log.Fatal(err)
}

model, err := embedding.NewModel(provider, "text-embedding-3-small", nil)
```

### Google Gemini Embeddings API
Google Gemini (`gemini-embedding-001`, `gemini-embedding-2`) supports task-aware embeddings (`TaskType`), configurable output dimensionality, and multimodal inputs (text, images, audio, video, and PDF documents).

```go
import (
    "context"
    "github.com/masterkeysrd/loom/embedding"
    loomgenai "github.com/masterkeysrd/loom/providers/genai"
)

// Uses GOOGLE_API_KEY from environment
provider, err := loomgenai.NewDefaultProvider(context.Background())
if err != nil {
    log.Fatal(err)
}

model, err := embedding.NewModel(provider, "gemini-embedding-001", nil)
```

### Ollama Local Embeddings API
Ollama runs open-weights embedding models locally (`nomic-embed-text`, `mxbai-embed-large`, `bge-m3`, `all-minilm`, `qwen3-embedding`) via `/api/embed`.

```go
import (
    "github.com/masterkeysrd/loom/embedding"
    loomollama "github.com/masterkeysrd/loom/providers/ollama"
)

// Connects to http://127.0.0.1:11434 by default
provider, err := loomollama.NewDefaultProvider()
if err != nil {
    log.Fatal(err)
}

model, err := embedding.NewModel(provider, "nomic-embed-text", nil)
```

---

## 3. Embedding Queries and Documents

In retrieval workflows, indexing documents and embedding search queries are the two primary operations. `Model` provides dedicated helpers that automatically apply `TaskRetrievalDocument` and `TaskRetrievalQuery`:

```go
ctx := context.Background()

// 1. Batch-embed documents
docs := []string{
    "Loom orchestrates stateful AI workflows using directed graphs.",
    "Checkpointers persist graph execution state to SQLite or PostgreSQL.",
    "Decision models evaluate fast System One predicates and choices.",
}
docVectors, err := model.EmbedDocuments(ctx, docs)
if err != nil {
    log.Fatal(err)
}

// 2. Embed a single search query
queryVector, err := model.EmbedQuery(ctx, "How is workflow state persisted?")
if err != nil {
    log.Fatal(err)
}

// 3. Score similarity using Vector methods
for i, docVec := range docVectors {
    score := queryVector.CosineSimilarity(docVec)
    fmt.Printf("[%.4f] %s\n", score, docs[i])
}
```

If you also need token usage and cost metrics, call `model.Embed` directly to receive the full `*embedding.Response`:

```go
resp, err := model.Embed(ctx, embedding.Texts("first chunk", "second chunk"))
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Generated %d vectors (tokens: %d, cost: %s)\n",
    len(resp.Embeddings),
    resp.Metrics.Tokens.Input,
    resp.Metrics.TotalCost,
)
```

---

## 4. Configuring Dimensions & Task Types

Many modern embedding models support truncating vectors to a smaller dimensionality (`Dimensions`) to reduce vector storage size and speed up similarity comparisons:

```go
// Configure defaults on a cloned model
model512 := model.
    WithDimensions(512).
    WithTaskType(embedding.TaskSemanticSimilarity)

// Or pass per-call options
vec, err := model.EmbedQuery(
    ctx,
    "semantic search query",
    embedding.WithDimensionsOption(256),
    embedding.WithTaskTypeOption(embedding.TaskRetrievalQuery),
)
```

---

## 5. Multimodal Embeddings

Models such as Google's `gemini-embedding-2` can embed images, audio, video, and documents directly into the same vector space as text:

```go
import (
    "github.com/masterkeysrd/loom/embedding"
    "github.com/masterkeysrd/loom/message"
)

// Embed a single inline image
imgVec, err := model.EmbedInput(ctx, embedding.Image(imageBytes, "image/png"))

// Embed text combined with multiple media blocks
combined := embedding.Text("Architecture diagram and voice note").WithParts(
    &message.ImageBlock{Data: imageBytes, MIMEType: "image/png"},
    &message.AudioBlock{Data: audioBytes, MIMEType: "audio/mp3"},
)
vec, err := model.EmbedInput(ctx, combined)
```

---

## 6. Vector Similarity & Math Operations

`embedding.Vector` (`[]float32`) includes built-in methods for comparing and transforming vectors without external dependencies:

| Method | Description |
|---|---|
| `v.Dimension() int` | Returns the number of elements in the vector (`len(v)`). |
| `v.CosineSimilarity(other Vector) float32` | Computes cosine similarity $\frac{v \cdot \text{other}}{\|v\| \|\text{other}\|}$. Returns `0` on dimension mismatch or zero norm. |
| `v.Dot(other Vector) float32` | Computes the inner dot product $\sum v_i \cdot \text{other}_i$. |
| `v.EuclideanDistance(other Vector) float32` | Computes the $L_2$ distance $\sqrt{\sum (v_i - \text{other}_i)^2}$. |
| `v.Normalize() Vector` | Returns a new $L_2$-normalized unit vector (`\|v\| = 1`). |
| `v.Float64() []float64` | Converts the `[]float32` vector into a `[]float64` slice. |

---

## 7. Middleware & Observability

Like `llm.Model` and `decision.Model`, `embedding.Model` attaches `TelemetryMiddleware` by default and supports custom `embedding.Middleware` chains:

```go
customModel := model.WithMiddleware(func(next embedding.Embedder) embedding.Embedder {
    return func(ctx context.Context, req *embedding.Request) (*embedding.Response, error) {
        log.Printf("embedding %d inputs with model %s", len(req.Inputs), req.Model)
        return next(ctx, req)
    }
})
```

- **OpenTelemetry Spans & Metrics**: Automatically records `embed {model}` client spans (`gen_ai.operation.name = "embeddings"`) and GenAI duration/token usage histograms.
- **Stream Writer Integration**: Emits `on_embedding` events when a `stream.Writer` is attached to `ctx`.
