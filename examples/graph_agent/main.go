package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/masterkeysrd/loom/checkpoint/sqlite"
	"github.com/masterkeysrd/loom/graph"
	"github.com/masterkeysrd/loom/llm"
	loomollama "github.com/masterkeysrd/loom/llm/ollama"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/telemetry"
	"github.com/masterkeysrd/loom/tool"
	_ "modernc.org/sqlite"
)

// AgentState holds the conversation history and status for our graph.
type AgentState struct {
	Messages message.MessageList `json:"messages"`
	NextNode string              `json:"-"` // Used for routing
}

// Copy satisfies the graph.State interface.
func (s AgentState) Copy() AgentState {
	// Deep copy messages
	msgs := make(message.MessageList, len(s.Messages))
	copy(msgs, s.Messages)
	return AgentState{
		Messages: msgs,
		NextNode: s.NextNode,
	}
}

func main() {
	ctx := telemetry.WithContentRecording(context.Background())

	// Initialize Telemetry
	shutdown, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName: "graph-agent",
		Insecure:    true,
	})
	if err != nil {
		log.Printf("Warning: failed to initialize telemetry: %v", err)
	} else {
		defer shutdown(ctx)
	}

	// 0. Setup Persistence (SQLite)
	checkpointDB, err := sql.Open("sqlite", "agent_checkpoints.db")
	if err != nil {
		log.Fatalf("failed to open checkpoint db: %v", err)
	}
	cp, err := sqlite.NewCheckpointer(checkpointDB)
	if err != nil {
		log.Fatalf("failed to create checkpointer: %v", err)
	}

	// 1. Setup Model and Tools
	p, _ := loomollama.NewDefaultProvider()
	model, _ := llm.NewModel(p, "qwen3.6:35b-mlx", nil)

	shellTool, _ := tool.New(
		"shell_command",
		"Shell Executor",
		"Executes safe shell commands: ls, cat, pwd, echo, date.",
		func(ctx context.Context, in struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}) (string, error) {
			allowed := map[string]bool{"ls": true, "cat": true, "pwd": true, "echo": true, "date": true}
			if !allowed[in.Command] {
				return "", fmt.Errorf("forbidden command: %q", in.Command)
			}
			cmd := exec.Command(in.Command, in.Args...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return string(out), fmt.Errorf("failed: %w", err)
			}
			return string(out), nil
		},
	)
	container := tool.NewContainer(shellTool)
	model = model.BindTools(shellTool)

	// 2. Define the Graph
	builder := graph.New[AgentState]()
	builder.WithCheckpointer(cp)

	// Node: Agent (LLM Call)
	builder.AddNode("Agent", graph.NodeFunc[AgentState](func(ctx context.Context, state AgentState) (graph.Command[AgentState], error) {
		fmt.Println("\n[Node: Agent] Calling LLM...")
		resp, err := model.Invoke(ctx, state.Messages)
		if err != nil {
			return nil, err
		}

		if text := resp.GetContent().Text(); text != "" {
			fmt.Printf("\nAssistant: %s\n", text)
		}

		// Determine if we should go to tools or end
		nextNode := graph.END
		for _, block := range resp.GetContent() {
			if _, ok := block.(*message.ToolCall); ok {
				nextNode = "Tools"
				break
			}
		}

		return graph.Update[AgentState](func(s AgentState) AgentState {
			s.Messages = append(s.Messages, resp)
			s.NextNode = nextNode
			return s
		}), nil
	}))

	// Node: Tools (Tool Execution)
	builder.AddNode("Tools", graph.NodeFunc[AgentState](func(ctx context.Context, state AgentState) (graph.Command[AgentState], error) {
		fmt.Println("[Node: Tools] Executing Tool Calls...")
		lastMsg := state.Messages[len(state.Messages)-1]
		var toolResults []message.Message

		for _, block := range lastMsg.GetContent() {
			if tc, ok := block.(*message.ToolCall); ok {
				fmt.Printf(" -> Executing %s %v\n", tc.Name, tc.Args)
				toolResp, err := container.Call(ctx, tc)
				if err != nil {
					toolResults = append(toolResults, &message.Tool{
						ToolCallID: tc.ID,
						Name:       tc.Name,
						IsError:    true,
						Content:    message.Content{&message.TextBlock{Text: err.Error()}},
					})
				} else {
					toolResults = append(toolResults, toolResp)
				}
			}
		}

		return graph.Update[AgentState](func(s AgentState) AgentState {
			s.Messages = append(s.Messages, toolResults...)
			return s
		}), nil
	}))

	// Define Edges
	builder.AddEdge(graph.START, "Agent")
	builder.AddRouteEdge("Agent", func(s AgentState) (string, error) {
		return s.NextNode, nil
	}, map[string]string{
		"Tools":   "Tools",
		graph.END: graph.END,
	})
	builder.AddEdge("Tools", "Agent") // Loop back to Agent

	agentGraph, _ := builder.WithName("ShellAgent").Build()

	// 3. REPL Loop
	scanner := bufio.NewScanner(os.Stdin)
	var history []message.Message
	history = append(history, &message.System{
		Content: message.Content{&message.TextBlock{Text: "You are a helpful assistant with access to a limited shell. Only use tools if needed."}},
	})

	fmt.Println("=== Loom Graph Agent REPL (type 'exit' to quit) ===")
	fmt.Println("View Graph Architecture at:", agentGraph.MermaidURL())

	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		userInput := scanner.Text()
		if strings.ToLower(userInput) == "exit" {
			break
		}

		history = append(history, &message.User{
			Content: message.Content{&message.TextBlock{Text: userInput}},
		})

		// Run the graph for this turn
		initialCmd := graph.Update[AgentState](func(s AgentState) AgentState {
			s.Messages = history
			return s
		})

		// Execute the graph. We pass a location with a thread ID for telemetry tracking.
		loc := &graph.Location{
			ThreadID: fmt.Sprintf("graph-session-%d", time.Now().Unix()),
		}
		snapshot, err := agentGraph.Execute(ctx, initialCmd, loc)

		if err != nil {
			fmt.Printf("Graph Error: %v\n", err)
			continue
		}

		// Sync history back
		history = snapshot.State.Messages
	}
}
