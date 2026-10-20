# embedding

The `embedding` package provides a unified **Embedding Model** abstraction for generating dense vector representations from text and multimodal inputs in Go.

Embeddings convert text, images, audio, video, or documents into fixed-length `[]float32` vectors (`embedding.Vector`) that capture semantic meaning, powering similarity search, Retrieval-Augmented Generation (RAG), clustering, classification, and semantic memory.

---

## Core Abstractions

| Type | Description |
|---|---|
| `Provider` | Interface for concrete embedding backends (OpenAI, Google Gemini, Ollama). |
| `Model` | High-level wrapper around a `Provider` and model identifier with shortcuts (`EmbedQuery`, `EmbedDocuments`, `EmbedInput`), batching (`Embed`), token/cost calculation, and middleware. |
| `Vector` | Named `[]float32` slice with built-in math helpers (`CosineSimilarity`, `Dot`, `EuclideanDistance`, `Normalize`, `Float64`, `Dimension`). |
| `Input` | Single embedding item supporting plain text (`Text`, `Texts`) and multimodal `message.Block` parts (`Image`, `ImageURL`, `Multimodal`, `WithParts`). |
| `TaskType` | Optional semantic hint (`TaskRetrievalQuery`, `TaskRetrievalDocument`, `TaskSemanticSimilarity`, `TaskClassification`, `TaskClustering`). |
| `Response` | Batch result containing `[]Vector` embeddings and `*message.TokenMetrics` (input tokens and computed cost). |

---

## Supported Providers

### 1. OpenAI Embeddings API
- **Models**: `text-embedding-3-small`, `text-embedding-3-large`, `text-embedding-ada-002`
- **Provider**: `loomopenai.Provider` (`github.com/masterkeysrd/loom/providers/openai`) implements `embedding.Provider` and `profile.Provider`.
- **Features**: Batch text inputs, Matryoshka dimension reduction (`WithDimensions`), automatic `float64` $\to$ `float32` conversion, and OpenTelemetry span decoration (`openai.api.type = "embeddings"`).

```go
import (
    "github.com/masterkeysrd/loom/embedding"
    loomopenai "github.com/masterkeysrd/loom/providers/openai"
)

provider, err := loomopenai.NewDefaultProvider()
if err != nil {
    log.Fatal(err)
}

model, err := embedding.NewModel(provider, "text-embedding-3-small", nil)
```

### 2. Google Gemini Embeddings API
- **Models**: `gemini-embedding-001`, `gemini-embedding-2`
- **Provider**: `loomgenai.Provider` (`github.com/masterkeysrd/loom/providers/genai`) implements `embedding.Provider` and `profile.Provider`.
- **Features**: Full **multimodal** support (text, images, audio, video, PDFs), native `TaskType` optimization (`RETRIEVAL_QUERY`, `RETRIEVAL_DOCUMENT`, etc.), and configurable `OutputDimensionality` (`WithDimensions`).

```go
import (
    "context"
    "github.com/masterkeysrd/loom/embedding"
    loomgenai "github.com/masterkeysrd/loom/providers/genai"
)

provider, err := loomgenai.NewDefaultProvider(context.Background())
if err != nil {
    log.Fatal(err)
}

model, err := embedding.NewModel(provider, "gemini-embedding-001", nil)
```

### 3. Ollama Local Embeddings API
- **Endpoint**: `/api/embed`
- **Models**: `nomic-embed-text`, `mxbai-embed-large`, `bge-m3`, `all-minilm`, `qwen3-embedding`
- **Provider**: `loomollama.Provider` (`github.com/masterkeysrd/loom/providers/ollama`) implements `embedding.Provider` and `profile.Provider`.
- **Features**: Local execution, batch text inputs, dimension truncation, and OpenTelemetry span decoration (`ollama.api.type = "embed"`).

```go
import (
    "github.com/masterkeysrd/loom/embedding"
    loomollama "github.com/masterkeysrd/loom/providers/ollama"
)

provider, err := loomollama.NewDefaultProvider()
if err != nil {
    log.Fatal(err)
}

model, err := embedding.NewModel(provider, "nomic-embed-text", nil)
```

---

## Code Examples

### Query & Document Embeddings
`EmbedQuery` and `EmbedDocuments` automatically set `TaskRetrievalQuery` and `TaskRetrievalDocument` respectively (unless overridden on the model or call):

```go
ctx := context.Background()

// 1. Embed a batch of documents to index
docs := []string{
    "Loom is a graph-based AI workflow engine for Go.",
    "Decision models provide calibrated System One evaluations.",
}
docVectors, err := model.EmbedDocuments(ctx, docs)
if err != nil {
    log.Fatal(err)
}

// 2. Embed a search query
queryVec, err := model.EmbedQuery(ctx, "What is Loom?")
if err != nil {
    log.Fatal(err)
}

// 3. Compare vectors using built-in similarity helpers
for i, docVec := range docVectors {
    sim := queryVec.CosineSimilarity(docVec)
    fmt.Printf("Doc %d cosine similarity: %.4f\n", i, sim)
}
```

### Controlling Vector Dimensions
Models trained with Matryoshka Representation Learning (such as `text-embedding-3-*`, `gemini-embedding-*`, and `qwen3-embedding`) can truncate output vectors to smaller dimensions:

```go
// Configure at the model level
compactModel := model.WithDimensions(512)

// Or override for a single call
vec, err := model.EmbedQuery(ctx, "search query", embedding.WithDimensionsOption(256))
```

### Multimodal Embeddings
For multimodal embedding models like `gemini-embedding-2`, construct inputs using `embedding.Image`, `embedding.ImageURL`, `embedding.Multimodal`, or `Input.WithParts`:

```go
import (
    "github.com/masterkeysrd/loom/embedding"
    "github.com/masterkeysrd/loom/message"
)

// Single image input
imgVec, err := model.EmbedInput(ctx, embedding.Image(pngBytes, "image/png"))

// Combined text + image + audio in a single multimodal embedding
multiInput := embedding.Text("Product overview").WithParts(
    &message.ImageBlock{Data: pngBytes, MIMEType: "image/png"},
    &message.AudioBlock{Data: mp3Bytes, MIMEType: "audio/mp3"},
)
resp, err := model.Embed(ctx, []embedding.Input{multiInput})
```

### Vector Math Helpers
`embedding.Vector` (`[]float32`) includes built-in similarity and normalization operations:

```go
sim := v1.CosineSimilarity(v2)      // Cosine similarity in [-1, 1]
dot := v1.Dot(v2)                   // Dot (inner) product
dist := v1.EuclideanDistance(v2)    // L2 distance
norm := v1.Normalize()              // L2-normalized unit vector copy
f64 := v1.Float64()                 // Convert []float32 to []float64
dims := v1.Dimension()              // len(v1)
mmrIdx := embedding.MMR(queryVec, candidateVecs, 0.5, 5) // Maximal Marginal Relevance indices
```

---

## Observability & Stream Events

1. **OpenTelemetry**: `TelemetryMiddleware` is attached by default when calling `embedding.NewModel`. Every call records a client span named `embed {model}` with `gen_ai.operation.name = "embeddings"`, `gen_ai.provider.name`, `gen_ai.request.model`, `gen_ai.response.model`, and `gen_ai.usage.input_tokens`, plus `gen_ai.client.operation.duration` and `gen_ai.client.token.usage` histograms.
2. **Token Cost Estimation**: If the provider implements `profile.Provider`, `Model.Embed` automatically computes `resp.Metrics.Cost` and `resp.Metrics.TotalCost` from `resp.Metrics.Tokens`.
3. **Stream Events**: When a `stream.Writer` is present in `ctx`, `Model.Embed` emits an `on_embedding` event with source `embedding:{model}`.
