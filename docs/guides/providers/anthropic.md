# Anthropic Provider 🔌

The Anthropic provider supports the Claude family of models, including Claude 3.5 Sonnet and Haiku.

## 1. Setup & Authentication

Set the `ANTHROPIC_API_KEY` environment variable.

```bash
export ANTHROPIC_API_KEY='your-api-key-here'
```

## 2. Basic Usage

```go
import (
    "github.com/masterkeysrd/loom/llm"
    "github.com/masterkeysrd/loom/llm/anthropic"
)

provider, err := loomanthropic.NewDefaultProvider()
if err != nil {
    // handle error
}

model := llm.NewModel(provider, "claude-3-5-sonnet-latest")
```

## 3. Advanced Features

### Thinking (Reasoning) Mode

Anthropic models support a dedicated "thinking" budget for extended reasoning.

```go
model = model.WithThinking(4000) // 4000 tokens for internal reasoning
```

### Adaptive Thinking

Enable the model to dynamically adjust its thinking process.

```go
model = model.WithAdaptiveThinking()
```

### Prompt Caching

Anthropic support "Prompt Caching" via ephemeral markers. Loom provides a structured way to leverage this at two levels:

#### 1. Caching the "Header" (Call Extension)
This is the most common pattern. It caches the static parts of your request (system prompt and tool definitions) so they are reused across turns.

```go
// Use the PromptCaching extension
model = model.WithExtension(loomanthropic.PromptCaching{
    CacheHeader: true,
})
```

#### 2. Manual Breakpoints (Message Extension)
You can mark specific messages in the conversation as cache checkpoints using **Message Extensions**.

```go
// Add a breakpoint to this specific turn using the fluent API
msg := message.NewUserText("Analyze this 500-page PDF...").
    WithExtension(&loomanthropic.MessageCache{Enabled: true})
```

## 4. Model Catalog

The provider maintains a list of known Anthropic models and their characteristics.

```go
profile, found := provider.GetProfile("claude-3-5-sonnet-latest")
if found {
    fmt.Println("Context Limit:", profile.Limits.Context)
}
```
