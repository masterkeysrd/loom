package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/masterkeysrd/loom/graph"
)

// AppState defines the data schema for our application.
// It must implement a Copy() method to satisfy the graph.State interface.
type AppState struct {
	Input    string `json:"input"`
	Category string `json:"category"`
	Output   string `json:"output"`
}

// Copy creates a deep copy of the state to ensure each graph step
// operates on an independent snapshot.
func (s AppState) Copy() AppState {
	return s
}

func main() {
	ctx := context.Background()

	// 1. Initialize the Graph Builder
	builder := graph.New[AppState]()

	// 2. Add Nodes
	// The Categorize node determines the "route" based on input content.
	builder.AddNode("Categorize", graph.NodeFunc[AppState](func(ctx context.Context, state AppState) (graph.Command[AppState], error) {
		fmt.Println("--- Node: Categorize ---")
		category := "general"
		if strings.Contains(strings.ToLower(state.Input), "math") || strings.Contains(state.Input, "+") {
			category = "math"
		}
		
		// Return an Update command to modify the state
		return graph.Update[AppState](func(s AppState) AppState {
			s.Category = category
			return s
		}), nil
	}))

	// Simulated LLM nodes
	builder.AddNode("MathLLM", graph.NodeFunc[AppState](func(ctx context.Context, state AppState) (graph.Command[AppState], error) {
		fmt.Println("--- Node: MathLLM (Simulated) ---")
		return graph.Update[AppState](func(s AppState) AppState {
			s.Output = "The simulated LLM says: I am a math expert. Numbers are fun!"
			return s
		}), nil
	}))

	builder.AddNode("GeneralLLM", graph.NodeFunc[AppState](func(ctx context.Context, state AppState) (graph.Command[AppState], error) {
		fmt.Println("--- Node: GeneralLLM (Simulated) ---")
		return graph.Update[AppState](func(s AppState) AppState {
			s.Output = "The simulated LLM says: I can help you with general questions!"
			return s
		}), nil
	}))

	// 3. Define the Workflow (Edges)
	builder.AddEdge(graph.START, "Categorize")

	// Use a RouteEdge to branch from Categorize to either Math or General nodes.
	builder.AddRouteEdge("Categorize", func(s AppState) (string, error) {
		return s.Category, nil
	}, map[string]string{
		"math":    "MathLLM",
		"general": "GeneralLLM",
	})

	// Both branches lead to the END node.
	builder.AddEdge("MathLLM", graph.END)
	builder.AddEdge("GeneralLLM", graph.END)

	// 4. Compile the Graph
	g, err := builder.Build()
	if err != nil {
		panic(err)
	}

	// 5. Visualize the Graph
	fmt.Println("\n=== GRAPH VISUALIZATION (Mermaid) ===")
	fmt.Println(g.ToMermaid())
	fmt.Println("\n=== VIEW DIAGRAM IN BROWSER ===")
	fmt.Println(g.MermaidURL())
	fmt.Println()

	// 6. Execute the Graph
	fmt.Println("=== EXECUTING GRAPH ===")
	initialInput := "Can you help me with a math problem?"
	fmt.Printf("User Input: %q\n\n", initialInput)

	// We use an initial Update command to inject the user's input into the starting state.
	inputCmd := graph.Update[AppState](func(s AppState) AppState {
		s.Input = initialInput
		return s
	})

	finalSnapshot, err := g.Execute(ctx, inputCmd, nil)
	if err != nil {
		panic(err)
	}

	fmt.Printf("\nFinal Output: %q\n", finalSnapshot.State.Output)
	fmt.Println("Workflow Complete.")
}
