# Human-in-the-Loop (HITL) 🤝

Human-in-the-loop patterns allow you to pause an AI workflow and wait for human approval or additional input. This is critical for high-stakes tasks where an LLM should not act autonomously.

## 1. The Interrupt Pattern

The most basic HITL pattern involves using `graph.Interrupt()` to stop execution at a specific node.

```go
builder.AddNode("Review", func(ctx context.Context, s MyState) (graph.Command[MyState], error) {
    if s.RequiresApproval && !s.IsApproved {
        // Stop execution and save checkpoint
        return graph.Interrupt(), nil 
    }
    return graph.Update(func(s MyState) MyState { return s }), nil
})
```

## 2. Waiting for Input

When a graph is interrupted, it returns a `Snapshot`. You can then present the current state to a user (e.g., in a web UI) and wait for them to provide feedback.

```go
// 1. Initial execution stops at the Review node
snapshot, _ := g.Execute(ctx, initialInput, nil)

// ... UI waits for human button click ...

// 2. Resume with the human's input
humanInput := graph.Update(func(s MyState) MyState {
    s.IsApproved = true
    return s
})

// Resume from the previous location
nextSnapshot, _ := g.Execute(ctx, humanInput, &snapshot.Location)
```

## 3. State Forking (Time Travel)

Because Loom uses checkpoints, you can "go back in time" by resuming from an older checkpoint with different inputs. This is useful for testing different paths or correcting an agent's mistakes.

```go
// Resume from a specific historical checkpoint ID
forkLocation := &graph.Location{
    ThreadID: "session-1",
    CheckpointID: "cp-5", // ID from a previous snapshot
}

snapshot, _ := g.Execute(ctx, correctionCommand, forkLocation)
```

## 4. Breakpoints

Instead of hardcoding interrupts into your nodes, you can set dynamic breakpoints on the graph builder. (Coming soon in Roadmap).

## Summary

- **Interrupts**: Pause the graph and save state.
- **Resumption**: Provide new input and resume from a saved `Location`.
- **State Manipulation**: Use `graph.Update` during resumption to inject human decisions into the state.
