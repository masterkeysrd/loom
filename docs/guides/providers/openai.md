# OpenAI Provider 🔌

The OpenAI provider allows you to use all standard GPT models, including reasoning models like `o1` and `o3-mini`, as well as standard models like `gpt-4o`.

## 1. Installation

Ensure you have the Loom package installed:

```bash
go get github.com/masterkeysrd/loom
```

## 2. Setup & Authentication

The OpenAI provider uses the `OPENAI_API_KEY` environment variable for authentication by default.

```bash
export OPENAI_API_KEY='your-api-key-here'
```

## 3. Basic Usage

```go
import (
    "github.com/masterkeysrd/loom/llm"
    "github.com/masterkeysrd/loom/llm/openai"
)

// Create the provider
provider, err := loomopenai.NewDefaultProvider()
if err != nil {
    // handle error
}

// Instantiate a model
model := llm.NewModel(provider, "gpt-4o")
```

## 4. Advanced Features

### Reasoning Models (Thinking)

For models like `o1` or `o3-mini` that support internal reasoning, you can configure the thinking effort.

```go
model = model.WithThinkingEffort("medium") // low, medium, high
```

### Structured Output

OpenAI's native JSON Schema enforcement is supported.

```go
model = model.WithStructuredOutput(myJsonSchema)
```

### Prompt Caching

OpenAI automatically caches prompt prefixes longer than 1024 tokens. Loom allows you to optimize this behavior using the `PromptCache` extension:

#### 1. Cache Bucketing (Key)
Use a stable identifier to improve cache hit rates by bucketing similar requests together.

```go
model = model.WithExtension(loomopenai.PromptCache{
    Key: "user-session-123",
})
```

#### 2. Extended Retention
By default, caches are ephemeral. You can enable extended caching (up to 24 hours) for specific prefixes.

```go
model = model.WithExtension(loomopenai.PromptCache{
    Retention: "24h",
})
```

## 5. Model Catalog

The provider includes a static catalog of common OpenAI models with their context limits and capabilities. You can search or list them at runtime:

```go
profiles := provider.ListProfiles()
for _, p := range profiles {
    fmt.Printf("Model: %s, Context: %d\n", p.ID, p.Limits.Context)
}
```
