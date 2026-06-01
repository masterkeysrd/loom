package main

import (
	"context"
	"fmt"
	"time"

	"github.com/masterkeysrd/loom/graph"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/tool"
)

// AppState holds the conversation history.
type AppState struct {
	Messages []message.Message
}

func (s AppState) Copy() AppState {
	msgs := make([]message.Message, len(s.Messages))
	copy(msgs, s.Messages)
	return AppState{Messages: msgs}
}

func main() {
	// 1. Define a streaming tool with NewStreaming.
	// This tool reports progress to the UI while yielding result chunks for the LLM.
	processorTool, _ := tool.NewStreaming(
		"analyze_data",
		"Data Processor",
		"Analyzes a data set and returns a text summary and a chart.",
		func(ctx context.Context, in struct {
			DataSet string `json:"dataset"`
		}) (tool.ToolStream, error) {
			// Basic validation check for functional error example
			if in.DataSet == "" {
				return nil, tool.NewError("dataset name cannot be empty")
			}

			return func(yield func(message.ToolChunk, error) bool) {
				// Report detailed numerical progress (current/total) for a progress bar
				totalSteps := 5.0
				for i := 1.0; i <= totalSteps; i++ {
					yield(message.ToolChunk{
						Progress:        fmt.Sprintf("Step %.0f: Processing %s...", i, in.DataSet),
						ProgressCurrent: &i,
						ProgressTotal:   &totalSteps,
					}, nil)
					time.Sleep(300 * time.Millisecond) // Simulate work
				}

				// Yield the first part of the result (text)
				yield(message.ToolChunk{
					Content: message.Content{&message.TextBlock{Text: "Analysis complete. Found significant trends in " + in.DataSet}},
				}, nil)

				// Yield the second part of the result (multimodal image)
				yield(message.ToolChunk{
					Content: message.Content{&message.ImageBlock{
						Data:     []byte("fake-png-bytes"),
						MIMEType: "image/png",
					}},
				}, nil)
			}, nil
		},
	)

	container := tool.NewContainer(processorTool)

	// 2. Build a graph with an Agent node that calls the tool.
	builder := graph.New[AppState]()

	builder.AddNode("Agent", graph.NodeFunc[AppState](func(ctx context.Context, s AppState) (graph.Command[AppState], error) {
		fmt.Println("\n--- Node: Agent (Calling Tool) ---")

		// In a real app, you'd get this ToolCall from an LLM.
		call := &message.ToolCall{
			ID:   "call_abc_123",
			Name: "analyze_data",
			Args: map[string]any{"dataset": "Sales_Q4"},
		}

		// container.Call automatically aggregates the stream into a single result for the model,
		// but if we are using graph.Stream, the individual chunks are also emitted to the UI!
		resp, err := container.Call(ctx, call)
		if err != nil {
			return nil, err
		}

		return graph.Update[AppState](func(s AppState) AppState {
			s.Messages = append(s.Messages, resp)
			return s
		}), nil
	}))

	builder.AddEdge(graph.START, "Agent")
	builder.AddEdge("Agent", graph.END)

	g, _ := builder.Build()

	// 3. Execute the graph using Stream mode to observe the real-time events.
	ctx := context.Background()

	// Start with an empty update command
	input := graph.Update[AppState](func(s AppState) AppState { return s })
	events, _ := g.Stream(ctx, input, nil)

	fmt.Println("=== Loom Tool Streaming Demo ===")

	for event, err := range events {
		if err != nil {
			fmt.Printf("Execution Error: %v\n", err)
			break
		}

		switch event.Event {
		case graph.EventToolProgress:
			// These events are ephemeral and for the UI only.
			chunk := event.Data.(message.ToolChunk)
			pct := (*chunk.ProgressCurrent / *chunk.ProgressTotal) * 100
			fmt.Printf(" [UI PROGRESS] %-30s | %3.0f%% complete\n", chunk.Progress, pct)

		case graph.EventToolChunk:
			// These events are the actual data being streamed to the UI and aggregated for the LLM.
			chunk := event.Data.(message.ToolChunk)
			for _, block := range chunk.Content {
				fmt.Printf(" [UI CHUNK]    Received %T block\n", block)
			}

		case graph.EventCompleted:
			// The final snapshot contains the aggregated state.
			snap := event.Data.(graph.Snapshot[AppState])
			lastMsg := snap.State.Messages[len(snap.State.Messages)-1].(*message.Tool)

			fmt.Println("\n=== Final Aggregated Tool Result (Sent to LLM) ===")
			fmt.Printf("Tool Name:  %s\n", lastMsg.Name)
			fmt.Printf("Block Count: %d\n", len(lastMsg.Content))
			fmt.Printf("Text Result: %q\n", lastMsg.Content.Text())
			fmt.Println("==================================================")
		}
	}
}
