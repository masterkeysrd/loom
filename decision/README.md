# decision

The `decision` package provides a unified **System One / Decision Model** abstraction for fast, deterministic, probabilistic decision-making in Go.

Unlike open-ended generative chat completions ("System Two" reasoning), decision models evaluate structured questions against an input context (an arbitrary Go state struct, plain text string, or multimodal images) and return calibrated probabilities, confidence scores, and categorical selections.

### System One vs. System Two
- **System Two (LLM Chat & Reasoning)**: Unconstrained generative text, multi-turn dialogues, tool-calling loops, and reasoning traces (`llm` package). Higher latency, higher token cost, open-ended output.
- **System One (Decision Models)**: High-speed, calibrated probabilistic evaluation for classification, guardrails, policy verification, content moderation, sentiment analysis, and workflow routing (`decision` package). Low latency, zero parsing fragility, calibrated confidence metrics.

---

## Core Abstractions

| Type | Description |
|---|---|
| `Provider` | Interface for concrete decision backends (OpenAI Decisions, Ollama SystemOne, TypeSafe AI, LLMAdapter). |
| `Model` | High-level wrapper around a `Provider` and model identifier for probabilistic evaluation. Supports shortcuts, batching, and middleware. |
| `Question` | Structured question definition (`Predicate`, `Choice`, `Score`) with instructions, criteria, choices, or rating levels. |
| `Answer` | Calibrated evaluation result for a question, containing boolean/choice/score probability distributions, confidence, and refusal status. |
| `LLMAdapter` | Adapts any standard `llm.Model` to implement `Provider` via constrained JSON structured outputs. |
| `PredicateEdge` | Graph routing edge evaluating a binary predicate question against workflow state and routing to `Next` or `ElseNode`. |
| `ChoiceRouteEdge` | Graph routing edge evaluating a categorical choice question against workflow state and routing according to a route map. |

---

## Supported Backends

### 1. OpenAI Native Decisions API
- **Endpoint**: `/v1/decisions`
- **Supported Models**: `gpt-6-luna`
- **Provider**: `loomopenai.Provider` (from `github.com/masterkeysrd/loom/llm/openai`) implements `decision.Provider` directly.
- **Telemetry**: Native OpenTelemetry span decoration (`openai.api.type = "decisions"`).

```go
import (
    "github.com/masterkeysrd/loom/decision"
    "github.com/masterkeysrd/loom/llm/openai"
)

// Initialize OpenAI provider (reads OPENAI_API_KEY)
provider, err := loomopenai.NewDefaultProvider()
if err != nil {
    log.Fatal(err)
}

// Create decision model backed by OpenAI Decisions API
model, err := decision.NewModel(provider, "gpt-6-luna", nil)
```

### 2. Ollama Native SystemOne API
- **Endpoint**: `/v1/systemone`
- **Supported Models**: `nimble`, `clef`, `clef-flash`
- **Provider**: `loomollama.Provider` (from `github.com/masterkeysrd/loom/llm/ollama`) implements `decision.Provider` directly.
- **Telemetry**: Local execution with OpenTelemetry span decoration (`ollama.api.type = "systemone"`).

```go
import (
    "github.com/masterkeysrd/loom/decision"
    "github.com/masterkeysrd/loom/llm/ollama"
)

// Initialize Ollama provider (connects to http://localhost:11434 by default)
provider, err := loomollama.NewDefaultProvider()
if err != nil {
    log.Fatal(err)
}

// Create decision model backed by Ollama SystemOne
model, err := decision.NewModel(provider, "nimble", nil)
```

### 3. TypeSafe AI Native SystemOne API
- **Endpoints**: `POST https://api.typesafe.ai/v1/systemone` and `GET https://api.typesafe.ai/v1/models`
- **Supported Models**: `loomtypesafe.ModelJevLatest` (`"jev-latest"`), `loomtypesafe.ModelJevPreview` (`"jev-preview"`), `loomtypesafe.ModelJev1_14_0` (`"jev-1.14.0"`), `loomtypesafe.ModelJev1_13_0` (`"jev-1.13.0"`)
- **Provider**: `loomtypesafe.Provider` (from `github.com/masterkeysrd/loom/decision/typesafe`) implements `decision.Provider` and `decision.ProfileProvider` directly.
- **Features**: Ultra-low latency, **$0.042 / 1M input tokens** ($0 output tokens), automatic gzip payload compression for requests $\ge$ 1 KiB, exponential retries on 429/529, and optional `loomtypesafe.LongContext` extension up to 262k context.
- **Modality**: Text and structured JSON state (multimodal images not supported).

```go
import (
    "github.com/masterkeysrd/loom/decision"
    loomtypesafe "github.com/masterkeysrd/loom/decision/typesafe"
)

// Initialize TypeSafe AI provider (reads TYPESAFE_API_KEY)
provider, err := loomtypesafe.NewDefaultProvider()
if err != nil {
    log.Fatal(err)
}

// Create decision model backed by TypeSafe AI System One
model, err := decision.NewModel(provider, loomtypesafe.ModelJevLatest, nil)
```

### 4. LLM Fallback Adapter
- **Helper**: `decision.NewFromLLM(llmModel, nil)`
- **Adapter**: `decision.NewLLMAdapter(llmModel)`
- **Supported Backends**: Any LLM provider supported by Loom (Anthropic Claude, Google Gemini, OpenAI, etc.).
- **Mechanism**: Enforces a strict JSON Schema via `WithStructuredOutput` and calculates normalized Shannon entropy confidence scores over the predicted probabilities.

```go
import (
    "github.com/masterkeysrd/loom/decision"
    "github.com/masterkeysrd/loom/llm"
    "github.com/masterkeysrd/loom/llm/anthropic"
)

// Standard LLM model (e.g., Anthropic Claude)
anthropicProvider, _ := loomanthropic.NewDefaultProvider()
llmModel := llm.NewModel(anthropicProvider, "claude-3-5-sonnet-20241022")

// Wrap with LLMAdapter as a decision.Model
model, err := decision.NewFromLLM(llmModel, nil)
```

---

## Code Examples

### Single-Question Shortcuts
The `Model` type provides convenient shortcuts for evaluating a single question:

```go
ctx := context.Background()
input := decision.Text("Customer states: 'I was charged twice for order #9821. Please refund immediately.'")

// 1. Binary Predicate Check
ans, err := model.Check(ctx, input, "Is the customer requesting a financial refund?")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Passed: %v (Probability: %.2f, Confidence: %.2f)\n", ans.Passed(0.7), ans.Probability, ans.Confidence)

// 2. Categorical Choice
choices := []decision.Option{
    decision.Opt("billing", "Billing disputes, invoices, duplicate charges, or refunds"),
    decision.Opt("tech_support", "Technical troubleshooting, bugs, or error messages"),
    decision.Opt("sales", "New product inquiries, upgrade requests, or pricing quotes"),
}
choiceAns, err := model.Choose(ctx, input, "Classify customer ticket intent", choices)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Selected: %s (Probability: %.2f, Confidence: %.2f)\n", choiceAns.Choice, choiceAns.Probability, choiceAns.Confidence)

// 3. Ordinal Rating / Score
levels := []decision.Level{
    decision.Lvl("calm", "Neutral, constructive, or pleasant inquiry"),
    decision.Lvl("frustrated", "Mild displeasure, impatience, or annoyance"),
    decision.Lvl("irate", "Hostile, threatening legal/regulatory escalation"),
}
rateAns, err := model.Rate(ctx, input, "Rate customer frustration level", levels)
if err != nil {
    log.Fatal(err)
}
top, _ := rateAns.TopLevel()
fmt.Printf("Score: %.2f / %d (Top: %s, Normalized: %.2f)\n", rateAns.Score, len(levels)-1, top.Label, rateAns.NormalizedScore())
```

### Batch Evaluation
Evaluate multiple questions in a single model invocation:

```go
input := decision.Text("I need to upgrade our enterprise cluster to 16 nodes next Tuesday.")

resp, err := model.Evaluate(ctx, input,
    decision.Predicate("is_urgent", "Does this request indicate an immediate deadline or urgency?").
        WithCriteria("Explicit timeframe within one week", "No immediate deadline or unspecified schedule"),
    decision.Choice("department", "Route to responsible team",
        decision.Opt("devops", "Infrastructure, clusters, and scaling"),
        decision.Opt("sales", "Contract upgrades and sales expansion"),
        decision.Opt("security", "Compliance and vulnerability audits"),
    ),
    decision.Score("lead_score", "Evaluate customer conversion and revenue readiness",
        decision.Lvl("low", "Casual inquiry, student, or unverified lead"),
        decision.Lvl("medium", "Qualified business user exploring options"),
        decision.Lvl("high", "Enterprise ready for immediate expansion"),
    ),
)
if err != nil {
    log.Fatal(err)
}

if urgentAns, ok := resp.Answer("is_urgent"); ok {
    fmt.Printf("Urgent: %v (p=%.2f)\n", urgentAns.Passed(0.75), urgentAns.Probability)
}
if deptAns, ok := resp.Answer("department"); ok {
    fmt.Printf("Department: %s (confidence=%.2f)\n", deptAns.Choice, deptAns.Confidence)
}
if scoreAns, ok := resp.Answer("lead_score"); ok {
    fmt.Printf("Lead score: %.2f (normalized: %.2f)\n", scoreAns.Score, scoreAns.NormalizedScore())
}
```

### Multimodal Image Evaluation
Decision models can inspect multimodal image inputs alongside text or structured state:

```go
imgData, _ := os.ReadFile("receipt.png")

input := decision.Text("Verify whether this receipt image meets corporate expense criteria").
    WithImages(
        message.ImageData("image/png", imgData),
        message.ImageURL("https://example.com/receipt-signature.jpg"),
    )

ans, err := model.Check(ctx, input, "Is the receipt legible, dated within 30 days, and includes the merchant tax ID?")
if err != nil {
    log.Fatal(err)
}

if ans.Passed(0.85) {
    fmt.Println("Receipt verified successfully!")
}
```

### Interpreting Answers
The `Answer` struct provides calibrated metrics and convenience accessors:

```go
// 1. Binary Predicate
if ans.Passed(0.80) {
    // True condition met with >= 0.80 probability and not refused
}

// 2. Choice Probabilities
pBilling := ans.Prob("billing")
pTech := ans.Prob("tech_support")

// 3. Ordinal Scores
norm := ans.NormalizedScore() // Normalized to [0.0, 1.0]
if top, ok := ans.TopLevel(); ok {
    fmt.Printf("Highest probability level: %s (Index %d, p=%.2f)\n", top.Label, top.Index, top.Probability)
}

// 4. Uncertainty & Safety
if ans.Refused {
    // Model refused to answer (e.g., safety policies or ambiguity)
}
if ans.Confidence < 0.60 {
    // High entropy / uncertainty across options; route to human review
}
```

### Graph Routing Integration
The `decision` package integrates seamlessly with Loom workflows via `PredicateEdge` and `ChoiceRouteEdge`:

```go
type SupportTicketState struct {
    CustomerMessage string `json:"customer_message"`
    Category        string `json:"category"`
    Status          string `json:"status"`
}

func (s SupportTicketState) Copy() SupportTicketState { return s }

builder := graph.New[SupportTicketState]()

// 1. Binary Conditional Edge
isUrgentEdge := decision.NewConditionalEdge(
    model,
    "expedited_queue",   // Next node if threshold passed
    "Does this ticket mention production downtime or severe data loss?",
    0.80,                // Probability threshold
    func(s SupportTicketState) decision.Input {
        return decision.Text(s.CustomerMessage)
    },
).WithElse("standard_triage") // Else node if probability < 0.80 or refused

decision.AddConditionalEdge(builder, "intake", isUrgentEdge)

// 2. Categorical Choice Route Edge
departmentEdge := decision.NewRouteEdge(
    model,
    "Classify support ticket department",
    []decision.Option{
        decision.Opt("billing", "Billing, invoicing, refunds"),
        decision.Opt("technical", "Technical issues, errors, API bugs"),
    },
    map[string]string{
        "billing":   "billing_agent",
        "technical": "technical_agent",
    },
    func(s SupportTicketState) decision.Input {
        return decision.Text(s.CustomerMessage)
    },
).
    WithMinConfidence(0.70).         // Require confidence >= 0.70
    WithFallback("human_escalation") // Route here on refusal, low confidence, or unmapped choice

decision.AddRouteEdge(builder, "standard_triage", departmentEdge)
```
