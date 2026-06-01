# Tools & Streaming 🛠️

Tools allow your AI agents to interact with external systems, APIs, and databases. Loom provides a robust, type-safe way to define tools and execute them within your graphs.

## 1. Defining a Tool

Loom uses Go reflection to automatically generate the JSON schema for your tools, which is then sent to the LLM.

```go
type SearchInput struct {
    Query string `json:"query"`
}

type SearchOutput struct {
    Results []string `json:"results"`
}

searchTool, _ := tool.New(
    "web_search",          // Name
    "Web Search",         // Display Name
    "Searches the web",   // Description
    func(ctx context.Context, in SearchInput) (SearchOutput, error) {
        // Implementation logic
        return SearchOutput{Results: []string{"Result 1"}}, nil
    },
)
```

## 2. Using the Tool Container

The `tool.Container` groups multiple tools together and handles their execution.

```go
container := tool.NewContainer(searchTool, calculatorTool)

// Bind tool definitions to the model
model = model.BindToolDefs(container.Definitions()...)
```

In a graph node, you can execute a tool call received from the LLM:

```go
resp, err := container.Call(ctx, assistantToolCall)
```

## 3. Streaming Tools

Loom supports "Streaming Tools" which can report progress to the UI or yield multiple parts of a result (e.g., text followed by an image).

```go
processorTool, _ := tool.NewStreaming(
    "analyze_data",
    "Data Processor",
    "Analyzes data and returns results",
    func(ctx context.Context, in struct{ Dataset string }) (tool.ToolStream, error) {
        return func(yield func(message.ToolChunk, error) bool) {
            // Report progress
            curr := 1.0; total := 2.0
            yield(message.ToolChunk{
                Progress: "Analyzing...",
                ProgressCurrent: &curr,
                ProgressTotal: &total,
            }, nil)

            // Yield result
            yield(message.ToolChunk{
                Content: message.Content{&message.TextBlock{Text: "Result"}},
            }, nil)
        }, nil
    },
)
```

## 4. MCP Tools

Loom provides first-class support for the Model Context Protocol (MCP). You can dynamically extract tools from any MCP-compliant server and add them to your `tool.Container`.

```go
// Connect to an MCP server
client := mcp.NewClient(mcp.Config{
    Transport: "stdio",
    Command:   "python",
    Args:      []string{"my_server.py"},
})

// Extract tools
session, _ := client.Session(ctx)
mcpTools, _ := session.Tools(ctx)

// Integrate with Loom container
container := tool.NewContainer()
container.AddTools(mcpTools...)
```

See the [MCP Guide](./mcp.md) for more details.

## 5. Graph Streaming

To observe tool progress and token-level updates from the LLM, use the `g.Stream` method.

```go
events, _ := g.Stream(ctx, input, nil)

for event, err := range events {
    // The 'Source' field identifies the origin (e.g., "llm:gpt-4o" or "tool:web_search")
    fmt.Printf("[%s] ", event.Source)

    switch event.Event {
    case graph.EventToolProgress:
        chunk := event.Data.(message.ToolChunk)
        fmt.Println("Progress:", chunk.Progress)
    case graph.EventLLMChunk:
        chunk := event.Data.(message.AssistantChunk)
        fmt.Print(chunk.Content.Text())
    }
}
```

## Summary

- **`tool.New`**: For standard request-response tools.
- **`tool.NewStreaming`**: For tools that need to report progress or stream multi-part results.
- **`tool.Container`**: For managing and executing multiple tools.
- **`g.Stream`**: For real-time event monitoring.
