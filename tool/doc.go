// Package tool provides a type-safe framework for defining LLM-callable tools.
//
// A tool bundles a name, description, and JSON schemas inferred from Go types,
// together with a handler function that is invoked when the LLM calls the tool.
// Use [New] to create a [Tool] from a typed [HandlerFunc]; the factory
// automatically infers and resolves the input and output JSON schemas via
// reflection so callers never have to write schema boilerplate.
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
