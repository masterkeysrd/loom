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
stream, err := model.Stream(ctx, messages)
for chunk, err := range stream {
    fmt.Print(chunk.Content.Text())
}
```

## 2. Using the LLM Registry

The `Registry` is useful for decoupling your application from specific provider packages. It allows you to register provider factories and retrieve them by name.

```go
registry := llm.NewRegistry()

// Register a provider
registry.Register("openai", func() (llm.Provider, error) {
    return loomopenai.NewDefaultProvider()
})

// Later, retrieve and use it
provider, _ := registry.Get("openai")
model := llm.NewModel(provider, "gpt-4o")
```

## 3. Advanced Configuration

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

## Summary

- Use `llm.NewModel` to start interacting with an LLM.
- Use the `With*` methods to configure model parameters.
- Use `Invoke` for simple request-response and `Stream` for real-time applications.
- Use `llm.Registry` to manage multiple providers in a large application.
