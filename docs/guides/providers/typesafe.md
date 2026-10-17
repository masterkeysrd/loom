# TypeSafe AI Provider ⚖️

The TypeSafe AI provider (`loomtypesafe`) offers native, high-performance integration with TypeSafe AI's dedicated **System One / Decision Model** API. 

Unlike general-purpose generative LLM providers, TypeSafe AI is purpose-built specifically for ultra-fast, calibrated probabilistic decisions, binary predicates, categorical classification, and ordinal scoring.

---

## 1. Overview

TypeSafe AI provides dedicated System One endpoints:
- **Evaluation**: `POST https://api.typesafe.ai/v1/systemone`
- **Model Discovery**: `GET https://api.typesafe.ai/v1/models`

`loomtypesafe.Provider` natively implements both [`decision.Provider`](../decision.md) and `decision.ProfileProvider`, allowing it to be plugged directly into `decision.NewModel` without any adapter.

### Key Capabilities
- **Ultra-low latency & cost**: Purpose-built decision inference at **$0.042 per 1M input tokens** ($0 output tokens).
- **Automatic payload compression**: Payloads $\ge$ 1 KiB are automatically gzip-compressed by default.
- **Resilient retries**: Built-in exponential backoff on HTTP `429 Too Many Requests` and `529 Overloaded`.
- **Dynamic model catalog**: Models and profiles are queryable via `provider.ListModels(ctx)`.
- **Extended context extension**: Context limits can be scaled from 64k up to 262k tokens via `loomtypesafe.LongContext`.

---

## 2. Setup & Authentication

The TypeSafe AI provider uses the `TYPESAFE_API_KEY` environment variable for authentication:

```bash
export TYPESAFE_API_KEY='your-typesafe-api-key'
# Optional: override default API endpoint (https://api.typesafe.ai)
export TYPESAFE_BASE_URL='https://api.typesafe.ai'
```

---

## 3. Initialization & Configuration

### Default Provider
Reads `TYPESAFE_API_KEY` and optional `TYPESAFE_BASE_URL` from the environment:

```go
import (
    "github.com/masterkeysrd/loom/decision"
    loomtypesafe "github.com/masterkeysrd/loom/decision/typesafe"
)

provider, err := loomtypesafe.NewDefaultProvider()
if err != nil {
    log.Fatal(err)
}
```

### Custom Provider Options
Use functional options to configure the client:

```go
provider, err := loomtypesafe.NewProvider(
    loomtypesafe.WithAPIKey("ts-custom-key"),
    loomtypesafe.WithBaseURL("https://api.typesafe.ai"),
    loomtypesafe.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}),
    loomtypesafe.WithCompression(true), // Enabled by default for payloads >= 1 KiB
    loomtypesafe.WithMaxRetries(5),      // Exponential backoff for 429/529 (default: 3)
)
```

Available options:
- `WithAPIKey(string)`: Sets the API key.
- `WithBaseURL(string)`: Overrides the base URL (defaults to `https://api.typesafe.ai`).
- `WithHTTPClient(*http.Client)`: Injects a custom HTTP client.
- `WithCompression(bool)`: Controls automatic gzip compression for requests $\ge$ 1 KiB (enabled by default).
- `WithMaxRetries(int)`: Sets the maximum number of retries on `429` (Too Many Requests) or `529` (Overloaded) responses (default: 3).

---

## 4. Models & Pricing

TypeSafe AI models specialize in deterministic probability calibration:

| Model Constant | Model ID | Description | Context Limit | Input Price / 1M |
|---|---|---|---|---|
| `loomtypesafe.ModelJevLatest` | `"jev-latest"` | Recommended stable alias (points to `jev-1.13.0`) | 64k (262k with extension) | $0.042 |
| `loomtypesafe.ModelJevPreview` | `"jev-preview"` | Preview release alias (points to `jev-1.14.0`) | 64k (262k with extension) | $0.042 |
| `loomtypesafe.ModelJev1_14_0` | `"jev-1.14.0"` | Pin to specific version 1.14.0 | 64k (262k with extension) | $0.042 |
| `loomtypesafe.ModelJev1_13_0` | `"jev-1.13.0"` | Pin to specific version 1.13.0 | 64k (262k with extension) | $0.042 |

### Dynamic Model Discovery
You can query the TypeSafe AI catalog at runtime via `ListModels`:

```go
models, err := provider.ListModels(ctx)
if err != nil {
    log.Fatal(err)
}

for _, m := range models {
    fmt.Printf("Model: %-15s Released: %s (%s)\n", m.Name, m.ReleaseDate, m.Description)
}
```

---

## 5. Supported Question Types

TypeSafe AI natively supports all three core decision question types:

1. **Binary Predicate (`decision.Predicate`)**:
   - Maps to TypeSafe AI's `"noul"` question type.
   - Supports explicit criteria via `.WithCriteria(trueDesc, falseDesc)`.
2. **Categorical Choice (`decision.Choice`)**:
   - Maps to TypeSafe AI's `"choice"` question type.
   - Supports up to **255 discrete options**.
3. **Ordinal Score (`decision.Score`)**:
   - Maps to TypeSafe AI's `"score"` question type.
   - Evaluates across **2 to 10 rubric levels**.

> [!WARNING]
> **Modality Support**: TypeSafe AI is optimized for pure text and structured JSON state (`decision.Text` and `decision.State`). It does **not** accept multimodal image inputs. For visual evaluations, use the OpenAI Decisions provider or the LLM fallback adapter.

---

## 6. Danger Zone: Long Context Extension

By default, TypeSafe AI enforces a **64k token limit** per request (with up to 32k tokens per question). 

To evaluate large documents, legal contracts, or extensive conversation transcripts, enable the `LongContext` extension. This sends the `TypeSafe-Danger-Zone-Long-Context: true` header to elevate context capacity to **262k tokens**:

### Model-Level Configuration
```go
// Enable long context for all calls made by this model
model = model.WithExtension(loomtypesafe.LongContext{Enabled: true})
```

### Call-Level Configuration
```go
// Enable long context for a single evaluation request
resp, err := model.EvaluateWithOptions(
    ctx,
    decision.Text(hugeDocument),
    questions,
    decision.WithExtensionOption(loomtypesafe.LongContext{Enabled: true}),
)
```

---

## 7. Basic Usage Example

Here is a complete example demonstrating single-question checks and batch evaluations:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/masterkeysrd/loom/decision"
    loomtypesafe "github.com/masterkeysrd/loom/decision/typesafe"
)

func main() {
    ctx := context.Background()

    // 1. Initialize the TypeSafe AI provider
    provider, err := loomtypesafe.NewDefaultProvider()
    if err != nil {
        log.Fatalf("Failed to initialize TypeSafe provider: %v", err)
    }

    // 2. Instantiate a decision model using jev-latest
    model, err := decision.NewModel(provider, loomtypesafe.ModelJevLatest, nil)
    if err != nil {
        log.Fatalf("Failed to create decision model: %v", err)
    }

    // 3. Single-question shortcut: Check (Predicate)
    input := decision.Text("Customer states: 'I never authorized this $450 transaction on my card.'")
    ans, err := model.Check(ctx, input, "Is this a fraud or unauthorized transaction dispute?")
    if err != nil {
        log.Fatal(err)
    }

    if ans.Passed(0.80) {
        fmt.Printf("Dispute flagged (probability: %.2f, confidence: %.2f)\n", 
            ans.Probability, ans.Confidence)
    }

    // 4. Batch evaluation with Predicate, Choice, and Score
    orderState := struct {
        Amount   float64 `json:"amount"`
        Shipping string  `json:"shipping"`
        NewUser  bool    `json:"new_user"`
    }{
        Amount:   1299.99,
        Shipping: "International Courier",
        NewUser:  true,
    }

    resp, err := model.Evaluate(ctx, decision.State(orderState),
        decision.Predicate("high_value", "Is this an unusually high-value order?").
            WithCriteria("Amount exceeds $1,000", "Amount is under $1,000"),
        decision.Choice("action", "Select the recommended review action",
            decision.Opt("approve", "Auto-approve order"),
            decision.Opt("hold", "Hold for verification"),
            decision.Opt("reject", "Reject transaction"),
        ),
        decision.Score("risk", "Rate transaction risk level",
            decision.Lvl("low", "Normal risk pattern"),
            decision.Lvl("medium", "Elevated risk pattern"),
            decision.Lvl("high", "Critical risk pattern"),
        ),
    )
    if err != nil {
        log.Fatal(err)
    }

    if a, ok := resp.Answer("action"); ok {
        fmt.Printf("Selected Action: %s (confidence: %.2f)\n", a.Choice, a.Confidence)
    }
    if s, ok := resp.Answer("risk"); ok {
        fmt.Printf("Risk Score: %.2f (normalized: %.2f)\n", s.Score, s.NormalizedScore())
    }
}
```

---

## Next Steps

- Learn more about [Decision Models ⚖️](../decision.md) and graph routing integration.
- Check out other providers like [OpenAI](./openai.md) and [Ollama](./ollama.md).
