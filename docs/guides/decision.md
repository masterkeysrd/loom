# Decision Models ⚖️

The `decision` package introduces a first-class **System One / Decision Model** abstraction for Loom. 

While Large Language Models excels at open-ended generative tasks, multi-turn reasoning, and conversational chat ("System Two"), modern AI workflows frequently require fast, deterministic, probabilistic decisions ("System One")—such as intent routing, guardrails, policy enforcement, classification, and triage.

Decision models evaluate structured questions against an input context (arbitrary Go state structs, plain text strings, or multimodal images) and return calibrated probabilities, confidence scores, and categorical selections without the latency, token overhead, and parsing fragility of open-ended generation.

---

## System One vs. System Two

| Dimension | System One (`decision`) | System Two (`llm`) |
|---|---|---|
| **Primary Task** | Fast classification, predicates, scoring, routing | Generative responses, reasoning traces, tool loops |
| **Output Type** | Calibrated probabilities, choices, ordinal scores | Open-ended text, streaming tokens, tool calls |
| **Latency** | Extremely low (< 100ms on native backends) | Higher (seconds, token streaming) |
| **Deterministic Structure** | Strongly typed `Answer` structs | Freeform text or schema validation |
| **Uncertainty Calibration** | Explicit probabilities & Shannon entropy confidence | Logprobs or heuristic estimation |

---

## 1. Core Abstractions

- **`Provider`**: The backend implementation that executes decision evaluations. Native backends include OpenAI Decisions, Ollama SystemOne, and TypeSafe AI; any LLM can also be adapted.
- **`Model`**: The high-level wrapper configured with a provider and model name. Supports single-question shortcuts, batch evaluations, and middleware chains.
- **`Question`**: The question definition:
  - **`Predicate`**: Binary true/false question with probability and criteria descriptions.
  - **`Choice`**: Categorical classification selecting among discrete options with probability distributions.
  - **`Score`**: Ordinal rating evaluated across an ordered list of descriptive levels.
- **`Answer`**: The calibrated result containing probabilities, selected choices, expected score, entropy-based confidence, and refusal status.
- **`PredicateEdge` / `ChoiceRouteEdge`**: Graph edges that integrate decision evaluations directly into Loom workflow graphs.

---

## 2. Supported Providers

### OpenAI Native Decisions API
OpenAI exposes dedicated decision endpoints (`/v1/decisions`) optimized for low-latency probabilistic evaluations using models such as `gpt-6-luna`.

Loom's `loomopenai.Provider` implements `decision.Provider` out of the box:

```go
import (
    "github.com/masterkeysrd/loom/decision"
    "github.com/masterkeysrd/loom/providers/openai"
)

// Uses OPENAI_API_KEY from environment
provider, err := loomopenai.NewDefaultProvider()
if err != nil {
    log.Fatal(err)
}

// Pass provider directly to decision.NewModel
model, err := decision.NewModel(provider, "gpt-6-luna", nil)
```

### Ollama Native SystemOne API
Ollama provides local, private inference via the native `/v1/systemone` API using lightweight models such as `nimble`, `clef`, and `clef-flash`.

Loom's `loomollama.Provider` implements `decision.Provider` out of the box:

```go
import (
    "github.com/masterkeysrd/loom/decision"
    "github.com/masterkeysrd/loom/providers/ollama"
)

// Connects to http://localhost:11434 by default
provider, err := loomollama.NewDefaultProvider()
if err != nil {
    log.Fatal(err)
}

// Pass provider directly to decision.NewModel
model, err := decision.NewModel(provider, "nimble", nil)
```

### TypeSafe AI Native SystemOne API
TypeSafe AI provides dedicated, high-speed System One endpoints (`POST https://api.typesafe.ai/v1/systemone` and `GET https://api.typesafe.ai/v1/models`) specifically architected for deterministic probabilistic decision-making.

Loom's `loomtypesafe.Provider` implements `decision.Provider` and `profile.Provider` out of the box:

```go
import (
    "github.com/masterkeysrd/loom/decision"
    loomtypesafe "github.com/masterkeysrd/loom/providers/typesafe"
)

// Uses TYPESAFE_API_KEY from environment
provider, err := loomtypesafe.NewDefaultProvider()
if err != nil {
    log.Fatal(err)
}

// Pass provider directly to decision.NewModel with jev-latest
model, err := decision.NewModel(provider, loomtypesafe.ModelJevLatest, nil)
```

Features:
- **Ultra-low cost**: $0.042 per 1M input tokens ($0 output tokens).
- **Purpose-built models**: `loomtypesafe.ModelJevLatest` (`"jev-latest"`), `loomtypesafe.ModelJevPreview` (`"jev-preview"`), `loomtypesafe.ModelJev1_14_0` (`"jev-1.14.0"`), and `loomtypesafe.ModelJev1_13_0` (`"jev-1.13.0"`).
- **Extended context extension**: Use `loomtypesafe.LongContext{Enabled: true}` to raise context limits from 64k to 262k tokens.
- **Payload optimization & retries**: Automatic gzip compression for requests $\ge$ 1 KiB, exponential retries on `429` and `529`.
- **Modality**: Text and structured JSON state (multimodal images not supported).

### LLM Fallback Adapter
If your preferred provider does not offer a native decision endpoint (e.g., Anthropic Claude or Google Gemini), you can adapt any standard `llm.Model` into a decision model using `decision.NewFromLLM`.

The adapter leverages constrained structured outputs (`WithStructuredOutput`) and automatically computes calibrated confidence scores using normalized Shannon entropy:

```go
import (
    "github.com/masterkeysrd/loom/decision"
    "github.com/masterkeysrd/loom/llm"
    "github.com/masterkeysrd/loom/providers/anthropic"
)

anthropicProvider, _ := loomanthropic.NewDefaultProvider()
llmModel := llm.NewModel(anthropicProvider, "claude-3-5-sonnet-20241022")

// Wrap LLM model with the adapter
model, err := decision.NewFromLLM(llmModel, nil)
```

---

## 3. Single-Question Shortcuts

For common decision patterns, `Model` exposes dedicated single-question helper methods:

### Binary Predicate (`Check`)
Evaluates whether a statement or condition is true:

```go
ctx := context.Background()
input := decision.Text("Customer: 'I would like to cancel my subscription and get a refund.'")

ans, err := model.Check(ctx, input, "Is the customer requesting subscription cancellation?")
if err != nil {
    log.Fatal(err)
}

if ans.Passed(0.80) {
    fmt.Printf("Cancellation confirmed (probability: %.2f, confidence: %.2f)\n", ans.Probability, ans.Confidence)
}
```

### Categorical Choice (`Choose`)
Selects the best matching category from a list of options:

```go
choices := []decision.Option{
    decision.Opt("billing", "Billing queries, invoices, payment methods, or refunds"),
    decision.Opt("technical", "Software errors, bug reports, system outages"),
    decision.Opt("general", "General product inquiries and feature questions"),
}

ans, err := model.Choose(ctx, input, "Classify customer support ticket", choices)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Selected category: %s (confidence: %.2f)\n", ans.Choice, ans.Confidence)
fmt.Printf("Probability for 'billing': %.2f\n", ans.Prob("billing"))
```

### Ordinal Rating (`Rate`)
Evaluates an item across an ordered scale of descriptive levels, returning both level probabilities and a probability-weighted expected score:

```go
levels := []decision.Level{
    decision.Lvl("low", "Low urgency; standard inquiry or casual question"),
    decision.Lvl("medium", "Moderate urgency; account issue or unexpected behavior"),
    decision.Lvl("high", "High urgency; critical service failure or active security issue"),
}

ans, err := model.Rate(ctx, input, "Assess ticket urgency level", levels)
if err != nil {
    log.Fatal(err)
}

top, _ := ans.TopLevel()
fmt.Printf("Expected Score: %.2f / %d\n", ans.Score, len(levels)-1)
fmt.Printf("Normalized Score [0..1]: %.2f\n", ans.NormalizedScore())
fmt.Printf("Top Level: %s (p=%.2f)\n", top.Label, top.Probability)
```

---

## 4. Batch Evaluation

When you need multiple answers evaluated against the same input context, `Evaluate` runs them in a single network roundtrip:

```go
type OrderDetails struct {
    OrderID      string  `json:"order_id"`
    Amount       float64 `json:"amount"`
    ShippingCity string  `json:"shipping_city"`
    IsNewAccount bool    `json:"is_new_account"`
}

input := decision.State(OrderDetails{
    OrderID:      "ORD-7749",
    Amount:       4850.00,
    ShippingCity: "International Freight Forwarder",
    IsNewAccount: true,
})

resp, err := model.Evaluate(ctx, input,
    decision.Predicate("high_fraud_risk", "Evaluate whether this order exhibits high risk of credit fraud").
        WithCriteria("New account with large order to freight forwarder", "Verified user with consistent history"),
    decision.Choice("review_action", "Determine required risk mitigation action",
        decision.Opt("auto_approve", "Safe to process immediately"),
        decision.Opt("manual_review", "Hold for fraud analyst inspection"),
        decision.Opt("decline", "High certainty fraud; reject transaction"),
    ),
    decision.Score("risk_score", "Rate fraud risk on a 4-point scale",
        decision.Lvl("safe", "Normal legitimate pattern"),
        decision.Lvl("guarded", "Minor irregularities present"),
        decision.Lvl("elevated", "Multiple suspicious indicators"),
        decision.Lvl("critical", "Severe fraud indicators"),
    ),
)
if err != nil {
    log.Fatal(err)
}

if fraudAns, ok := resp.Answer("high_fraud_risk"); ok {
    fmt.Printf("Fraud Flag: %v (p=%.2f)\n", fraudAns.Passed(0.70), fraudAns.Probability)
}
if actionAns, ok := resp.Answer("review_action"); ok {
    fmt.Printf("Recommended Action: %s (confidence: %.2f)\n", actionAns.Choice, actionAns.Confidence)
}
if scoreAns, ok := resp.Answer("risk_score"); ok {
    fmt.Printf("Risk Score: %.2f (normalized: %.2f)\n", scoreAns.Score, scoreAns.NormalizedScore())
}
```

---

## 5. Multimodal Image Evaluation

Decision inputs support images via `WithImages`, allowing decision models to inspect visual evidence alongside text prompts or structured state:

```go
licenseImage, _ := os.ReadFile("driver_license.jpg")

input := decision.Text("Verify driver's license upload for KYC identity verification").
    WithImages(
        message.ImageData("image/jpeg", licenseImage),
    )

ans, err := model.Check(ctx, input, "Is the ID document clearly legible and unexpired?")
if err != nil {
    log.Fatal(err)
}

if ans.Passed(0.90) {
    fmt.Println("KYC document verified.")
} else {
    fmt.Println("Document rejected or requires manual inspection.")
}
```

---

## 6. Interpreting Calibrated Answers

The `Answer` struct provides helper methods to interpret probabilistic results:

### Probability & Passing Thresholds
For binary predicates, `Passed(threshold)` verifies that the question was not refused and its probability satisfies your threshold:

```go
if ans.Passed(0.85) {
    // Proceed with high confidence
}
```

### Choice Probabilities
Retrieve specific choice probabilities via `Prob(value)`:

```go
pAdmin := ans.Prob("admin")
pUser := ans.Prob("user")
```

### Ordinal Scores
- `ans.Score`: Probability-weighted expected score ($\sum i \cdot P(i)$).
- `ans.NormalizedScore()`: Expected score scaled to $[0.0, 1.0]$.
- `ans.TopLevel()`: Returns the `LevelProbability` with the highest probability.

### Confidence & Refusals
- `ans.Confidence`: Normalized Shannon entropy concentration ($1 - H(p)/\ln(N)$) clamped between `0.0` (maximum uncertainty) and `1.0` (complete certainty).
- `ans.Refused`: Indicates the model refused to evaluate the question (due to safety filters, unanswerable queries, or missing context).

---

## 7. Workflow Graph Routing Integration

Decision models integrate directly with Loom workflow graphs (`graph.Builder`) via `PredicateEdge` and `ChoiceRouteEdge`.

### Conditional Binary Branching (`PredicateEdge`)

Use `decision.AddConditionalEdge` to route workflow execution based on a binary decision:

```go
type OrderState struct {
    Amount    float64 `json:"amount"`
    UserEmail string  `json:"user_email"`
    Action    string  `json:"action"`
}

func (s OrderState) Copy() OrderState { return s }

builder := graph.New[OrderState]()

builder.AddNode("validate", validateOrderNode)
builder.AddNode("require_2fa", prompt2FANode)
builder.AddNode("process_payment", chargeCardNode)

// Route from "validate": if high risk, go to "require_2fa", otherwise "process_payment"
edge := decision.NewConditionalEdge(
    decisionModel,
    "require_2fa", // Next node when predicate passes threshold
    "Is this transaction unusually high-value or suspect?",
    0.75,          // Confidence threshold
    func(s OrderState) decision.Input {
        return decision.State(s)
    },
).WithElse("process_payment") // Fallback node when below threshold or refused

decision.AddConditionalEdge(builder, "validate", edge)
```

### Categorical Route Edge (`ChoiceRouteEdge`)

Use `decision.AddRouteEdge` to branch execution across multiple nodes with confidence safeguards and a designated fallback route:

```go
routeEdge := decision.NewRouteEdge(
    decisionModel,
    "Determine the department responsible for handling this inquiry",
    []decision.Option{
        decision.Opt("billing", "Payment, billing questions, invoices"),
        decision.Opt("tech", "Software defects, outages, API errors"),
        decision.Opt("sales", "Pricing, sales inquiries, new features"),
    },
    map[string]string{
        "billing": "billing_agent",
        "tech":    "technical_support_agent",
        "sales":   "sales_agent",
    },
    func(s OrderState) decision.Input {
        return decision.Text(s.UserEmail)
    },
).
    WithMinConfidence(0.70).          // Require at least 70% confidence
    WithFallback("human_triage_queue") // Route here on low confidence, refusal, or unmapped choice

decision.AddRouteEdge(builder, "classify_inquiry", routeEdge)
```

---

## 8. Observability & Telemetry

Decision models include built-in OpenTelemetry instrumentation via `TelemetryMiddleware`:

- **Client Spans**: Every decision request records a span named `decision {model}` with semantic conventions:
  - `gen_ai.operation.name = "decision"`
  - `gen_ai.provider.name = "<provider>"`
  - `gen_ai.request.model = "<model>"`
  - `decision.question_count = <int>`
- **Token Metrics**: Input and output token usage, cache reads, and estimated costs are automatically reported to OpenTelemetry metrics.
- **Streaming Events**: When a `stream.Writer` is attached to the context, `on_decision` events are published with the evaluation response and metadata source `decision:<model>`.

---

## Next Steps

- Explore [OpenAI Provider](./providers/openai.md) for configuring `gpt-6-luna` with native decisions.
- Explore [Ollama Provider](./providers/ollama.md) for running local `nimble` and `clef` System One models.
- Explore [TypeSafe AI Provider](./providers/typesafe.md) for purpose-built, high-throughput System One decision models.
- Review [Graph Workflows](./quickstart.md) to learn how to construct complex graph agents.
