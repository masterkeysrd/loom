# OpenAI Provider 🔌

The OpenAI provider allows you to use all standard GPT models, including reasoning models like `o1` and `o3-mini`, as well as standard models like `gpt-4o`.

## 1. Installation

Ensure you have the Loom package installed:

```bash
go get github.com/masterkeysrd/loom
```

## 2. Setup & Authentication

The OpenAI provider uses the `OPENAI_API_KEY` environment variable for authentication by default.

```bash
export OPENAI_API_KEY='your-api-key-here'
```

## 3. Basic Usage

```go
import (
    "github.com/masterkeysrd/loom/llm"
    "github.com/masterkeysrd/loom/llm/openai"
)

// Create the provider
provider, err := loomopenai.NewDefaultProvider()
if err != nil {
    // handle error
}

// Instantiate a model
model := llm.NewModel(provider, "gpt-4o")
```

## 4. Advanced Features

### Reasoning Models (Thinking)

For models like `o1` or `o3-mini` that support internal reasoning, you can configure the thinking effort.

```go
model = model.WithThinkingEffort("medium") // low, medium, high
```

### Structured Output

OpenAI's native JSON Schema enforcement is supported.

```go
model = model.WithStructuredOutput(myJsonSchema)
```

### Prompt Caching

OpenAI automatically caches prompt prefixes longer than 1024 tokens. Loom allows you to optimize this behavior using the `PromptCache` extension:

#### 1. Cache Bucketing (Key)
Use a stable identifier to improve cache hit rates by bucketing similar requests together.

```go
model = model.WithExtension(loomopenai.PromptCache{
    Key: "user-session-123",
})
```

#### 2. Extended Retention
By default, caches are ephemeral. You can enable extended caching (up to 24 hours) for specific prefixes.

```go
model = model.WithExtension(loomopenai.PromptCache{
    Retention: "24h",
})
```

## 5. Model Catalog

The provider includes a static catalog of common OpenAI models with their context limits and capabilities. You can search or list them at runtime:

```go
profiles := provider.ListProfiles()
for _, p := range profiles {
    fmt.Printf("Model: %s, Context: %d\n", p.ID, p.Limits.Context)
}
```

## 6. Decision Models (System One) ⚖️

`loomopenai.Provider` natively implements the [`decision.Provider`](../decision.md) interface, communicating directly with OpenAI's `/v1/decisions` endpoint. This allows you to evaluate fast, calibrated probabilistic questions using decision models such as `gpt-6-luna`.

You can pass the OpenAI provider directly to `decision.NewModel`:

```go
import (
    "context"
    "fmt"
    "log"

    "github.com/masterkeysrd/loom/decision"
    "github.com/masterkeysrd/loom/llm/openai"
)

// 1. Create OpenAI provider
provider, err := loomopenai.NewDefaultProvider()
if err != nil {
    log.Fatal(err)
}

// 2. Instantiate decision model (e.g., gpt-6-luna)
decisionModel, err := decision.NewModel(provider, "gpt-6-luna", nil)
if err != nil {
    log.Fatal(err)
}

// 3. Fast probabilistic predicate check
input := decision.Text("Order #1294: Expedited overnight delivery to an unverified address with a new credit card.")
ans, err := decisionModel.Check(context.Background(), input, "Is this order high-risk fraud?")
if err != nil {
    log.Fatal(err)
}

if ans.Passed(0.80) {
    fmt.Printf("Flagged for fraud review (probability: %.2f, confidence: %.2f)\n", ans.Probability, ans.Confidence)
}
```

Calls to OpenAI decisions automatically record OpenTelemetry client spans decorated with `openai.api.type = "decisions"`.

