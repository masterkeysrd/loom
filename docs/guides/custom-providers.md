# Custom Providers (Advanced) 🛠️

Loom is designed to be provider-agnostic. While it comes with built-in support for OpenAI, Anthropic, Gemini, and Ollama, you can easily add support for any other LLM backend by implementing the `llm.Provider` interface.

## 1. The Provider Interface

To create a custom provider, you must implement the following interface:

```go
type Provider interface {
    Name() string
    Stream(context.Context, *Request) (StreamResponse, error)
    ListProfiles() []ModelProfile
    GetProfile(id string) (ModelProfile, bool)
}
```

### Optional: Cache Management
If your provider supports explicit resource management for context caching, you can optionally implement the `CacheManager` interface:

```go
type CacheManager interface {
    CreateCache(context.Context, *Request) (string, error)
    DeleteCache(context.Context, string) error
}
```

### Optional: Extensions
If your provider has specialized features, you should define your own extension structs.

1.  **Define the struct**: It must implement either `llm.Extension` or `message.Extension`.
2.  **Implement `ExtensionID()`**: Return a unique string (e.g. `myprovider.feature`).
3.  **Register (Message level only)**: If it's a message-level extension, call `message.RegisterExtension` in your `init()` function so it can be restored from JSON.

```go
func init() {
    message.RegisterExtension(func() message.Extension { return &MyMsgExt{} })
}
```

## 2. Implementing the Stream Method

The core of a provider is the `Stream` method. It takes a generic `llm.Request` and returns an iterator over `message.AssistantChunk` values.

```go
func (p *MyCustomProvider) Stream(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
    // 1. Translate llm.Request to your provider's specific API format
    apiReq := translate(req)

    // 2. Call your backend's streaming API
    resp, _ := p.client.CallStreaming(ctx, apiReq)

    // 3. Return an iterator
    return func(yield func(message.AssistantChunk, error) bool) {
        for {
            chunk, err := resp.Next()
            if err != nil {
                yield(message.AssistantChunk{}, err)
                return
            }
            
            // 4. Translate back to Loom's AssistantChunk
            loomChunk := translateBack(chunk)
            if !yield(loomChunk, nil) {
                return
            }
        }
    }, nil
}
```

## 3. Mapping Messages & Tools

Most of the work in creating a provider involves mapping between Loom's block-based message system and the provider's specific message format (e.g., Anthropic's `ContentBlock` vs OpenAI's `ToolCall`).

### Key Mapping Areas:
- **Multimodal Content**: Mapping `message.ImageBlock` to the provider's base64 or URL format.
- **Tool Definitions**: Translating `tool.Definition` into the provider's JSON schema format.
- **Tool Calls**: Capturing tool call IDs and arguments from the stream.

## 4. Registering Your Provider

Once implemented, you can register your provider with the `llm.Registry` to make it available to the rest of your application.

```go
registry := llm.NewRegistry()
registry.Register("my-provider", func() (llm.Provider, error) {
    return &MyCustomProvider{}, nil
})
```

## Summary

- Implementing a custom provider allows Loom to talk to any internal or specialized LLM API.
- Focus on accurate mapping of `llm.Request` and `message.AssistantChunk`.
- Use the `llm/openai` or `llm/anthropic` packages as reference implementations.
