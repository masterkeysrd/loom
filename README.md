# loom

`loom` is the AI workflow engine at the heart of TaskSmith. It lets you define multi-step agent workflows as directed graphs, connect them to any large language model, and persist execution state so interrupted runs can be resumed exactly where they left off.

---

## Packages

| Package | Description |
|---|---|
| [`graph`](graph/) | Workflow engine — nodes, edges, checkpointing, streaming |
| [`llm`](llm/) | Provider-agnostic LLM abstraction (model, registry, streaming) |
| [`llm/ollama`](llm/ollama/) | Ollama backend implementation of `llm.Provider` |
| [`message`](message/) | Conversation history primitives (roles, content blocks, JSON serialization) |
| [`checkpoint/pg`](checkpoint/pg/) | PostgreSQL-backed `graph.Checkpointer` implementation |

---

## Concepts

### Graph

A `graph.Graph[State]` is a directed network of **nodes** wired together by **edges**. Each node receives the current `State`, performs its work (an LLM call, a tool invocation, a branching decision, etc.), and returns a `Command` describing how the state should change before the next node runs.

```
START ──► think ──► act ──► END
               │
               └──► END   (conditional)
```

Graphs are built with a fluent `Builder`:

```go
g, err := graph.New[MyState]().
    WithName("planner").
    WithCheckpointer(cp).
    AddNode("think", thinkNode).
    AddNode("act", actNode).
    AddEdge(graph.START, "think").
    AddConditionalEdge("think", "act",  func(s MyState) bool { return s.ShouldAct }).
    AddConditionalEdge("think", graph.END, func(s MyState) bool { return !s.ShouldAct }).
    AddEdge("act", graph.END).
    Build()
```

### Nodes

A `Node[State]` is any type that implements `Execute`:

```go
type Node[State any] interface {
    Execute(context.Context, State) (Command[State], error)
}
```

Use `graph.NodeFunc` to define lightweight inline nodes without a named type:

```go
builder.AddNode("llm", graph.NodeFunc(func(ctx context.Context, s MyState) (graph.Command[MyState], error) {
    resp, err := model.Invoke(ctx, s.Messages)
    if err != nil {
        return nil, err
    }
    return graph.Update[MyState](func(s MyState) MyState {
        s.Messages = append(s.Messages, resp)
        return s
    }), nil
}))
```

### Commands

A `Command[State]` describes a state mutation:

| Type | Purpose |
|---|---|
| `graph.Update[State]` | Apply an arbitrary transformation to the state |
| `graph.InterrupCmd[State]` | Pause execution and save a checkpoint for later resumption |

### Checkpointing

Attach a `Checkpointer` to save a snapshot after every node transition:

```go
db, _ := sql.Open("postgres", dsn)
cp, _ := pg.NewCheckpointer(db)

g, _ := graph.New[MyState]().
    WithCheckpointer(cp).
    // ...
    Build()
```

Resume a paused graph by passing its `Location` back to `Execute` or `Stream`:

```go
snapshot, err := g.Execute(ctx, nil, &previousSnapshot.Location)
```

### Streaming

`Graph.Stream` wraps `Execute` and produces an iterator of `StreamEvent` values. LLM token chunks are surfaced as `on_llm_chunk` events in real time:

```go
events, err := g.Stream(ctx, input, loc)

for event, err := range events {
    if err != nil { ... }
    switch event.Event {
    case graph.EventLLMChunk:
        chunk := event.Data.(*message.AssistantChunk)
        // forward to HTTP client
    case "completed":
        finalState := event.Data.(MyState)
    case "interrupted":
        // save event.Data.(MyState) location for resumption
    }
}
```

---

## LLM abstraction

`llm.Model` wraps a `Provider` and a model name:

```go
provider, _ := ollama.NewDefaultProvider()
model := llm.NewModel(provider, "qwen3-coder:30b")

// Blocking
resp, err := model.Invoke(ctx, messages)

// Streaming
stream, err := model.Stream(ctx, messages)
for chunk, err := range stream { ... }
```

Implement `llm.Provider` to add a new backend (OpenAI, Google GenAI, etc.) and register it in an `llm.Registry`:

```go
registry := llm.NewRegistry()
registry.Register("ollama", provider)

p, err := registry.Get("ollama")
```

---

## Message types

The `message` package provides strongly-typed conversation history compatible with standard LLM APIs:

```go
history := message.MessageList{
    message.NewSystemText("You are a helpful assistant."),
    message.NewUserText("What is the capital of France?"),
}
```

Content is polymorphic — each message holds a `[]Block`. Currently `TextBlock` is supported; additional block kinds (images, tool calls) can be added without changing the message interfaces.

Messages serialize to a flat JSON structure idiomatic for LLM APIs and compatible with PostgreSQL JSONB columns:

```json
[
  {"role": "system",    "id": "...", "content": [{"kind": "text", "text": "You are a helpful assistant."}]},
  {"role": "user",      "id": "...", "content": [{"kind": "text", "text": "What is the capital of France?"}]},
  {"role": "assistant", "id": "...", "content": [{"kind": "text", "text": "Paris."}]}
]
```

---

## Quick start

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/masterkeysrd/tasksmith/pkg/loom/graph"
    "github.com/masterkeysrd/tasksmith/pkg/loom/llm"
    "github.com/masterkeysrd/tasksmith/pkg/loom/llm/ollama"
    "github.com/masterkeysrd/tasksmith/pkg/loom/message"
)

type State struct {
    Messages message.MessageList
}

func main() {
    ctx := context.Background()

    provider, err := ollama.NewDefaultProvider()
    if err != nil {
        log.Fatal(err)
    }
    model := llm.NewModel(provider, "qwen3-coder:30b")

    g, err := graph.New[State]().
        WithName("chat").
        AddNode("llm", graph.NodeFunc(func(ctx context.Context, s State) (graph.Command[State], error) {
            resp, err := model.Invoke(ctx, s.Messages)
            if err != nil {
                return nil, err
            }
            return graph.Update[State](func(s State) State {
                s.Messages = append(s.Messages, resp)
                return s
            }), nil
        })).
        AddEdge(graph.START, "llm").
        AddEdge("llm", graph.END).
        Build()
    if err != nil {
        log.Fatal(err)
    }

    input := graph.Update[State](func(s State) State {
        s.Messages = append(s.Messages, message.NewUserText("Hello!"))
        return s
    })

    snapshot, err := g.Execute(ctx, input, nil)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(snapshot.State.Messages)
}
```
