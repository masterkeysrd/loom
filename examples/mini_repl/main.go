package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/masterkeysrd/loom/llm"
	loomollama "github.com/masterkeysrd/loom/llm/ollama"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/telemetry"
	"github.com/masterkeysrd/loom/tool"
)

func main() {
	// Enable content recording to see raw messages and tool payloads in Loom Studio
	ctx := telemetry.WithContentRecording(context.Background())

	// Initialize Telemetry
	shutdown, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName: "mini-repl",
		Insecure:    true,
	})
	if err != nil {
		log.Printf("Warning: failed to initialize telemetry: %v", err)
	} else {
		defer shutdown(ctx)
	}

	// 1. Define the Shell Tool
	shellTool, _ := tool.New(
		"shell_command",
		"Shell Executor",
		"Executes safe shell commands like ls, cat, etc.",
		func(ctx context.Context, in struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}) (string, error) {
			// Basic security: only allow a subset of commands for this demo
			allowed := map[string]bool{
				"ls":   true,
				"cat":  true,
				"pwd":  true,
				"echo": true,
				"date": true,
			}
			if !allowed[in.Command] {
				return "", fmt.Errorf("command %q is not allowed in this demo", in.Command)
			}

			cmd := exec.Command(in.Command, in.Args...)
			output, err := cmd.CombinedOutput()
			if err != nil {
				return string(output), fmt.Errorf("command failed: %w", err)
			}
			return string(output), nil
		},
	)

	container := tool.NewContainer(shellTool)

	// 2. Setup Ollama Provider and Model
	p, err := loomollama.NewDefaultProvider()
	if err != nil {
		log.Fatalf("failed to create provider: %v", err)
	}

	// Use llama3.2 as it supports tool calling well
	model, err := llm.NewModel(p, "qwen3.6:35b-mlx", nil)
	if err != nil {
		log.Fatalf("failed to create model: %v", err)
	}
	model = model.BindTools(shellTool)

	// 3. REPL Loop
	scanner := bufio.NewScanner(os.Stdin)
	var messages []message.Message
	messages = append(messages, &message.System{
		Content: message.Content{&message.TextBlock{Text: "You are a helpful assistant with access to a limited shell. You can use 'ls' to see files, 'cat' to read them, and 'pwd' to see the current directory. Only use these tools if necessary."}},
	})

	fmt.Println("=== Loom Mini REPL (type 'exit' to quit) ===")
	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		input := scanner.Text()
		if strings.ToLower(input) == "exit" {
			break
		}

		messages = append(messages, &message.User{
			Content: message.Content{&message.TextBlock{Text: input}},
		})

		// Create a single span for the entire user interaction turn
		turnCtx, span := telemetry.Start(ctx, "repl-turn")

		// Invoke Model
		for {
			resp, err := model.Invoke(turnCtx, messages)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				break
			}
			messages = append(messages, resp)

			// Print content
			if text := resp.GetContent().Text(); text != "" {
				fmt.Printf("\nAssistant: %s\n", text)
			}

			// Check for tool calls
			var toolCalls []*message.ToolCall
			for _, block := range resp.GetContent() {
				if tc, ok := block.(*message.ToolCall); ok {
					toolCalls = append(toolCalls, tc)
				}
			}

			if len(toolCalls) == 0 {
				break
			}

			// Execute tool calls
			for _, tc := range toolCalls {
				fmt.Printf("[Executing %s %v...]\n", tc.Name, tc.Args)
				toolResp, err := container.Call(turnCtx, tc)
				if err != nil {
					fmt.Printf("Tool Error: %v\n", err)
					messages = append(messages, &message.Tool{
						ToolCallID: tc.ID,
						Name:       tc.Name,
						IsError:    true,
						Content:    message.Content{&message.TextBlock{Text: err.Error()}},
					})
				} else {
					messages = append(messages, toolResp)
				}
			}
			// Loop again to give the tool results back to the model
		}
		span.End()
	}
}
