// Package tool provides a type-safe framework for defining LLM-callable tools.
//
// A tool bundles a name, description, and JSON schemas inferred from Go types,
// together with a handler function that is invoked when the LLM calls the tool.
// Use [New] to create a [Tool] from a typed [HandlerFunc]; the factory
// automatically infers and resolves the input and output JSON schemas via
// reflection so callers never have to write schema boilerplate.
//
// # Error Handling
//
// The framework provides sentinel errors to help applications manage tool execution
// failures gracefully. When a tool is called with arguments that do not match its
// input schema, [Container.Call] returns a [ValidationError] that satisfies [ErrInvalidInput].
// Applications can use [errors.Is] or [errors.As] to detect these failures and decide
// how to report them back to the LLM (e.g., by providing schema feedback).
//
// # Streaming
//
// The framework supports tools that stream their results (e.g., for long-running tasks
// or protocol-based yields like MCP). Use [NewStreaming] to define a tool that
// yields [message.ToolChunk] values. The [Container.Stream] method provides an
// iterator-based interface for consuming these chunks, mirroring the streaming
// pattern used in the llm package. Chunks are automatically aggregated into a
// single [message.Tool] result for the LLM while being forwarded to any [stream.Writer]
// in the context (e.g. for real-time UI updates).
//
// Example:
//
//	resp, err := container.Call(ctx, call)

//	if errors.Is(err, tool.ErrInvalidInput) {
//	    // Handle validation error (e.g., tell the LLM it sent bad arguments)
//	}
//
// Example:
//
//	type WeatherInput struct {
//		City string `json:"city" jsonschema:"city to get the weather for"`
//	}
//
//	type WeatherOutput struct {
//		Temperature float64 `json:"temperature"`
//		Condition   string  `json:"condition"`
//	}
//
//	def, err := tool.New(
//		"get_weather",
//		"Get Weather",
//		"Returns current weather for a given city.",
//		func(ctx context.Context, input WeatherInput) (WeatherOutput, error) {
//			return WeatherOutput{Temperature: 22.5, Condition: "sunny"}, nil
//		},
//	)
package tool
