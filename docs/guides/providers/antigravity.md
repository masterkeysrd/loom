# Google Antigravity Provider 🔌

The Google Antigravity provider connects Loom to Google's internal Cloud Code / Antigravity LLM backend, supporting streaming completions, dynamic model discovery, thinking effort configuration, and quota inspection.

## 1. Setup & Authentication

The provider supports three authentication methods:

### Option A: Interactive Browser OAuth 2.0 PKCE (Recommended)
Automatically runs a local loopback server, requests authorization from Google, securely stores credentials in `~/.config/loom/antigravity_token.json`, and handles background token refresh:

```go
import "github.com/masterkeysrd/loom/llm/antigravity"

oauthProvider, err := loomantigravity.NewOAuth2TokenProvider(loomantigravity.OAuth2Config{})
if err != nil {
    log.Fatal(err)
}

// Interactively log in if no token is saved
if err := oauthProvider.Login(ctx); err != nil {
    log.Fatal(err)
}
```

Or authenticate via the example CLI tool:
```bash
go run ./examples/antigravity --login
```

### Option B: Environment Variables
Export a valid bearer token:
```bash
export ANTIGRAVITY_TOKEN="<your-oauth-bearer-token>"
# or
export GOOGLE_ACCESS_TOKEN="<your-oauth-bearer-token>"
```
The provider will automatically pick up `ANTIGRAVITY_TOKEN` or `GOOGLE_ACCESS_TOKEN`.

### Option C: Static Token
Pass an existing token programmatically:
```go
tokenProvider := loomantigravity.StaticToken("<your-oauth-bearer-token>")
```

---

## 2. Basic Usage

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/masterkeysrd/loom/llm"
    loomantigravity "github.com/masterkeysrd/loom/llm/antigravity"
    "github.com/masterkeysrd/loom/message"
)

func main() {
    ctx := context.Background()

    // 1. Initialize provider
    provider, err := loomantigravity.NewDefaultProvider()
    if err != nil {
        log.Fatal(err)
    }

    // 2. Instantiate model (e.g. Gemini 3.8 Flash)
    model := llm.NewModel(provider, "gemini-3.8-flash")

    // 3. Stream a completion
    stream, err := model.Stream(ctx, message.MessageList{
        message.NewUserText("Explain quantum computing in three sentences."),
    })
    if err != nil {
        log.Fatal(err)
    }

    for chunk, err := range stream {
        if err != nil {
            log.Fatal(err)
        }
        fmt.Print(chunk.Content.Text())
    }
    fmt.Println()
}
```

---

## 3. Advanced Features

### Thinking & Reasoning Effort

Antigravity models support configurable thinking budgets and levels (`low`, `medium`, `high`):

```go
// 1. Via fluent Model configuration
model = model.WithThinkingEffort("high")

// 2. Or using dynamic model alias names
fastModel := llm.NewModel(provider, "gemini-3.8-flash-low")
deepModel := llm.NewModel(provider, "gemini-3.8-flash-high")
```

The provider's heuristic resolver automatically sets `thinkingConfig.thinkingLevel = "HIGH"` and routes requests to the underlying gateway endpoint (such as `gemini-3.8-flash-tiered`).

### Dynamic Model Discovery & Resolution

Antigravity discovers available models dynamically at runtime directly from Google's `/v1internal:fetchAvailableModels` endpoint:

- **Zero hardcoded catalogs**: Newly published models (e.g. Gemini 3.9, Claude updates) work immediately without code changes.
- **Compact View (`RawProfiles: false`, default)**: Groups endpoints by canonical family and surfaces reasoning effort levels.
- **Raw View (`RawProfiles: true`)**: Exposes all raw backend endpoints and variants.
- **Custom Aliases & Resolvers**: Map custom shorthand names (e.g. `"fast" -> "gemini-3.8-flash-low"`) or inject a custom `ModelResolver` function in `loomantigravity.Config`.

```go
provider, _ := loomantigravity.NewProvider(&loomantigravity.Config{
    TokenProvider: oauthProvider,
    Aliases: map[string]string{
        "fast": "gemini-3.8-flash-low",
        "smart": "gemini-2.5-pro",
    },
})
```

---

## 4. Quota & Rate Limit Inspection (`llm.QuotaProvider`)

The Antigravity provider implements Loom's [`llm.QuotaProvider`](../llm-package.md#5-quota--rate-limit-inspection-llmquotaprovider) interface. Quotas are decoupled from the 1-hour profile metadata cache to ensure fresh reporting without staleness:

```go
// Assert llm.QuotaProvider
if qp, ok := provider.(llm.QuotaProvider); ok {
    // Check quota for a specific model (resolves aliases automatically)
    if quota, found, err := qp.GetQuota(ctx, "gemini-3.8-flash"); found && err == nil {
        fmt.Printf("Remaining: %.1f%%\n", quota.Percentage())
        if reset, err := quota.ParsedResetTime(); err == nil && !reset.IsZero() {
            fmt.Printf("Replenishes at: %s\n", reset.Format(time.RFC3339))
        }
    }

    // List all model quotas
    quotas, _ := qp.ListQuotas(ctx)
    for modelID, q := range quotas {
        fmt.Printf("• %-30s | Remaining: %5.1f%%\n", modelID, q.Percentage())
    }
}
```

### Cache Configuration

Quota caching can be tuned in `loomantigravity.Config`:
- **`QuotaCacheTTL: 30 * time.Second`** (Default): Avoids redundant network roundtrips during rapid polling.
- **`QuotaCacheTTL: -1`**: Bypasses the cache and always executes direct live network queries.
- Direct uncached queries can also be executed via `provider.Client().FetchQuotas(ctx)`.

---

## 5. Token Counting & Metrics (`llm.TokenCounter`)

The Antigravity provider implements Loom's [`llm.TokenCounter`](../../llm/token_counter.go) interface and provides comprehensive token metrics during streaming:

### Pre-call Token Counting (`CountTokens`)

Computes exact token counts for message sequences using Google Cloud Code's `/v1internal:countTokens` endpoint. If network or server issues occur, it gracefully falls back to `llm.ApproximateTokenCounter`:

```go
// Assert llm.TokenCounter
if counter, ok := provider.(llm.TokenCounter); ok {
    msgs := message.MessageList{
        message.NewSystemText("You are an expert Go developer."),
        message.NewUserText("Explain channels in Go."),
    }
    count, err := counter.CountTokens(ctx, msgs)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Total tokens: %d\n", count)
}
```

This enables seamless integration with Loom's [`memory.Summarizer`](../../memory/summarizer.go) and conversational memory pruning.

### Streaming Token Metrics

The provider extracts detailed token usage from `UsageMetadata` chunks:
- `chunk.Metrics.TotalTokens`: Total tokens.
- `chunk.Metrics.Tokens.Input`: Prompt / input token count.
- `chunk.Metrics.Tokens.Output`: Candidate completion token count.
- `chunk.Metrics.Tokens.CacheRead`: Tokens served from Google's cached content (`cachedContentTokenCount`).
- `chunk.Metrics.Tokens.Reasoning`: Reasoning / thinking tokens generated by the model (`thoughtsTokenCount`).

---

## 6. CLI Test Runner

The repository includes a ready-to-run interactive CLI test runner at `examples/antigravity`:

```bash
# 1. Authenticate via Google OAuth
go run ./examples/antigravity --login

# 2. Inspect available models and remaining quotas
go run ./examples/antigravity --list-models

# 3. View raw endpoints
go run ./examples/antigravity --list-models --raw

# 4. Check detailed quota and reset windows
go run ./examples/antigravity --quota

# 5. Count tokens for a prompt using the server endpoint
go run ./examples/antigravity --count-tokens "The quick brown fox jumps over the lazy dog."

# 6. Run a prompt with thinking effort and inspect token metrics
go run ./examples/antigravity --model gemini-3.8-flash --effort high --prompt "Analyze the complexity of QuickSort."
```

