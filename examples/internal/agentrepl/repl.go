// Package agentrepl provides a shared interactive REPL, one-shot runner,
// `Agent -> Tools -> Agent` graph, and demo tools for Loom provider examples.
package agentrepl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/masterkeysrd/loom/graph"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/tool"
)

// AgentState holds the conversation history for the shared example graph.
type AgentState struct {
	Messages message.MessageList
}

// Copy implements [graph.State].
func (s AgentState) Copy() AgentState {
	return AgentState{
		Messages: slices.Clone(s.Messages),
	}
}

// Options configures [Run].
type Options struct {
	Title        string
	Banner       string
	SystemPrompt string
	Prompt       string // When non-empty, executes one turn and exits; otherwise starts an interactive REPL.
	Tools        []*tool.Tool
	SkipBind     bool // Skip model.BindTools(Tools...) if caller already bound tools before Run.
	Stream       bool // Stream thinking and text deltas live using model.Stream instead of model.Invoke.
	GraphName    string

	// WrapTurnCtx optionally wraps each turn's context (e.g. to start a telemetry span)
	// and returns a cleanup function invoked after the turn completes.
	WrapTurnCtx func(ctx context.Context) (context.Context, func())

	// OnCommand optionally handles custom commands entered in REPL mode (such as
	// "/compact" or "activate <tool>"). It may mutate *history directly.
	// Returning handled=true skips sending the raw line as a user message;
	// returning non-empty callOpts executes an immediate turn with those options.
	OnCommand func(line string, history *message.MessageList) (handled bool, callOpts []llm.CallOption)
}

// NewShellTool returns a safe read-only `shell_command` demo tool.
func NewShellTool(opts ...tool.Option) (*tool.Tool, error) {
	return tool.New(
		"shell_command",
		"Shell Executor",
		"Executes safe read-only shell commands: ls, pwd, date, echo, cat, uname.",
		func(ctx context.Context, in struct {
			Command string   `json:"command" jsonschema:"Safe command name: ls, pwd, date, echo, cat, uname"`
			Args    []string `json:"args,omitempty" jsonschema:"Arguments for the command"`
		}) (string, error) {
			allowed := map[string]bool{
				"ls":    true,
				"pwd":   true,
				"date":  true,
				"echo":  true,
				"cat":   true,
				"uname": true,
			}
			if !allowed[in.Command] {
				return "", fmt.Errorf("forbidden command: %q", in.Command)
			}
			out, err := exec.CommandContext(ctx, in.Command, in.Args...).CombinedOutput()
			if err != nil {
				return string(out), fmt.Errorf("command failed: %w", err)
			}
			return strings.TrimSpace(string(out)), nil
		},
		opts...,
	)
}

// Run builds an `Agent -> Tools -> Agent` graph and executes either a single
// prompt (when opts.Prompt != "") or an interactive stdin REPL.
func Run(ctx context.Context, model *llm.Model, opts Options) error {
	if model == nil {
		return errors.New("agentrepl: model cannot be nil")
	}

	container := tool.NewContainer(opts.Tools...)
	if len(opts.Tools) > 0 && !opts.SkipBind {
		model = model.BindTools(opts.Tools...)
	}

	var nextCallOpts []llm.CallOption

	builder := graph.New[AgentState]()
	builder.AddNode("Agent", graph.NodeFunc(func(ctx context.Context, state AgentState) (graph.Command[AgentState], error) {
		callOpts := nextCallOpts
		nextCallOpts = nil

		var resp *message.Assistant
		var err error
		if opts.Stream {
			resp, err = invokeStreaming(ctx, model, state.Messages, callOpts...)
		} else {
			resp, err = invokeBlocking(ctx, model, state.Messages, callOpts...)
		}
		if err != nil {
			return nil, err
		}

		return graph.Update[AgentState](func(s AgentState) AgentState {
			s.Messages = append(s.Messages, resp)
			return s
		}), nil
	}))

	builder.AddNode("Tools", graph.NodeFunc(func(ctx context.Context, state AgentState) (graph.Command[AgentState], error) {
		last := state.Messages[len(state.Messages)-1]
		var results []message.Message
		for _, tc := range last.(*message.Assistant).ToolCalls() {
			fmt.Printf("\n[Executing %s with args %v...]\n", tc.Name, tc.Args)
			res, err := container.Call(ctx, tc)
			if err != nil {
				fmt.Printf("Tool Error: %v\n", err)
				results = append(results, &message.Tool{
					ToolCallID: tc.ID,
					Name:       tc.Name,
					IsError:    true,
					Content:    message.Content{&message.TextBlock{Text: err.Error()}},
				})
				continue
			}
			for _, b := range res.Content {
				if ref, ok := b.(*message.ToolReferenceBlock); ok {
					fmt.Printf("[Discovered tool: %s]\n", ref.ToolName)
				}
			}
			if txt := res.Content.Text(); txt != "" {
				fmt.Printf("[Tool Output: %s]\n", txt)
			}
			results = append(results, res)
		}
		return graph.Update[AgentState](func(s AgentState) AgentState {
			s.Messages = append(s.Messages, results...)
			return s
		}), nil
	}))

	builder.AddEdge(graph.START, "Agent")
	builder.AddRouteEdge("Agent", func(s AgentState) (string, error) {
		last := s.Messages[len(s.Messages)-1]
		if len(last.(*message.Assistant).ToolCalls()) > 0 {
			return "Tools", nil
		}
		return graph.END, nil
	}, map[string]string{
		"Tools":   "Tools",
		graph.END: graph.END,
	})
	builder.AddEdge("Tools", "Agent")

	graphName := opts.GraphName
	if graphName == "" {
		graphName = "AgentREPL"
	}
	agentGraph, err := builder.WithName(graphName).Build()
	if err != nil {
		return fmt.Errorf("agentrepl: build graph: %w", err)
	}

	var history message.MessageList
	if opts.SystemPrompt != "" {
		history = append(history, message.NewSystemText(opts.SystemPrompt))
	}

	executeTurn := func(userMsg *message.User, callOpts []llm.CallOption) error {
		turnCtx := ctx
		var cleanup func()
		if opts.WrapTurnCtx != nil {
			turnCtx, cleanup = opts.WrapTurnCtx(ctx)
		}
		if cleanup != nil {
			defer cleanup()
		}

		nextCallOpts = callOpts
		defer func() { nextCallOpts = nil }()

		prevLen := len(history)
		if userMsg != nil {
			history = append(history, userMsg)
		}

		snap, err := agentGraph.Execute(turnCtx, graph.Update[AgentState](func(s AgentState) AgentState {
			s.Messages = slices.Clone(history)
			return s
		}), nil)
		if err != nil {
			var syncErr *llm.OutOfSyncError
			if errors.As(err, &syncErr) && len(syncErr.Messages) > 0 {
				fmt.Printf("[sync: session %s; reconciling history and retrying]\n", syncErr.Reason)
				reconciled := slices.Clone(syncErr.Messages)
				if userMsg != nil {
					reconciled = append(reconciled, userMsg)
				}
				snap, err = agentGraph.Execute(turnCtx, graph.Update[AgentState](func(s AgentState) AgentState {
					s.Messages = reconciled
					return s
				}), nil)
				if err == nil {
					history = snap.State.Messages
					return nil
				}
			}
			history = history[:prevLen]
			return err
		}
		history = snap.State.Messages
		return nil
	}

	if opts.Prompt != "" {
		return executeTurn(message.NewUserText(opts.Prompt), nil)
	}

	title := opts.Title
	if title == "" {
		title = "Loom Agent REPL"
	}
	fmt.Printf("=== %s (type 'exit' to quit) ===\n", title)
	if opts.Banner != "" {
		fmt.Println(opts.Banner)
	}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.EqualFold(input, "exit") || strings.EqualFold(input, "quit") {
			break
		}

		if opts.OnCommand != nil {
			handled, callOpts := opts.OnCommand(input, &history)
			if handled {
				if len(callOpts) > 0 {
					if err := executeTurn(nil, callOpts); err != nil {
						fmt.Printf("Error: %v\n", err)
					}
				}
				continue
			}
		}

		if err := executeTurn(message.NewUserText(input), nil); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}
	return scanner.Err()
}

func invokeBlocking(ctx context.Context, model *llm.Model, msgs message.MessageList, callOpts ...llm.CallOption) (*message.Assistant, error) {
	resp, err := model.Invoke(ctx, msgs, callOpts...)
	if err != nil {
		return nil, err
	}
	printServerToolBlocks(resp.GetContent())
	if txt := resp.Content.Text(); txt != "" {
		fmt.Printf("\nAssistant: %s\n", txt)
	}
	printMetrics(resp.DoneReason, resp.Metrics)
	return resp, nil
}

func invokeStreaming(ctx context.Context, model *llm.Model, msgs message.MessageList, callOpts ...llm.CallOption) (*message.Assistant, error) {
	streamResp, err := model.Stream(ctx, msgs, callOpts...)
	if err != nil {
		return nil, err
	}

	agg := message.NewAssistantAggregator()
	var thoughtStarted, textStarted bool
	for chunk, err := range streamResp {
		if err != nil {
			if thoughtStarted {
				fmt.Print("\033[0m\n")
			}
			return nil, err
		}
		c := chunk
		agg.Add(&c)

		printServerToolBlocks(chunk.Content)
		for _, block := range chunk.Content {
			switch b := block.(type) {
			case *message.ThinkingBlock:
				if !thoughtStarted {
					fmt.Print("\033[90m[Thinking] ")
					thoughtStarted = true
				}
				fmt.Print(b.Thinking)
			case *message.TextBlock:
				if thoughtStarted {
					fmt.Print("\033[0m\n\n")
					thoughtStarted = false
				}
				if !textStarted {
					fmt.Printf("\n[%s]:\n", model.Name())
					textStarted = true
				}
				fmt.Print(b.Text)
			}
		}
	}
	if thoughtStarted {
		fmt.Print("\033[0m\n")
	} else if textStarted {
		fmt.Println()
	}

	resp, err := agg.Build()
	if err != nil {
		return nil, err
	}
	printMetrics(resp.DoneReason, resp.Metrics)
	return resp, nil
}

func printServerToolBlocks(blocks message.Content) {
	for _, block := range blocks {
		switch b := block.(type) {
		case *message.ServerToolCall:
			fmt.Printf("\n[Server Tool Call: %s (id: %s) args: %v]\n", b.Name, b.ID, b.Args)
		case *message.ServerToolResult:
			fmt.Printf("\n[Server Tool Result: %s (id: %s) is_error: %v]\n", b.Name, b.ToolCallID, b.IsError)
			for _, sub := range b.Content {
				if ref, ok := sub.(*message.ToolReferenceBlock); ok {
					fmt.Printf("  -> [Discovered tool: %s]\n", ref.ToolName)
				}
			}
		}
	}
}

func printMetrics(doneReason string, m *message.TokenMetrics) {
	if m == nil {
		return
	}
	details := fmt.Sprintf("In=%d, Out=%d, Total=%d",
		m.Tokens.Input,
		m.Tokens.Output,
		m.TotalTokens,
	)
	if m.Tokens.Reasoning > 0 {
		details += fmt.Sprintf(", Reasoning=%d", m.Tokens.Reasoning)
	}
	if m.Tokens.CacheRead > 0 {
		details += fmt.Sprintf(", CacheRead=%d", m.Tokens.CacheRead)
	}
	if m.Tokens.CacheWrite > 0 {
		details += fmt.Sprintf(", CacheWrite=%d", m.Tokens.CacheWrite)
	}
	if doneReason != "" {
		fmt.Printf("\033[90m[Done: %s | Tokens: %s]\033[0m\n", doneReason, details)
	} else {
		fmt.Printf("\033[90m[Tokens: %s]\033[0m\n", details)
	}
}
