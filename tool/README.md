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
