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
defer client.Close()

// Extract tools as native Loom tools
mcpTools, _ := client.Tools(ctx)

// Integrate with Loom container
container := tool.NewContainer(mcpTools...)
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

## 6. Deferred Tools, Tool Search & Dynamic Tool Injection

When working with large tool catalogs (e.g. hundreds of tools across multiple MCP servers or enterprise APIs), sending every tool schema in the initial request consumes excessive context window tokens and degrades LLM reasoning. Loom supports **Deferred Tools** and **Tool Search**, allowing you to keep tools deferred and dynamically discover or activate them mid-conversation.

### Marking Tools as Deferred

To mark a tool as deferred during creation, pass `tool.WithDeferLoading()`:

```go
weatherTool, _ := tool.New(
    "get_weather",
    "Get Weather",
    "Fetches real-time weather for a city",
    weatherHandler,
    tool.WithDeferLoading(),
)
```

For existing tools (such as MCP tools returned by `mcpClient.Tools(ctx)`), use `WithDeferLoading(true)`:

```go
mcpTools, _ := mcpClient.Tools(ctx)
for _, t := range mcpTools {
    t.WithDeferLoading(true)
}
```

Deferred tools have `Definition.DeferLoading = true`.

### Built-in Client Search Tools

Loom provides built-in search tools that index a catalog of deferred tools and allow the LLM to search for tools by keyword or relevance:

```go
// BM25 full-text ranker (recommended for natural language queries)
searchTool, err := tool.NewBM25Search(allTools, 5) // top 5 results

// Or exact regex matcher
regexSearchTool, err := tool.NewRegexSearch(allTools, 5)
```

When invoked by the model, these search tools return `[]message.ToolReferenceBlock` elements containing the matching tool names.

### Direct Mid-Conversation Tool Injection

You can also inject a deferred tool directly into an active conversation without waiting for a search tool call. Simply append a `message.ToolReferenceBlock` to a system or user message:

```go
// Dynamically activate a tool mid-conversation
msg := message.NewSystem(&message.ToolReferenceBlock{ToolName: "convert_currency"})
// or: message.NewUser(&message.ToolReferenceBlock{ToolName: "convert_currency"})
```

Anthropic (via `tool_addition` / `inline-tools-2026-09-15`), OpenAI Responses API (via `additional_tools`), and Loom's fallback middleware handle mid-conversation tool reference blocks seamlessly.

### Cross-Provider Middleware (`llm.DeferredToolsMiddleware`)

To ensure seamless deferred tool execution across all providers—even those without native server-side deferred tool loading (such as Ollama, Gemini, or older OpenAI/Anthropic models)—Loom provides `llm.DeferredToolsMiddleware`:

```go
model = model.WithMiddleware(llm.DeferredToolsMiddleware(nil))
```

#### How it Works:
1. **Capability Detection**: Checks if the underlying provider implements `llm.DeferredToolsSupport` and returns `true` for the current model via `SupportsDeferredTools(model)`.
2. **Native Providers**: On models with native deferred tool support (Claude 4.5+/5+, OpenAI `/v1/responses` with `gpt-5.4+`), the middleware acts as a transparent no-op so native server-side handling is preserved.
3. **Fallback Transformation**: On non-native providers (Ollama, Gemini, older models):
   - **Filters Deferred Tools**: Strips `DeferLoading: true` tools out of `req.Tools` on every turn, keeping the system prompt invariant to preserve prompt cache prefixes.
   - **Transforms Tool References**: Replaces `*message.ToolReferenceBlock` instances (found in user/system messages or `ServerToolResult` blocks) with full `<tools>` JSON schemas in cloned messages, leaving original conversation state untouched.
   - **Coerces Tool Arguments**: In the streaming response, intercepts tool calls for deferred tools and automatically coerces stringified arguments (commonly emitted by XML tool parsers like Ollama) to their typed equivalents (`float64`, `int64`, `bool`, `[]any`, `map[string]any`) according to the tool's `InputSchema`.

#### Configuration Options:
```go
opts := &llm.DeferredToolsOptions{
    // Match overrides provider capability detection when non-nil
    Match: func(ctx context.Context, req *llm.Request) bool { ... },
    // FormatReference customizes how activated tool schemas are formatted into text
    FormatReference: func(def tool.Definition) message.Block { ... },
    // DisableCoercion disables automatic type coercion of tool call arguments
    DisableCoercion: false,
}
model = model.WithMiddleware(llm.DeferredToolsMiddleware(opts))
```

## Summary

- **`tool.New`**: For standard request-response tools.
- **`tool.NewStreaming`**: For tools that need to report progress or stream multi-part results.
- **`tool.Container`**: For managing and executing multiple tools.
- **`tool.WithDeferLoading`**: Marks tools as deferred to reduce token consumption.
- **`tool.NewBM25Search` / `tool.NewRegexSearch`**: Built-in search tools returning `ToolReferenceBlock`s.
- **`llm.DeferredToolsMiddleware`**: Universal middleware bringing deferred tools and argument coercion to all models.
- **`g.Stream`**: For real-time event monitoring.
