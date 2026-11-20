---
title: Discover models and register providers
---

Use model discovery to find IDs available to your credentials. Use the registry when application configuration determines which provider to instantiate.

## List available models

Providers that expose model-listing APIs (OpenAI, Anthropic, Google GenAI, Ollama, and Google Antigravity) implement [`llm.ModelLister`](https://pkg.go.dev/github.com/masterkeysrd/loom/llm#ModelLister):

```go
if ml, ok := provider.(llm.ModelLister); ok {
    // Query all models available to your credentials or local server
    models, err := ml.ListModels(ctx)
    if err == nil {
        for _, m := range models {
            fmt.Printf("• %s (%s) — ctx=%d\n", m.ID, m.Name, m.Limits.Context)
        }
    }

    // Fetch live metadata for a single model by ID
    prof, err := ml.GetModel(ctx, "claude-sonnet-4-6")
    _ = prof
    _ = err
}
```

- **Static-catalog providers (`openai`, `anthropic`, `genai`)**: Enrich live API results with static catalog metadata (preserving `Pricing`, `TieredLimits`, `Modalities`, and `Capabilities`) without mutating `provider.ListProfiles()`. Unknown models (such as fine-tunes `ft:...`, tuned Gemini models, or OpenAI-compatible vLLM/LiteLLM servers) receive synthesized profiles from live API metadata.
- **Dynamic-catalog providers (`ollama`, `antigravity`)**: Refresh their in-memory discovered model cache while preserving any explicit `provider.OverrideProfile(...)` entries.

## Register a provider factory

The `Registry` is useful for decoupling your application from specific provider packages. It allows you to register provider factories and retrieve them by name.

```go
registry := llm.NewRegistry()
registry.Register("openai", func() (llm.Provider, error) {
    return loomopenai.NewDefaultProvider()
})
provider, err := registry.Get("openai")
if err != nil {
    return err
}
model, err := llm.NewModel(provider, modelID, nil)
if err != nil {
    return err
}
```

## Check supported inputs and capabilities

All built-in providers (`openai`, `anthropic`, `genai`, `ollama`, `copilot`, `claudecode`, `antigravity`, and `typesafe`) enforce strict validation across `llm`, `embedding`, and `decision` requests so unsupported attachments are **never silently dropped**:

1. **Profile & Capability Gate (When Known)**: When `provider.GetProfile(model)` resolves an authoritative `profile.Profile`, Loom validates message/input modalities against `Modalities.Inputs` and tool definitions against `Capabilities.ToolCall` (as well as `Completion`, `Embedding`, or `Decision` workload flags). Unknown or custom model IDs with no catalog profile skip this step automatically.
2. **Wire-Protocol Gate (Always Enforced)**: Every provider's message mapper validates `System`, `User`, `Assistant`, `Tool`, `embedding.Input`, and `decision.Input` blocks—rejecting unsupported block types, unsupported roles (e.g., images inside `System` prompts), empty media blocks, or unsupported delivery modes (e.g., URL-only audio where inline bytes are required). Historical `*message.ThinkingBlock` items remain lenient on replay.

```go
import (
    "errors"

    "github.com/masterkeysrd/loom/profile"
)

resp, err := model.Invoke(ctx, messages)
if errors.Is(err, profile.ErrUnsupportedModality) {
    // Handle unsupported image/audio/video/document modality or URL source
}
if errors.Is(err, profile.ErrUnsupportedCapability) {
    // Handle unsupported capability (e.g., tools bound to a non-tool model)
}
```
