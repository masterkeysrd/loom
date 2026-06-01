# Memory & Context Management 🧠

As conversations with AI agents grow, they can quickly exceed the LLM's context window (token limit). Loom provides automated tools to manage this history through **Trimming** and **Summarization**.

## 1. Trimming Messages

Trimming is a simple way to keep only the most recent messages. Loom provides a flexible `Trimmer` that can handle different strategies.

```go
trimmed, _ := message.TrimMessages(ctx, history, 4000, &message.TrimConfig{
    Strategy:      message.TrimStrategyLast, // Keep the N most recent tokens
    IncludeSystem: true,                     // Always keep the system instruction
})
```

### Trimming Strategies
- **`TrimStrategyLast`**: Keeps the most recent messages that fit within the token limit.
- **`TrimStrategyFirst`**: Keeps the oldest messages (rarely used for history).

## 2. Automatic Summarization

Summarization is a more advanced technique where the agent "remembers" the gist of the conversation by condensing old messages into a summary.

### Setting up a Summarizer

```go
import "github.com/masterkeysrd/loom/memory"

summarizer, _ := memory.NewSummarizer(model, memory.SummarizerConfig{
    TokenCounter: modelProvider.(llm.TokenCounter),
    Triggers: []memory.SummarizerTrigger{
        // Summarize when the conversation exceeds 4000 tokens
        memory.TriggerSummaryOnTokenCount(4000),
    },
})
```

### Using the Summarizer in a Node

```go
func llmNode(ctx context.Context, s MyState) (graph.Command[AppState], error) {
    // 1. Summarize if a trigger is met
    newHistory, _ := summarizer.Summarize(ctx, s.Messages)
    
    // 2. Invoke the model with the condensed history
    resp, _ := model.Invoke(ctx, newHistory)

    return graph.Update(func(s MyState) MyState {
        s.Messages = append(newHistory, resp)
        return s
    }), nil
}
```

## Best Practices

- **Combine Strategies**: Use summarization for long-term "memory" and trimming as a safety net to ensure you never exceed hard limits.
- **System Message**: Always preserve your system instruction (prompt) during trimming/summarization.
- **Token Accuracy**: Use the `TokenCounter` provided by your LLM provider for the most accurate results.
