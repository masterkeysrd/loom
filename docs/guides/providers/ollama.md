# Ollama Provider 🔌

The Ollama provider allows you to run open-source models (like Llama 3, Mistral, Qwen) locally on your machine.

## 1. Setup

Ollama must be installed and running on your system. You can download it from [ollama.com](https://ollama.com).

By default, Loom connects to the standard Ollama API at `http://localhost:11434`.

## 2. Basic Usage

```go
import (
    "github.com/masterkeysrd/loom/llm"
    "github.com/masterkeysrd/loom/providers/ollama"
)

provider, err := loomollama.NewDefaultProvider()
if err != nil {
    // handle error
}

// Make sure you have pulled the model locally: ollama pull llama3.1
model := llm.NewModel(provider, "llama3.1")
```

## 3. Local Model Catalog

Unlike cloud providers, Ollama's available models depend on what you have installed locally. You can register your local models with Loom to ensure context window metadata is available:

```go
provider.OverrideProfile("my-local-llama", profile.Profile{
    ID: "my-local-llama",
    DisplayName: "Local Llama 3",
    Limits: profile.Limits{
        Context: 8192,
    },
})
```

## 4. Decision Models (System One) ⚖️

`loomollama.Provider` natively implements the [`decision.Provider`](../decision.md) interface, communicating directly with Ollama's `/v1/systemone` endpoint. This enables fast, fully local probabilistic evaluation using System One decision models such as `nimble`, `clef`, or `clef-flash`.

You can pass the Ollama provider directly to `decision.NewModel`:

```go
import (
    "context"
    "fmt"
    "log"

    "github.com/masterkeysrd/loom/decision"
    "github.com/masterkeysrd/loom/providers/ollama"
)

// 1. Create Ollama provider (connects to http://localhost:11434 by default)
provider, err := loomollama.NewDefaultProvider()
if err != nil {
    log.Fatal(err)
}

// 2. Instantiate decision model (e.g., nimble, clef, or clef-flash)
decisionModel, err := decision.NewModel(provider, "nimble", nil)
if err != nil {
    log.Fatal(err)
}

// 3. Fast local decision check
input := decision.Text("Customer says: 'Can you provide the API documentation for webhook signatures?'")
ans, err := decisionModel.Check(context.Background(), input, "Is this request asking for technical documentation?")
if err != nil {
    log.Fatal(err)
}

if ans.Passed(0.75) {
    fmt.Printf("Technical inquiry confirmed (probability: %.2f, confidence: %.2f)\n", ans.Probability, ans.Confidence)
}
```

Calls to Ollama SystemOne endpoints automatically record OpenTelemetry client spans decorated with `ollama.api.type = "systemone"`.

## 5. Embedding Models 🧭

`loomollama.Provider` natively implements the [`embedding.Provider`](../embedding.md) interface via Ollama's `/api/embed` endpoint, allowing you to generate embeddings locally with models like `nomic-embed-text`, `mxbai-embed-large`, or `bge-m3`:

```go
import (
    "context"
    "fmt"
    "log"

    "github.com/masterkeysrd/loom/embedding"
    loomollama "github.com/masterkeysrd/loom/providers/ollama"
)

provider, err := loomollama.NewDefaultProvider()
if err != nil {
    log.Fatal(err)
}

// Make sure you have pulled the embedding model locally: ollama pull nomic-embed-text
embedModel, err := embedding.NewModel(provider, "nomic-embed-text", nil)
if err != nil {
    log.Fatal(err)
}

vecs, err := embedModel.EmbedDocuments(context.Background(), []string{
    "First local document chunk",
    "Second local document chunk",
})
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Embedded %d documents (dim=%d)\n", len(vecs), vecs[0].Dimension())
```

Calls to Ollama embedding endpoints automatically record OpenTelemetry client spans decorated with `ollama.api.type = "embed"`.
