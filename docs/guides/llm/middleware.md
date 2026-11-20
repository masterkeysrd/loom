---
title: Apply model middleware
---

Middleware allows you to wrap LLM calls to inject cross-cutting concerns like logging, auditing, or automatic metadata injection.

```go
model = model.WithMiddleware(func(next llm.Streamer) llm.Streamer {
    return func(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
        fmt.Printf("Calling model: %s\n", req.Model)

        return next(ctx, req)
    }
})
```

## Built-in middleware for Deferred Tools & Inline Tools

Loom includes built-in middlewares that bring deferred tool loading and mid-conversation inline tool injection to any provider:

### Deferred Tools (`llm.DeferredToolsMiddleware`)
```go
// Default configuration: automatically detects provider capability via llm.DeferredToolsSupport
model = model.WithMiddleware(llm.DeferredToolsMiddleware(nil))

// Or with custom options:
model = model.WithMiddleware(llm.DeferredToolsMiddleware(&llm.DeferredToolsOptions{
    Match: func(ctx context.Context, req *llm.Request) bool {
        return true // force fallback transformation
    },
    FormatReference: func(def tool.Definition) message.Block {
        return &message.TextBlock{Text: fmt.Sprintf("Tool %s is active", def.Name)}
    },
    DisableCoercion: false,
}))
```

### Inline Tools (`llm.InlineToolsMiddleware`)
```go
// Default configuration: automatically detects provider capability via llm.InlineToolsSupport
model = model.WithMiddleware(llm.InlineToolsMiddleware(nil))

// Or with custom options:
model = model.WithMiddleware(llm.InlineToolsMiddleware(&llm.InlineToolsOptions{
    Match: func(ctx context.Context, req *llm.Request) bool {
        return true // force fallback transformation
    },
    FormatDefinition: func(def tool.Definition) message.Block {
        return &message.TextBlock{Text: fmt.Sprintf("Inline tool %s loaded", def.Name)}
    },
    DisableCoercion: false,
}))
```

When used with providers that don't natively support deferred or inline tools (e.g. Ollama or Gemini), these middlewares strip matching tools from initial `req.Tools`, expand `ToolReferenceBlock` and `ToolDefinitionBlock` references into full schemas, and coerce stringified arguments returned by models into typed JSON schema values. See the [Tools Guide](../tools.md#6-deferred-tools-tool-search--dynamic-tool-injection) for more details.
