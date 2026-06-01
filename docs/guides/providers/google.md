# Google Gemini Provider 🔌

The Google Gemini provider (GenAI) supports Gemini 1.5 Pro, Flash, and Gemini 2.0 models.

## 1. Setup & Authentication

Set the `GOOGLE_API_KEY` environment variable.

```bash
export GOOGLE_API_KEY='your-api-key-here'
```

## 2. Basic Usage

```go
import (
    "context"
    "github.com/masterkeysrd/loom/llm"
    "github.com/masterkeysrd/loom/llm/genai"
)

ctx := context.Background()
provider, err := loomgenai.NewDefaultProvider(ctx)
if err != nil {
    // handle error
}

model := llm.NewModel(provider, "gemini-1.5-pro")
```

## 3. Advanced Features

### Reasoning

Configure thinking levels for models that support it.

```go
model = model.WithThinking(2000)
```

## 4. Model Profiles

Google provider models are statically defined and include information about multimodal support and context window sizes.

```go
profiles := provider.SearchProfiles("gemini-1.5")
```
