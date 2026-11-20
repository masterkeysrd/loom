---
title: Stream and configure responses
---

Use the model and messages from [your first model call](../llm-package.md). To display text as it arrives, replace the blocking `Invoke` call with `Stream`.

```go
chunks, err := model.Stream(ctx, messages)
if err != nil {
    return err
}
for chunk, err := range chunks {
    if err != nil {
        return err
    }
    fmt.Print(message.Content(chunk.Content).Text())
}
```

## Set generation options

The fluent API returns a configured model. Choose options supported by the model you selected.

```go
model = model.WithTemperature(0.7).
    WithMaxTokens(1000).
    WithStop("User:", "Assistant:")
```

## Structured Output (JSON Schema)
If a provider supports it, you can enforce a specific JSON schema for the model's response.
```go
model = model.WithStructuredOutput(myJsonSchema)
```

## Thinking (Reasoning) Mode
For models that support "thinking" or extended reasoning (like Anthropic Claude or Google Gemini), you can configure the reasoning budget.
```go
model = model.WithThinking(4000) // 4000 tokens for thinking
```

## Extensions & Hints
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
