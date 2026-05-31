# message

The `message` package provides the fundamental data structures for conversation history in Loom. It is designed to be provider-agnostic, strongly typed, and perfectly serializable to JSON.

## Key Features

- **Polymorphic Content**: Messages can contain multiple blocks of different types (Text, Image, Audio, Video, Document, Tool Calls, Thinking/Chain-of-Thought).
- **Flattened JSON**: Serializes to a clean, industry-standard flat structure compatible with standard LLM APIs and SQL JSONB columns.
- **Type Safety**: Explicit types for `User`, `Assistant`, `System`, and `Tool` messages.
- **Memory Management**: Built-in utilities for message trimming and token counting.

## Usage

### Creating Messages

```go
history := message.MessageList{
    message.NewSystemText("You are a helpful assistant."),
    message.NewUserText("What's the weather like?"),
    message.NewUserImage(imageData, "image/jpeg"), // New multimodal support
}
```

### Multimodal Blocks

Loom supports multiple media types within messages.

```go
msg := &message.User{
    Content: message.Content{
        &message.TextBlock{Text: "Analyze this image and audio:"},
        &message.ImageBlock{URL: "https://example.com/image.jpg"},
        &message.AudioBlock{Data: audioData, MIMEType: "audio/mpeg"},
    },
}
```

### Complex Content (Thinking & Tool Calls)

Loom supports "Thinking" blocks (Chain-of-Thought) and Tool Calls within assistant messages.

```go
msg := &message.Assistant{
    Content: message.Content{
        &message.ThinkingBlock{Thinking: "I should check the weather for London."},
        &message.ToolCall{
            Name: "get_weather",
            Args: map[string]any{"city": "London"},
        },
    },
}
```

### Trimming History

Manage context window limits by trimming messages from the start or end of the history.

```go
trimmed, err := message.TrimMessages(ctx, history, 4000, &message.TrimConfig{
    Strategy:      message.TrimStrategyLast, // Keep the newest messages
    IncludeSystem: true,                      // Always keep the system message at index 0
    CountTokens:   myTokenCounter,
})
```

## JSON Structure

Example of a serialized `MessageList`:

```json
[
  {
    "role": "user",
    "id": "018e...",
    "content": [{"kind": "text", "text": "Hello!"}]
  },
  {
    "role": "assistant",
    "id": "018e...",
    "content": [
      {"kind": "thinking", "thinking": "The user said hello. I should respond politely."},
      {"kind": "text", "text": "Hi there! How can I help you today?"}
    ]
  }
]
```
