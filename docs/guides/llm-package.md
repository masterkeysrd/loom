# LLM Package 🧠

The `llm` package provides a high-level abstraction over different Large Language Model (LLM) backends. It allows you to interact with models from various providers (OpenAI, Anthropic, Google, Ollama) using a consistent API.

## Core Components

- **`Model`**: The primary interface for interacting with an LLM. It supports both blocking (`Invoke`) and streaming (`Stream`) calls.
- **`Provider`**: The backend-specific implementation that translates generic requests into provider-specific API calls.
- **`Registry`**: A central store for managing and instantiating providers without creating hard dependencies.

## 1. Using the Model API

The `llm.Model` provides a fluent API for configuring and calling LLMs.

```go
import (
    "github.com/masterkeysrd/loom/llm"
    "github.com/masterkeysrd/loom/llm/openai"
)

// 1. Create a provider
provider, _ := loomopenai.NewDefaultProvider()

// 2. Instantiate a model
model := llm.NewModel(provider, "gpt-4o")

// 3. Configure parameters (Fluent API)
model = model.
    WithTemperature(0.7).
    WithMaxTokens(1000).
    WithStop("User:", "Assistant:")

// 4. Invoke the model (blocking)
resp, err := model.Invoke(ctx, messages)

// 5. Stream the model (real-time)
// Chunks are automatically forwarded to any stream.Writer in the context.
stream, err := model.Stream(ctx, messages)
for chunk, err := range stream {
    fmt.Print(chunk.Content.Text())
}
```

## 2. Advanced Configuration

### Structured Output (JSON Schema)
If a provider supports it, you can enforce a specific JSON schema for the model's response.
```go
model = model.WithStructuredOutput(myJsonSchema)
```

### Thinking (Reasoning) Mode
For models that support "thinking" or extended reasoning (like Anthropic Claude or Google Gemini), you can configure the reasoning budget.
```go
model = model.WithThinking(4000) // 4000 tokens for thinking
```

### Extensions & Hints
Providers often have specialized features (like caching) that aren't part of the common LLM parameters. Loom uses a type-safe **Extensions** system to pass these hints.

Extensions come in two flavors:
1.  **Call Extensions (`llm.Extension`)**: Apply to the entire request (e.g., enable system prompt caching).
2.  **Message Extensions (`message.Extension`)**: Apply to a specific turn in the conversation (e.g., mark a checkpoint).

```go
// Example: Setting a call-level extension for Anthropic
model = model.WithExtension(loomanthropic.PromptCaching{CacheHeader: true})

// Example: Passing a call-level extension for a single call
model.Invoke(ctx, msgs, llm.WithExtensionOption(loomopenai.PromptCache{
    Key: "user-session-123",
}))
```

## 3. Middleware & Hooks
Middleware allows you to wrap LLM calls to inject cross-cutting concerns like logging, auditing, or automatic metadata injection.

```go
model = model.WithMiddleware(func(next llm.Streamer) llm.Streamer {
    return func(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
        fmt.Printf("Calling model: %s\n", req.Model)
        
        // You can also modify the request before it reaches the provider
        if len(req.Messages) > 10 {
            req.Extensions[myprovider.CustomExt{}.ExtensionID()] = myprovider.CustomExt{Enabled: true}
        }

        return next(ctx, req)
    }
})
```

## 4. Cache Management
For providers that support explicit resource management (like Google Gemini's Context Caching), Loom provides a `CacheManager` API.

```go
// 1. Create a long-lived cache from a list of messages
cacheID, err := model.
    WithExtension(loomgenai.CacheCreation{DisplayName: "Project Docs"}).
    CreateCache(ctx, documentMessages)

// 2. Use the cache ID in subsequent calls
resp, _ := model.Invoke(ctx, prompt, llm.WithExtensionOption(loomgenai.ContextCache{ID: cacheID}))

// 3. Delete when no longer needed
model.DeleteCache(ctx, cacheID)
```

## 5. Quota & Rate Limit Inspection (`llm.QuotaProvider`)

Providers that expose quota or rolling-window capacity metrics (such as Google Antigravity) implement the [`llm.QuotaProvider`](file:///Users/masterkeysrd/Projects/loom/llm/provider.go) interface. This allows callers to inspect remaining capacity percentages and reset times:

```go
// Type-assert to llm.QuotaProvider
if qp, ok := provider.(llm.QuotaProvider); ok {
    // Inspect a specific model (resolves aliases like "gemini-3.8-flash")
    if quota, ok, err := qp.GetQuota(ctx, "gemini-3.8-flash"); ok && err == nil {
        fmt.Printf("Remaining: %.1f%%\n", quota.Percentage())
        if reset, err := quota.ParsedResetTime(); err == nil && !reset.IsZero() {
            fmt.Printf("Replenishes at: %s\n", reset.Format(time.RFC3339))
        }
    }

    // List all model quotas
    quotas, _ := qp.ListQuotas(ctx)
    for model, q := range quotas {
        fmt.Printf("• %s: %.1f%%\n", model, q.Percentage())
    }
}
```

## 6. Using the LLM Registry
The `Registry` is useful for decoupling your application from specific provider packages. It allows you to register provider factories and retrieve them by name.

## Summary

- Use `llm.NewModel` to start interacting with an LLM.
- Use the `With*` methods to configure model parameters.
- Use `Invoke` for simple request-response and `Stream` for real-time applications.
- Use `llm.QuotaProvider` to query model quotas and replenishment windows.
- Use `llm.Registry` to manage multiple providers in a large application.
