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
    "github.com/masterkeysrd/loom/providers/anthropic"
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

#### 1. Caching the "Header" or Auto-Caching (Call Extension)
This is the most common pattern. It caches the static parts of your request (system prompt and tool definitions) so they are reused across turns, or auto-caches the last cacheable block using top-level cache control.

```go
// Cache the static invariant header (tools and system prompt)
model = model.WithExtension(loomanthropic.PromptCaching{
    CacheHeader: true,
})

// Or auto-cache the entire conversation up to the last message turn
model = model.WithExtension(loomanthropic.PromptCaching{
    Auto: true,
})
```

#### 2. Manual Breakpoints (Message Extension)
You can mark specific messages in the conversation as cache checkpoints using **Message Extensions**.

```go
// Add a breakpoint to this specific turn using the fluent API
msg := message.NewUserText("Analyze this 500-page PDF...").
    WithExtension(&loomanthropic.MessageCache{Enabled: true})
```

### Server-Side Tool Search & Deferred Tools

Modern Claude models support native server-side tool search and deferred tool loading. This allows you to define hundreds of tools without consuming prompt tokens upfront:

```go
// 1. Mark tools as deferred
calcTool, _ := tool.New("calculate_mortgage", "...", "...", handler, tool.WithDeferLoading())

// 2. Enable server-side tool search (BM25 or Regex)
model = model.
    BindTools(calcTool).
    WithExtension(loomanthropic.ToolSearch{
        Strategy: loomanthropic.ToolSearchBM25, // or loomanthropic.ToolSearchRegex
    })
```

#### Mid-Conversation Tool Injection
You can also dynamically activate a deferred tool mid-conversation without waiting for a tool search call by appending a `ToolReferenceBlock` to a system or user message:

```go
// Claude activates the tool schema via the inline-tools-2026-09-15 beta header
msg := message.NewSystem(&message.ToolReferenceBlock{ToolName: "convert_currency"})
```

#### Model Support
- **Native Support**: Claude 4.5+ and Claude 5 families (`claude-sonnet-4-6`, `claude-opus-4-6`, `claude-haiku-4-5`, `claude-sonnet-5`, `claude-fable-5`, etc.).
- **Fallback for Older Models**: For Claude 3.5 Sonnet, Claude 3 Opus, or other earlier models, attach `llm.DeferredToolsMiddleware(nil)` to your model. The middleware automatically handles client-side filtering, schema injection, and type coercion.

## 4. Model Catalog

The provider maintains a list of known Anthropic models and their characteristics.

```go
profile, found := provider.GetProfile("claude-3-5-sonnet-latest")
if found {
    fmt.Println("Context Limit:", profile.Limits.Context)
}
```
