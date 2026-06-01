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

### Context Caching

Google Gemini supports long-lived context caches for large datasets or documents. Unlike Anthropic's implicit markers, Gemini caches must be explicitly created and referenced by ID.

#### 1. Creating a Cache
You can use `model.CreateCache` to upload content and get a cache ID.

```go
cacheID, _ := model.
    WithExtension(loomgenai.CacheCreation{
        DisplayName: "Project Docs",
        TTL:         1 * time.Hour,
    }).
    CreateCache(ctx, documentMessages)
```

#### 2. Using the Cache
Pass the ID back using the `ContextCache` extension.

```go
resp, _ := model.Invoke(ctx, prompt, llm.WithExtensionOption(loomgenai.ContextCache{ID: cacheID}))
```

#### 3. Deleting the Cache
It is good practice to delete the cache when finished to save on costs.

```go
model.DeleteCache(ctx, cacheID)
```

## 4. Model Profiles

Google provider models are statically defined and include information about multimodal support and context window sizes.

```go
profiles := provider.SearchProfiles("gemini-1.5")
```
