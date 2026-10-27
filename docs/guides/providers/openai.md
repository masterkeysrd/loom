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
    "github.com/masterkeysrd/loom/providers/openai"
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

## 5. Responses API ⚡

OpenAI's **Responses API** (`/v1/responses`) provides agentic capabilities including built-in tools (web search, file search), server-side state continuation, and encrypted reasoning persistence.

### Hybrid Routing

You can enable the Responses API either globally or per-request:

```go
// 1. Explicit opt-in at provider level:
provider, err := loomopenai.NewDefaultProvider(
    loomopenai.WithResponsesAPI(true), // or loomopenai.WithAPIMode(loomopenai.APIModeResponses)
)

// 2. Or automatic feature-triggered routing:
// Loom automatically routes to /v1/responses whenever Responses features
// (such as WebSearch, FileSearch, or conversation continuation) are used:
model = model.WithExtension(loomopenai.WithWebSearch())
```

### State Management & Message Pruning (`use_previous_response_id`)

When continuing conversational threads with `store: true` (default), Loom can automatically extract the response ID from the latest assistant message metadata (`openai.response_id`), set `previous_response_id`, and drop previous messages from the request payload to minimize token transmission:

```go
model = model.WithExtension(loomopenai.ResponsesOptions{
    Enabled:               true,
    UsePreviousResponseID: true,
})

// Turn 1
history := []message.Message{
    message.NewUserText("Hi, I'm Bob."),
}
resp1, _ := model.Generate(ctx, history)
// resp1 has metadata["openai.response_id"] = "resp_abc123"
history = append(history, resp1)

// Turn 2
history = append(history, message.NewUserText("What is my name?"))
// Loom automatically prunes Turn 1 and sets previous_response_id="resp_abc123"
resp2, _ := model.Generate(ctx, history)
```

### Dual Storage Modes & Encrypted Content (`encrypted_content`)

The Responses API supports both stateful server storage and Zero Data Retention (ZDR):

* **`Store: true` (Default)**: OpenAI retains conversation trees server-side, enabling message pruning via `previous_response_id`.
* **`Store: false` (Zero Data Retention)**: OpenAI retains no conversation history. Loom sends the complete conversation history client-side while preserving internal reasoning tokens via OpenAI's `encrypted_content` in `ThinkingBlock.Extras[openai.encrypted_content]`.

```go
storeFalse := false
model = model.WithExtension(loomopenai.ResponsesOptions{
    Store: &storeFalse, // Enforce Zero Data Retention compliance
})
```

## 6. Model Catalog

The provider includes a static catalog of common OpenAI models with their context limits and capabilities. You can search or list them at runtime:

```go
profiles := provider.ListProfiles()
for _, p := range profiles {
    fmt.Printf("Model: %s, Context: %d\n", p.ID, p.Limits.Context)
}
```

## 7. Decision Models (System One) ⚖️

`loomopenai.Provider` natively implements the [`decision.Provider`](../decision.md) interface, communicating directly with OpenAI's `/v1/decisions` endpoint. This allows you to evaluate fast, calibrated probabilistic questions using decision models such as `gpt-6-luna`.

You can pass the OpenAI provider directly to `decision.NewModel`:

```go
import (
    "context"
    "fmt"
    "log"

    "github.com/masterkeysrd/loom/decision"
    "github.com/masterkeysrd/loom/providers/openai"
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

## 8. Embedding Models 🧭

`loomopenai.Provider` also implements the [`embedding.Provider`](../embedding.md) interface for generating dense text embeddings using models like `text-embedding-3-small` and `text-embedding-3-large`:

```go
import (
    "context"
    "fmt"
    "log"

    "github.com/masterkeysrd/loom/embedding"
    loomopenai "github.com/masterkeysrd/loom/providers/openai"
)

provider, err := loomopenai.NewDefaultProvider()
if err != nil {
    log.Fatal(err)
}

embedModel, err := embedding.NewModel(provider, "text-embedding-3-small", nil)
if err != nil {
    log.Fatal(err)
}

// Optionally reduce vector dimensions (e.g., 512)
embedModel = embedModel.WithDimensions(512)

vec, err := embedModel.EmbedQuery(context.Background(), "How does Loom handle state persistence?")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Vector dimensions: %d\n", vec.Dimension())
```

Calls to OpenAI embeddings automatically record OpenTelemetry client spans decorated with `openai.api.type = "embeddings"`.
