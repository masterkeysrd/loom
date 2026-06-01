# tool

The `tool` package provides a type-safe framework for defining tools that LLMs can call. It eliminates the need for manual JSON schema boilerplate by using Go's reflection and struct tags.

## Usage

Define a tool by providing its name, description, and a handler function. The input and output types will be automatically converted to JSON schemas.

```go
type WeatherInput struct {
    City string `json:"city" jsonschema:"description=The city to get weather for"`
}

type WeatherOutput struct {
    Temperature float64 `json:"temperature"`
    Condition   string  `json:"condition"`
}

weatherTool, err := tool.New(
    "get_weather",
    "Weather Service",
    "Returns current weather for a city.",
    func(ctx context.Context, in WeatherInput) (WeatherOutput, error) {
        // Logic to fetch weather...
        return WeatherOutput{22.5, "Sunny"}, nil
    },
)
```

## Tool Container

`Container` is a collection of tools that can be easily converted into a format suitable for various LLM providers.

```go
container := tool.NewContainer()
container.Add(weatherTool)

// Pass to an LLM request
req := &llm.Request{
    Messages: messages,
    Tools:    container.Definitions(),
}
```

## Tool Streaming

Loom supports tools that stream their results (e.g. for protocol-based yields like MCP) or provide real-time progress updates to the UI.

```go
type LogInput struct {
    Lines int `json:"lines"`
}

logTool, _ := tool.NewStreaming(
    "get_logs",
    "Log Service",
    "Streams recent system logs.",
    func(ctx context.Context, in LogInput) (tool.ToolStream, error) {
        return func(yield func(message.ToolChunk, error) bool) {
            // 1. Send an ephemeral progress update with numerical data
            total := float64(in.Lines)
            yield(message.ToolChunk{
                Progress: "Streaming lines...",
                ProgressTotal: &total,
            }, nil)
            
            // 2. Stream actual log blocks
            for i := 0; i < in.Lines; i++ {
                current := float64(i + 1)
                yield(message.ToolChunk{
                    ProgressCurrent: &current,
                    Content: message.Content{&message.TextBlock{Text: "log line..."}},
                }, nil)
            }
        }, nil
    },
)

// The framework automatically aggregates these chunks when the LLM calls the tool.
// If a stream.Writer is present in the context, chunks are also forwarded to the UI in real-time.
// Each chunk is automatically tagged with the tool's name (e.g. Source: "tool:get_logs").
```

## Error Handling

The framework provides sentinel errors to help applications manage tool execution failures gracefully.

```go
resp, err := container.Call(ctx, toolCall)
if err != nil {
    if errors.Is(err, tool.ErrInvalidInput) {
        // The LLM provided arguments that don't match the schema.
        // You can use errors.As to get more details:
        var valErr *tool.ValidationError
        errors.As(err, &valErr)
        
        fmt.Printf("Validation failed for tool %s: %v\n", valErr.ToolName, valErr.Err)
        
        // Return a helpful message to the LLM so it can retry
        return &message.Tool{
            ToolCallID: toolCall.ID,
            Name:       toolCall.Name,
            Content:    message.Content{&message.TextBlock{Text: "Invalid arguments provided."}},
        }, nil
    }
    return nil, err
}
```
