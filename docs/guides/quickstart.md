# Quick Start Guide 🧵

Welcome to Loom! This guide will help you build your first graph-based AI agent. Loom allows you to define complex AI workflows as directed graphs, making it easy to manage state, branching logic, and persistence.

## Core Concepts

Before we dive into the code, let's look at the three main building blocks of Loom:

1.  **State**: A Go struct that holds all the data for your workflow.
2.  **Nodes**: Functions that perform work and update the state.
3.  **Edges**: The connections between nodes that define the flow of execution.

## 1. Define Your State

Every Loom graph operates on a shared state. This state must implement a `Copy()` method to ensure that each step in the graph works on an independent snapshot.

```go
type AppState struct {
    Input    string `json:"input"`
    Category string `json:"category"`
    Output   string `json:"output"`
}

func (s AppState) Copy() AppState {
    return s
}
```

## 2. Initialize the Graph Builder

The `graph.Builder` is used to assemble your nodes and edges.

```go
builder := graph.New[AppState]()
```

## 3. Add Nodes

Nodes are where the logic happens. A node takes the current state and returns a `Command`, which tells the Loom engine how to update the state or where to go next.

```go
builder.AddNode("Categorize", func(ctx context.Context, state AppState) (graph.Command[AppState], error) {
    category := "general"
    if strings.Contains(strings.ToLower(state.Input), "math") {
        category = "math"
    }

    // Return an Update command to modify the state
    return graph.Update[AppState](func(s AppState) AppState {
        s.Category = category
        return s
    }), nil
})
```

## 4. Define the Workflow (Edges)

Edges connect your nodes. Loom supports direct edges and **Route Edges** for conditional branching.

```go
// Start at the Categorize node
builder.AddEdge(graph.START, "Categorize")

// Branch based on the 'Category' in the state
builder.AddRouteEdge("Categorize", func(s AppState) (string, error) {
    return s.Category, nil
}, map[string]string{
    "math":    "MathLLM",
    "general": "GeneralLLM",
})

// Both branches lead to the END node
builder.AddEdge("MathLLM", graph.END)
builder.AddEdge("GeneralLLM", graph.END)
```

## 5. Build and Execute

Once your graph is defined, you can build it and execute it with an initial input.

```go
g, _ := builder.Build()

// Execute the graph
initialState := AppState{Input: "What is 2+2?"}
snapshot, _ := g.Execute(ctx, graph.Update(func(s AppState) AppState {
    return initialState
}), nil)

fmt.Println("Final Output:", snapshot.State.Output)
```

## Next Steps

Now that you've built your first graph, check out these advanced guides:

- [Conversations](./conversations.md): Deep dive into message roles, multimodal content, and history.
- [LLM Package](./llm-package.md): Deep dive into the Model API and Registry.
- [LLM Providers](./providers/openai.md): Learn about [OpenAI](./providers/openai.md), [Anthropic](./providers/anthropic.md), [Gemini](./providers/google.md), and [Ollama](./providers/ollama.md).
- [Persistence & State](./persistence.md): Learn how to save and resume your workflows.
- [Human-in-the-Loop](./hitl.md): Patterns for human approval and input.
- [Tools & Streaming](./tools.md): Integrate your agents with external tools and real-time updates.
- [Memory Management](./memory.md): Handle long conversations with automated trimming and summarization.
- [Observability](./observability.md): Visualize your graphs and trace execution.
- [Custom Providers](./custom-providers.md): (Advanced) How to add support for new LLM backends.
