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

Loom supports two ways to activate or inject tools mid-conversation without pre-registering them in the initial `req.Tools` header:

#### 1. Injection by Reference (`message.ToolReferenceBlock`)
For tools that were pre-registered in the model's catalog as deferred (`tool.WithDeferLoading()`), activate them by name:

```go
// Dynamically activate a pre-registered deferred tool
msg := message.NewSystem(&message.ToolReferenceBlock{ToolName: "convert_currency"})
// or: message.NewUser(&message.ToolReferenceBlock{ToolName: "convert_currency"})
```

#### 2. Injection by Value (`message.ToolDefinitionBlock` & `tool.ToBlock()`)
For dynamic tools that were **not** pre-registered in `req.Tools`, inject the complete definition inline using `tool.ToBlock()`:

```go
calcTool, _ := tool.New("calculate_mortgage", "...", "...", handler)

// Inject the complete tool schema inline into the conversation
msg := message.NewUser(
    &message.TextBlock{Text: "Please calculate my mortgage:"},
    calcTool.ToBlock(), // returns *message.ToolDefinitionBlock
)
```

Both Anthropic (via `tool_addition` / `inline-tools-2026-09-15`), OpenAI Responses API (via `additional_tools`), and Loom's fallback middleware handle mid-conversation tool injection seamlessly.

### Cross-Provider Middleware (`llm.DeferredToolsMiddleware` & `llm.InlineToolsMiddleware`)

To ensure seamless execution across all providers—including those without native server-side deferred tool loading or inline tool additions (such as Ollama, Gemini, or older OpenAI/Anthropic models)—Loom provides two complementary middlewares:

#### 1. `llm.DeferredToolsMiddleware`
Handles deferred tools and `ToolReferenceBlock` resolution:
- **Capability Check**: Checks `llm.DeferredToolsSupport` (`SupportsDeferredTools(model)`).
- **Native Providers**: Claude 4.5+/5+, OpenAI `/v1/responses` with `gpt-5.4+` (no-op).
- **Fallback Transformation**: Strips `DeferLoading: true` tools from `req.Tools` to keep the prompt cache prefix static, transforms `ToolReferenceBlock`s into `<tools>` schema text blocks, and coerces stringified tool call arguments.

```go
model = model.WithMiddleware(llm.DeferredToolsMiddleware(nil))
```

#### 2. `llm.InlineToolsMiddleware`
Handles mid-conversation `ToolDefinitionBlock`s injected by value:
- **Capability Check**: Checks `llm.InlineToolsSupport` (`SupportsInlineTools(model)`).
- **Native Providers**: Claude 4.8 / Claude 5 families, OpenAI `/v1/responses` with `gpt-5.4+` (no-op).
- **Fallback Transformation**: Omit any tool matching an inline definition from `req.Tools`, transforms `ToolDefinitionBlock`s into `<tools>` schema text blocks in cloned messages, and coerces stringified arguments returned for inline tools based on `InputSchema`.

```go
model = model.WithMiddleware(llm.InlineToolsMiddleware(nil))
```

#### Configuration Options:
Both middlewares support custom options (`Match`, `FormatDefinition`/`FormatReference`, and `DisableCoercion`):

```go
opts := &llm.InlineToolsOptions{
    // Match overrides provider capability detection when non-nil
    Match: func(ctx context.Context, req *llm.Request) bool { ... },
    // FormatDefinition customizes how inline tool schemas are formatted into text
    FormatDefinition: func(def tool.Definition) message.Block { ... },
    // DisableCoercion disables automatic type coercion of tool call arguments
    DisableCoercion: false,
}
model = model.WithMiddleware(llm.InlineToolsMiddleware(opts))
```

## Summary

- **`tool.New`**: For standard request-response tools.
- **`tool.NewStreaming`**: For tools that need to report progress or stream multi-part results.
- **`tool.Container`**: For managing and executing multiple tools.
- **`tool.WithDeferLoading`**: Marks tools as deferred to reduce token consumption.
- **`tool.ToBlock()`**: Converts a `*tool.Tool` or `tool.Definition` to a `*message.ToolDefinitionBlock` for inline injection.
- **`tool.NewBM25Search` / `tool.NewRegexSearch`**: Built-in search tools returning `ToolReferenceBlock`s.
- **`llm.DeferredToolsMiddleware`**: Universal middleware bringing deferred tools and argument coercion to all models.
- **`llm.InlineToolsMiddleware`**: Universal middleware bringing mid-conversation inline tool definitions to all models.
- **`g.Stream`**: For real-time event monitoring.
