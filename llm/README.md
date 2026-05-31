# llm

The `llm` package provides a provider-agnostic abstraction for interacting with Large Language Models (LLMs). It allows you to write code once and swap between different model backends (OpenAI, Anthropic, Ollama, etc.) with minimal changes.

## Core Abstractions

| Type | Description |
|---|---|
| `Provider` | Interface for concrete backends (OpenAI, Ollama, etc.). Handles API communication. |
| `Model` | A high-level wrapper around a `Provider` and a specific model ID (e.g., `gpt-4o`). |
| `Request` | The common format for LLM instructions, including messages and tools. |
| `Registry` | A thread-safe map for resolving providers by name at runtime. |

## Supported Providers & Configuration

Loom ships with support for major LLM providers. Each can be configured with default environment variables or by passing a custom client.

### OpenAI
- **Package**: `github.com/masterkeysrd/loom/llm/openai`
- **Auth**: `OPENAI_API_KEY` environment variable.

```go
import "github.com/masterkeysrd/loom/llm/openai"

// Use defaults (reads OPENAI_API_KEY)
p, _ := loomopenai.NewDefaultProvider()

// Or provide a custom client
client := openai.NewClient(option.WithAPIKey("custom-key"))
p := loomopenai.NewProvider(client)
```

### Anthropic
- **Package**: `github.com/masterkeysrd/loom/llm/anthropic`
- **Auth**: `ANTHROPIC_API_KEY` environment variable.

```go
import "github.com/masterkeysrd/loom/llm/anthropic"

p, _ := loomanthropic.NewDefaultProvider()
```

### Google Gemini
- **Package**: `github.com/masterkeysrd/loom/llm/genai`
- **Auth**: `GOOGLE_API_KEY` environment variable.

```go
import "github.com/masterkeysrd/loom/llm/genai"

p, _ := loomgenai.NewDefaultProvider(ctx)
```

### Ollama (Local)
- **Package**: `github.com/masterkeysrd/loom/llm/ollama`
- **Config**: `OLLAMA_HOST` (defaults to `http://localhost:11434`).

```go
import "github.com/masterkeysrd/loom/llm/ollama"

p, _ := loomollama.NewDefaultProvider()
```

## Configuring the Model

The `Model` type supports a fluent API for fine-tuning LLM parameters. Each configuration method returns a cloned model with the new settings, making it safe to reuse base models with different parameters.

```go
model := llm.NewModel(provider, "gpt-4o").
    WithTemperature(0.7).
    WithMaxTokens(1000).
    WithStop("###", "Observation:").
    WithThinking(2048).
    WithThinkingEffort("high").
    WithJSON() // Force JSON mode

// Invoke with these specific settings
resp, err := model.Invoke(ctx, messages)
```

Available configuration methods:
- `WithTemperature(float32)`: Controls randomness (0.0 to 2.0).
- `WithTopP(float32)`: Nucleus sampling diversity.
- `WithTopK(int)`: Limits vocabulary to top K tokens.
- `WithMaxTokens(int)`: Sets a limit on output length.
- `WithStop(...string)`: Custom stop sequences.
- `WithJSON()`: Enables JSON response format (provider-specific).
- `WithThinking(budget int)`: Enables thinking/reasoning with a specific token budget.
- `WithThinkingEffort(effort string)`: Sets reasoning intensity (e.g., "low", "medium", "high").
- `WithAdaptiveThinking()`: Enables adaptive reasoning mode (Anthropic).
- `WithConfig(llm.ModelConfig)`: Bulk configuration using a struct.

## Using the Model Wrapper

The `Model` type provides the primary entry point for invoking LLMs.

```go
model := llm.NewModel(provider, "gpt-4o")

// 1. Blocking Invoke
resp, err := model.Invoke(ctx, messages)

// 2. Streaming Invoke
stream, err := model.Stream(ctx, messages)
for chunk, err := range stream {
    fmt.Print(chunk.Text)
}
```

## The Provider Registry

Use the `Registry` to dynamically select providers based on configuration.

```go
registry := llm.NewRegistry()
registry.Register("openai", func() (llm.Provider, error) {
    return openai.NewProvider(config), nil
})
registry.Register("ollama", func() (llm.Provider, error) {
    return ollama.NewProvider(config), nil
})

// Later in your application...
p, _ := registry.Get("openai")
```

## Token Counting

The `llm` package includes an interface for token counting, which is essential for memory management (summarization and trimming). Providers often implement their own logic or use common libraries like `tiktoken`.

```go
counter := provider.(llm.TokenCounter)
count, _ := counter.CountTokens(ctx, messages)
```
