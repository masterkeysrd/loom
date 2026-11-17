package loomclaudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/masterkeysrd/loom/graph"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/stream"
	"github.com/masterkeysrd/loom/tool"
)

const mockCLIEnv = "LOOM_CLAUDECODE_MOCK_CLI"

func TestMain(m *testing.M) {
	if os.Getenv(mockCLIEnv) == "1" {
		runMockClaudeCLI()
		return
	}
	os.Exit(m.Run())
}

// runMockClaudeCLI simulates the `claude` CLI `stream-json` + `control` protocol
// for tests using this test binary re-executed via os.Executable().
func runMockClaudeCLI() {
	var (
		model      = "claude-sonnet-4-6"
		resumeID   string
		sysPrompt  string
		configDir  = os.Getenv("CLAUDE_CONFIG_DIR")
		logFile    = os.Getenv("MOCK_CLAUDE_LOG")
		scenario   = os.Getenv("MOCK_CLAUDE_SCENARIO")
		sessionID  = uuid.NewString()
		isResumed  bool
		historyCnt int
	)

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--model":
			if i+1 < len(args) {
				model = args[i+1]
				i++
			}
		case "--resume":
			if i+1 < len(args) {
				resumeID = args[i+1]
				isResumed = true
				i++
			}
		case "--system-prompt":
			if i+1 < len(args) {
				sysPrompt = args[i+1]
				i++
			}
		}
	}

	if resumeID != "" {
		sessionID = resumeID
	}

	transcriptDir := filepath.Join(configDir, "projects", "test-proj")
	_ = os.MkdirAll(transcriptDir, 0o700)
	transcriptPath := filepath.Join(transcriptDir, sessionID+".jsonl")

	if isResumed {
		if data, err := os.ReadFile(transcriptPath); err == nil {
			for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
				if strings.TrimSpace(line) != "" {
					historyCnt++
				}
			}
		}
	}

	appendLog := func(line string) {
		if logFile == "" {
			return
		}
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			_, _ = fmt.Fprintln(f, line)
			_ = f.Close()
		}
	}

	appendTranscript := func(entry any) {
		b, _ := json.Marshal(entry)
		f, err := os.OpenFile(transcriptPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			_, _ = f.Write(append(b, '\n'))
			_ = f.Close()
		}
	}

	var writeMu sync.Mutex
	emit := func(v any) {
		b, _ := json.Marshal(v)
		writeMu.Lock()
		_, _ = fmt.Println(string(b))
		writeMu.Unlock()
	}

	appendLog(fmt.Sprintf("SPAWN session=%s resumed=%v history=%d sys=%q", sessionID, isResumed, historyCnt, sysPrompt))

	if scenario == "fail_exit" {
		_, _ = fmt.Fprintln(os.Stderr, "mock stderr: authentication expired")
		os.Exit(2)
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var advertisedTools []string
	var pendingToolIDs = make(map[string]string) // control_request_id -> toolName

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		switch raw["type"] {
		case "control_request":
			reqID, _ := raw["request_id"].(string)
			reqObj, _ := raw["request"].(map[string]any)
			subtype, _ := reqObj["subtype"].(string)
			if subtype == "initialize" {
				// Emit system init + control_response.
				emit(map[string]any{
					"type":       "system",
					"subtype":    "init",
					"session_id": sessionID,
					"model":      model,
				})
				emit(map[string]any{
					"type": "control_response",
					"response": map[string]any{
						"subtype":    "success",
						"request_id": reqID,
						"response":   map[string]any{},
					},
				})

				// If sdkMcpServers were registered, query tools/list just like real Claude CLI.
				if servers, ok := reqObj["sdkMcpServers"].([]any); ok && len(servers) > 0 {
					emit(map[string]any{
						"type":       "control_request",
						"request_id": "mcp_init_list",
						"request": map[string]any{
							"subtype":     "mcp_message",
							"server_name": servers[0],
							"message": map[string]any{
								"jsonrpc": "2.0",
								"id":      "list_1",
								"method":  "tools/list",
							},
						},
					})
				}
			}

		case "control_response":
			respObj, _ := raw["response"].(map[string]any)
			reqID, _ := respObj["request_id"].(string)
			respBody, _ := respObj["response"].(map[string]any)
			mcpResp, _ := respBody["mcp_response"].(map[string]any)
			resultObj, _ := mcpResp["result"].(map[string]any)

			if reqID == "mcp_init_list" {
				if toolsList, ok := resultObj["tools"].([]any); ok {
					for _, t := range toolsList {
						if tm, ok := t.(map[string]any); ok {
							if n, ok := tm["name"].(string); ok {
								advertisedTools = append(advertisedTools, n)
							}
						}
					}
				}
				continue
			}

			if toolName, ok := pendingToolIDs[reqID]; ok {
				delete(pendingToolIDs, reqID)
				var toolText string
				if contentArr, ok := resultObj["content"].([]any); ok && len(contentArr) > 0 {
					if first, ok := contentArr[0].(map[string]any); ok {
						toolText, _ = first["text"].(string)
					}
				}
				appendLog(fmt.Sprintf("MCP_RESP session=%s tool=%s text=%q", sessionID, toolName, toolText))
				appendTranscript(map[string]any{"type": "tool_result", "tool": toolName, "text": toolText})

				if len(pendingToolIDs) == 0 {
					reply := fmt.Sprintf("Done after tool (%s): %s", toolName, toolText)
					emit(map[string]any{
						"type":       "stream_event",
						"session_id": sessionID,
						"event": map[string]any{
							"type":  "content_block_delta",
							"index": 0,
							"delta": map[string]any{"type": "text_delta", "text": reply},
						},
					})
					appendTranscript(map[string]any{"type": "assistant", "text": reply})
					emit(map[string]any{
						"type":        "result",
						"subtype":     "success",
						"is_error":    false,
						"session_id":  sessionID,
						"duration_ms": 42,
						"usage": map[string]any{
							"input_tokens":                10,
							"output_tokens":               20,
							"cache_read_input_tokens":     150,
							"cache_creation_input_tokens": 0,
						},
					})
				}
			}

		case "user":
			msgObj, _ := raw["message"].(map[string]any)
			prompt, _ := msgObj["content"].(string)
			appendLog(fmt.Sprintf("USER session=%s prompt=%q tools=%v", sessionID, prompt, advertisedTools))
			appendTranscript(map[string]any{"type": "user", "text": prompt})

			if scenario == "error_result" {
				emit(map[string]any{
					"type":       "result",
					"subtype":    "error_during_execution",
					"is_error":   true,
					"session_id": sessionID,
					"errors":     []string{"simulated execution failure"},
				})
				continue
			}

			if strings.Contains(prompt, "USE_PARALLEL_TOOLS") {
				emit(map[string]any{
					"type":       "stream_event",
					"session_id": sessionID,
					"event": map[string]any{
						"type":  "content_block_start",
						"index": 0,
						"content_block": map[string]any{
							"type": "tool_use",
							"id":   "toolu_p1",
							"name": "mcp__loom__add",
						},
					},
				})
				emit(map[string]any{
					"type":       "stream_event",
					"session_id": sessionID,
					"event": map[string]any{
						"type":  "content_block_delta",
						"index": 0,
						"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"a":2,"b":3}`},
					},
				})
				emit(map[string]any{
					"type":       "stream_event",
					"session_id": sessionID,
					"event": map[string]any{
						"type":  "content_block_start",
						"index": 1,
						"content_block": map[string]any{
							"type": "tool_use",
							"id":   "toolu_p2",
							"name": "mcp__loom__add",
						},
					},
				})
				emit(map[string]any{
					"type":       "stream_event",
					"session_id": sessionID,
					"event": map[string]any{
						"type":  "content_block_delta",
						"index": 1,
						"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"a":10,"b":5}`},
					},
				})
				emit(map[string]any{
					"type":       "stream_event",
					"session_id": sessionID,
					"event": map[string]any{
						"type":  "message_delta",
						"delta": map[string]any{"stop_reason": "tool_use"},
					},
				})
				pendingToolIDs["mcp_call_1"] = "add"
				pendingToolIDs["mcp_call_2"] = "add"
				emit(map[string]any{
					"type":       "control_request",
					"request_id": "mcp_call_1",
					"request": map[string]any{
						"subtype":     "mcp_message",
						"server_name": "loom",
						"message": map[string]any{
							"jsonrpc": "2.0",
							"id":      1,
							"method":  "tools/call",
							"params":  map[string]any{"name": "add", "arguments": map[string]any{"a": 2, "b": 3}},
						},
					},
				})
				emit(map[string]any{
					"type":       "control_request",
					"request_id": "mcp_call_2",
					"request": map[string]any{
						"subtype":     "mcp_message",
						"server_name": "loom",
						"message": map[string]any{
							"jsonrpc": "2.0",
							"id":      2,
							"method":  "tools/call",
							"params":  map[string]any{"name": "add", "arguments": map[string]any{"a": 10, "b": 5}},
						},
					},
				})
				continue
			}

			if strings.Contains(prompt, "USE_TOOL") {
				emit(map[string]any{
					"type":       "stream_event",
					"session_id": sessionID,
					"event": map[string]any{
						"type":  "content_block_start",
						"index": 0,
						"content_block": map[string]any{
							"type": "tool_use",
							"id":   "toolu_1",
							"name": "mcp__loom__echo_tool",
						},
					},
				})
				emit(map[string]any{
					"type":       "stream_event",
					"session_id": sessionID,
					"event": map[string]any{
						"type":  "content_block_delta",
						"index": 0,
						"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"msg":"ping"}`},
					},
				})
				emit(map[string]any{
					"type":       "stream_event",
					"session_id": sessionID,
					"event": map[string]any{
						"type":  "message_delta",
						"delta": map[string]any{"stop_reason": "tool_use"},
					},
				})
				pendingToolIDs["mcp_call_1"] = "echo_tool"
				emit(map[string]any{
					"type":       "control_request",
					"request_id": "mcp_call_1",
					"request": map[string]any{
						"subtype":     "mcp_message",
						"server_name": "loom",
						"message": map[string]any{
							"jsonrpc": "2.0",
							"id":      1,
							"method":  "tools/call",
							"params":  map[string]any{"name": "echo_tool", "arguments": map[string]any{"msg": "ping"}},
						},
					},
				})
				continue
			}

			if strings.HasPrefix(prompt, "/compact") {
				emit(map[string]any{
					"type":       "system",
					"subtype":    "status",
					"status":     "compacting",
					"session_id": sessionID,
				})
				emit(map[string]any{
					"type":       "system",
					"subtype":    "compact_boundary",
					"session_id": sessionID,
					"compactMetadata": map[string]any{
						"trigger":   "manual",
						"preTokens": 42000,
					},
				})
				summary := "compacted_summary(" + prompt + ")"
				emit(map[string]any{
					"type":       "stream_event",
					"session_id": sessionID,
					"event": map[string]any{
						"type":  "content_block_delta",
						"index": 0,
						"delta": map[string]any{"type": "text_delta", "text": summary},
					},
				})
				appendTranscript(map[string]any{"type": "compact_summary", "text": summary})
				emit(map[string]any{
					"type":       "result",
					"subtype":    "success",
					"is_error":   false,
					"session_id": sessionID,
					"usage": map[string]any{
						"input_tokens":                15,
						"output_tokens":               10,
						"cache_read_input_tokens":     0,
						"cache_creation_input_tokens": 300,
					},
				})
				continue
			}

			if strings.Contains(prompt, "AUTO_COMPACT") {
				emit(map[string]any{
					"type":       "system",
					"subtype":    "status",
					"status":     "compacting",
					"session_id": sessionID,
				})
				emit(map[string]any{
					"type":       "system",
					"subtype":    "compact_boundary",
					"session_id": sessionID,
					"compact_metadata": map[string]any{
						"trigger":    "auto",
						"pre_tokens": 155000,
					},
				})
			}

			// Normal text + thinking turn.
			emit(map[string]any{
				"type":       "stream_event",
				"session_id": sessionID,
				"event": map[string]any{
					"type":  "content_block_delta",
					"index": 0,
					"delta": map[string]any{"type": "thinking_delta", "thinking": "pondering..."},
				},
			})
			reply := fmt.Sprintf("reply(history=%d,resumed=%v): %s", historyCnt, isResumed, prompt)
			emit(map[string]any{
				"type":       "stream_event",
				"session_id": sessionID,
				"event": map[string]any{
					"type":  "content_block_delta",
					"index": 1,
					"delta": map[string]any{"type": "text_delta", "text": reply},
				},
			})
			appendTranscript(map[string]any{"type": "assistant", "text": reply})
			historyCnt += 2

			emit(map[string]any{
				"type":        "result",
				"subtype":     "success",
				"is_error":    false,
				"session_id":  sessionID,
				"duration_ms": 25,
				"usage": map[string]any{
					"input_tokens":                50,
					"output_tokens":               15,
					"cache_read_input_tokens":     100,
					"cache_creation_input_tokens": 25,
				},
			})

			if strings.Contains(prompt, "IDLE_COMPACT_AFTER") {
				go func(sid string) {
					time.Sleep(25 * time.Millisecond)
					emit(map[string]any{
						"type":       "system",
						"subtype":    "status",
						"status":     "compacting",
						"session_id": sid,
					})
					emit(map[string]any{
						"type":       "assistant",
						"session_id": sid,
						"message": map[string]any{
							"role": "assistant",
							"content": []map[string]any{
								{"type": "text", "text": "Summary of turn 1"},
							},
						},
					})
					emit(map[string]any{
						"type":       "system",
						"subtype":    "compact_boundary",
						"session_id": sid,
						"compactMetadata": map[string]any{
							"trigger":   "auto",
							"preTokens": 160000,
						},
					})
				}(sessionID)
			}
		}
	}
}

func newMockProvider(t *testing.T, extraEnv ...string) (*Provider, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	logFile := filepath.Join(t.TempDir(), "mock_cli.log")
	env := append([]string{
		mockCLIEnv + "=1",
		"MOCK_CLAUDE_LOG=" + logFile,
	}, extraEnv...)

	p, err := NewDefaultProvider(
		WithCLIPath(exe),
		WithEnv(env...),
		WithIdleTTL(30*time.Second),
		WithShutdownTimeout(time.Second),
	)
	if err != nil {
		t.Fatalf("NewDefaultProvider: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, logFile
}

func readLogLines(t *testing.T, logFile string) []string {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		return nil
	}
	raw := strings.Split(strings.TrimSpace(string(data)), "\n")
	var out []string
	for _, l := range raw {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func countPrefix(lines []string, prefix string) int {
	n := 0
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

type testStreamWriter struct {
	mu     sync.Mutex
	routes []string
	events []stream.Event
}

func (w *testStreamWriter) Write(_ context.Context, data any) error {
	if ev, ok := data.(stream.Event); ok {
		w.mu.Lock()
		w.events = append(w.events, ev)
		if ev.Name == "on_llm_request" {
			if m, ok := ev.Data.(map[string]any); ok {
				if r, ok := m["route"].(string); ok {
					w.routes = append(w.routes, r)
				}
			}
		}
		w.mu.Unlock()
	}
	return nil
}

func TestProviderStreamingThinkingAndMetrics(t *testing.T) {
	p, logFile := newMockProvider(t)
	model, err := llm.NewModel(p, "sonnet", nil)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}

	sw := &testStreamWriter{}
	ctx := stream.WithWriter(context.Background(), sw)

	msgs := message.MessageList{
		&message.System{Content: message.Content{&message.TextBlock{Text: "Sys"}}},
		&message.User{Content: message.Content{&message.TextBlock{Text: "Hello"}}},
	}
	resp1, err := model.Invoke(ctx, msgs)
	if err != nil {
		t.Fatalf("Invoke 1: %v", err)
	}
	if resp1.Content.Thought() != "pondering..." {
		t.Fatalf("thinking = %q, want pondering...", resp1.Content.Thought())
	}
	if !strings.Contains(resp1.Content.Text(), "reply(history=0,resumed=false): Hello") {
		t.Fatalf("unexpected text: %q", resp1.Content.Text())
	}
	if resp1.Metrics == nil || resp1.Metrics.Tokens.Input != 175 || resp1.Metrics.Tokens.CacheRead != 100 {
		t.Fatalf("unexpected metrics: %+v", resp1.Metrics)
	}

	// Second turn should reuse warm subprocess (Path A: warm_user) without spawning again.
	msgs = append(msgs, resp1, &message.User{Content: message.Content{&message.TextBlock{Text: "Followup"}}})
	resp2, err := model.Invoke(ctx, msgs)
	if err != nil {
		t.Fatalf("Invoke 2: %v", err)
	}
	if !strings.Contains(resp2.Content.Text(), "Followup") {
		t.Fatalf("unexpected resp2 text: %q", resp2.Content.Text())
	}

	if len(sw.routes) != 2 || sw.routes[0] != "fresh" || sw.routes[1] != "warm_user" {
		t.Fatalf("routes = %v, want [fresh warm_user]", sw.routes)
	}
	lines := readLogLines(t, logFile)
	if spawns := countPrefix(lines, "SPAWN "); spawns != 1 {
		t.Fatalf("expected 1 SPAWN across warm multi-turn, got %d (%v)", spawns, lines)
	}
}

type testAgentState struct {
	Messages message.MessageList
}

func (s testAgentState) Copy() testAgentState {
	return testAgentState{Messages: slices.Clone(s.Messages)}
}

func TestProviderGraphAgentToolLoopAndParallelTools(t *testing.T) {
	p, logFile := newMockProvider(t)
	model, err := llm.NewModel(p, "sonnet", nil)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}

	echoTool, err := tool.New(
		"echo_tool",
		"Echo",
		"Echoes a message",
		func(_ context.Context, in struct {
			Msg string `json:"msg"`
		}) (string, error) {
			return "echo:" + in.Msg, nil
		},
	)
	if err != nil {
		t.Fatalf("tool.New: %v", err)
	}
	addTool, err := tool.New(
		"add",
		"Add",
		"Adds two integers",
		func(_ context.Context, in struct {
			A int `json:"a"`
			B int `json:"b"`
		}) (string, error) {
			return fmt.Sprintf("%d", in.A+in.B), nil
		},
	)
	if err != nil {
		t.Fatalf("tool.New add: %v", err)
	}

	container := tool.NewContainer(echoTool, addTool)
	model = model.BindTools(echoTool, addTool)

	runGraph := func(prompt string) testAgentState {
		b := graph.New[testAgentState]()
		b.AddNode("Agent", graph.NodeFunc(func(ctx context.Context, st testAgentState) (graph.Command[testAgentState], error) {
			resp, err := model.Invoke(ctx, st.Messages)
			if err != nil {
				return nil, err
			}
			return graph.Update[testAgentState](func(s testAgentState) testAgentState {
				s.Messages = append(s.Messages, resp)
				return s
			}), nil
		}))
		b.AddNode("Tools", graph.NodeFunc(func(ctx context.Context, st testAgentState) (graph.Command[testAgentState], error) {
			last := st.Messages[len(st.Messages)-1]
			var results []message.Message
			for _, tc := range last.(*message.Assistant).ToolCalls() {
				res, err := container.Call(ctx, tc)
				if err != nil {
					t.Fatalf("container.Call(%s): %v", tc.Name, err)
				}
				results = append(results, res)
			}
			return graph.Update[testAgentState](func(s testAgentState) testAgentState {
				s.Messages = append(s.Messages, results...)
				return s
			}), nil
		}))
		b.AddEdge(graph.START, "Agent")
		b.AddRouteEdge("Agent", func(s testAgentState) (string, error) {
			last := s.Messages[len(s.Messages)-1]
			if len(last.(*message.Assistant).ToolCalls()) > 0 {
				return "Tools", nil
			}
			return graph.END, nil
		}, map[string]string{"Tools": "Tools", graph.END: graph.END})
		b.AddEdge("Tools", "Agent")

		g, err := b.Build()
		if err != nil {
			t.Fatalf("Build graph: %v", err)
		}
		snap, err := g.Execute(context.Background(), graph.Update[testAgentState](func(s testAgentState) testAgentState {
			s.Messages = append(s.Messages, &message.User{Content: message.Content{&message.TextBlock{Text: prompt}}})
			return s
		}), nil)
		if err != nil {
			t.Fatalf("g.Execute: %v", err)
		}
		return snap.State
	}

	// Single tool call loop.
	st1 := runGraph("please USE_TOOL")
	final1 := st1.Messages[len(st1.Messages)-1].GetContent().Text()
	if !strings.Contains(final1, "echo:ping") {
		t.Fatalf("expected tool result in final answer, got %q", final1)
	}

	// Parallel tool calls in a single turn.
	st2 := runGraph("please USE_PARALLEL_TOOLS")
	final2 := st2.Messages[len(st2.Messages)-1].GetContent().Text()
	if !strings.Contains(final2, "Done after tool") {
		t.Fatalf("expected parallel tool completion, got %q", final2)
	}

	lines := readLogLines(t, logFile)
	// Exactly 2 SPAWNs total (1 per independent graph run), proving parked tool turns did not respawn.
	if spawns := countPrefix(lines, "SPAWN "); spawns != 2 {
		t.Fatalf("expected 2 SPAWNs total across both tool loops, got %d (%v)", spawns, lines)
	}
	if mcpCount := countPrefix(lines, "MCP_RESP "); mcpCount != 3 {
		t.Fatalf("expected 3 MCP_RESP entries (1 single + 2 parallel), got %d (%v)", mcpCount, lines)
	}
}

func TestProviderSnapshotForkOnBranchAndCompactionFallback(t *testing.T) {
	p, logFile := newMockProvider(t)
	model, err := llm.NewModel(p, "sonnet", nil)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}

	sw := &testStreamWriter{}
	ctx := stream.WithWriter(context.Background(), sw)

	// Turn 1 (fresh).
	baseMsgs := message.MessageList{
		&message.User{Content: message.Content{&message.TextBlock{Text: "Turn1"}}},
	}
	a1, err := model.Invoke(ctx, baseMsgs)
	if err != nil {
		t.Fatalf("Turn 1: %v", err)
	}

	// Branch A consumes live tip (warm_user).
	branchA := append(slices.Clone(baseMsgs), a1, &message.User{Content: message.Content{&message.TextBlock{Text: "BranchA"}}})
	a2A, err := model.Invoke(ctx, branchA)
	if err != nil {
		t.Fatalf("Branch A: %v", err)
	}
	if !strings.Contains(a2A.Content.Text(), "BranchA") {
		t.Fatalf("unexpected BranchA reply: %q", a2A.Content.Text())
	}

	// Branch B forks from a1 (Path B: snapshot_fork) because Branch A already advanced the live session.
	branchB := append(slices.Clone(baseMsgs), a1, &message.User{Content: message.Content{&message.TextBlock{Text: "BranchB"}}})
	a2B, err := model.Invoke(ctx, branchB)
	if err != nil {
		t.Fatalf("Branch B: %v", err)
	}
	// Verify snapshot_fork loaded the 2-line pre-BranchA transcript and used --resume!
	if !strings.Contains(a2B.Content.Text(), "history=2,resumed=true") {
		t.Fatalf("expected resumed snapshot history=2 on BranchB, got %q", a2B.Content.Text())
	}

	// Compaction: mutate earlier message before a2B -> prefixDigest misses -> Path C fresh session.
	compacted := message.MessageList{
		&message.User{Content: message.Content{&message.TextBlock{Text: "COMPACTED_SUMMARY"}}},
		a2B,
		&message.User{Content: message.Content{&message.TextBlock{Text: "AfterCompact"}}},
	}
	a3, err := model.Invoke(ctx, compacted)
	if err != nil {
		t.Fatalf("AfterCompact: %v", err)
	}
	if !strings.Contains(a3.Content.Text(), "resumed=false") || !strings.Contains(a3.Content.Text(), "COMPACTED_SUMMARY") {
		t.Fatalf("expected fresh session with compacted history, got %q", a3.Content.Text())
	}

	wantRoutes := []string{"fresh", "warm_user", "snapshot_fork", "fresh"}
	if fmt.Sprint(sw.routes) != fmt.Sprint(wantRoutes) {
		t.Fatalf("routes = %v, want %v (logs=%v)", sw.routes, wantRoutes, readLogLines(t, logFile))
	}
}

func TestProviderIsolatedConfigDirAuthSyncAndClose(t *testing.T) {
	srcAuth := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcAuth, ".credentials.json"), []byte(`{"token":"test"}`), 0o600); err != nil {
		t.Fatalf("write creds: %v", err)
	}

	// Temporarily clear env auth vars so syncAuthCredentials runs.
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	p, err := NewDefaultProvider(
		WithCLIPath(exe),
		WithSourceAuthDir(srcAuth),
		WithEnv(mockCLIEnv+"=1"),
	)
	if err != nil {
		t.Fatalf("NewDefaultProvider: %v", err)
	}

	isolatedDir := p.proxy.configDir
	if _, err := os.Stat(filepath.Join(isolatedDir, ".credentials.json")); err != nil {
		t.Fatalf("expected synced .credentials.json in isolated dir: %v", err)
	}

	model, _ := llm.NewModel(p, "sonnet", nil)
	if _, err := model.Invoke(context.Background(), message.MessageList{
		&message.User{Content: message.Content{&message.TextBlock{Text: "hi"}}},
	}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	// SourceAuthDir must NOT have any project transcripts written into it.
	if _, err := os.Stat(filepath.Join(srcAuth, "projects")); !os.IsNotExist(err) {
		t.Fatalf("expected no projects dir in SourceAuthDir, err=%v", err)
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(isolatedDir); !os.IsNotExist(err) {
		t.Fatalf("expected owned isolated ConfigDir to be removed on Close, err=%v", err)
	}
}

func TestProviderErrorsAndProfiles(t *testing.T) {
	// Error result from CLI.
	pErr, _ := newMockProvider(t, "MOCK_CLAUDE_SCENARIO=error_result")
	mErr, _ := llm.NewModel(pErr, "sonnet", nil)
	if _, err := mErr.Invoke(context.Background(), message.MessageList{
		&message.User{Content: message.Content{&message.TextBlock{Text: "boom"}}},
	}); err == nil || !strings.Contains(err.Error(), "simulated execution failure") {
		t.Fatalf("expected simulated execution failure error, got %v", err)
	}

	// Non-zero exit with stderr surfaced.
	pExit, _ := newMockProvider(t, "MOCK_CLAUDE_SCENARIO=fail_exit")
	mExit, _ := llm.NewModel(pExit, "sonnet", nil)
	if _, err := mExit.Invoke(context.Background(), message.MessageList{
		&message.User{Content: message.Content{&message.TextBlock{Text: "boom"}}},
	}); err == nil || !strings.Contains(err.Error(), "authentication expired") {
		t.Fatalf("expected stderr in error, got %v", err)
	}

	// Profiles + ModelLister + OverrideProfile (backed by Anthropic generated catalog + aliases).
	if profs := pErr.ListProfiles(); len(profs) == 0 {
		t.Fatal("expected non-empty ListProfiles")
	}
	if sonnetProf, ok := pErr.GetProfile("sonnet"); !ok || sonnetProf.Capabilities.Temperature {
		t.Fatalf("expected sonnet alias with Temperature=false, got (%+v, %v)", sonnetProf, ok)
	}
	for _, id := range []string{"opus", "haiku", "default", "claude-sonnet-5-5", "claude-opus-5-5", "claude-fable-5-2"} {
		if _, ok := pErr.GetProfile(id); !ok {
			t.Fatalf("expected profile %q from generated Anthropic catalog", id)
		}
	}
	if found := pErr.SearchProfiles("opus"); len(found) == 0 {
		t.Fatal("expected opus search results")
	}
	custom := pErr.ListProfiles()[0]
	custom.ID = "custom-claude"
	custom.Name = "Custom Claude"
	pErr.OverrideProfile("custom-claude", custom)
	if got, err := pErr.GetModel(context.Background(), "custom-claude"); err != nil || got.Name != "Custom Claude" {
		t.Fatalf("GetModel override = (%+v, %v)", got, err)
	}
}

func TestProviderNativeCompactionAndStreamEvents(t *testing.T) {
	p, logFile := newMockProvider(t)
	model, err := llm.NewModel(p, "sonnet", nil)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}

	sw := &testStreamWriter{}
	ctx := stream.WithWriter(context.Background(), sw)

	// 1. Mid-turn auto compaction emits stream events and metadata.
	msgs := message.MessageList{
		&message.User{Content: message.Content{&message.TextBlock{Text: "Please AUTO_COMPACT"}}},
	}
	a1, err := model.Invoke(ctx, msgs)
	if err != nil {
		t.Fatalf("Turn 1 (auto compact): %v", err)
	}
	if got, _ := a1.Metadata[CompactedKey].(bool); !got {
		t.Fatalf("expected %s=true in metadata, got %v", CompactedKey, a1.Metadata)
	}
	if got, _ := a1.Metadata[CompactTriggerKey].(string); got != "auto" {
		t.Fatalf("expected %s='auto', got %q", CompactTriggerKey, got)
	}
	if got, _ := a1.Metadata[CompactPreTokensKey].(int); got != 155000 {
		t.Fatalf("expected %s=155000, got %v", CompactPreTokensKey, a1.Metadata[CompactPreTokensKey])
	}

	// 2. Manual compact-before-prompt via Session extension on the warm session.
	msgs = append(msgs, a1, &message.User{Content: message.Content{&message.TextBlock{Text: "AfterManualCompact"}}})
	a2, err := model.Invoke(ctx, msgs, llm.WithExtensionOption(Session{
		Compact:             true,
		CompactInstructions: "keep architecture decisions",
	}))
	if err != nil {
		t.Fatalf("Turn 2 (manual compact + prompt): %v", err)
	}
	if !strings.Contains(a2.Content.Text(), "AfterManualCompact") {
		t.Fatalf("expected user prompt answer after compact, got %q", a2.Content.Text())
	}
	if got, _ := a2.Metadata[CompactedKey].(bool); !got {
		t.Fatalf("expected %s=true on Turn 2, got %v", CompactedKey, a2.Metadata)
	}
	if got, _ := a2.Metadata[CompactTriggerKey].(string); got != "manual" {
		t.Fatalf("expected %s='manual', got %q", CompactTriggerKey, got)
	}

	// 3. Compact-only step (no new user delta) keeps TurnToken continuity on next turn.
	msgs = append(msgs, a2)
	a3, err := model.Invoke(ctx, msgs, llm.WithExtensionOption(Session{
		Compact: true,
	}))
	if err != nil {
		t.Fatalf("Turn 3 (compact only): %v", err)
	}
	if !strings.Contains(a3.Content.Text(), "compacted_summary(/compact)") {
		t.Fatalf("expected compact summary response, got %q", a3.Content.Text())
	}

	// 4. Subsequent normal turn still reuses the same warm session!
	msgs = append(msgs, a3, &message.User{Content: message.Content{&message.TextBlock{Text: "StillWarm"}}})
	a4, err := model.Invoke(ctx, msgs)
	if err != nil {
		t.Fatalf("Turn 4: %v", err)
	}
	if !strings.Contains(a4.Content.Text(), "StillWarm") {
		t.Fatalf("unexpected Turn 4 text: %q", a4.Content.Text())
	}

	wantRoutes := []string{"fresh", "warm_user", "warm_user", "warm_user"}
	if fmt.Sprint(sw.routes) != fmt.Sprint(wantRoutes) {
		t.Fatalf("routes = %v, want %v", sw.routes, wantRoutes)
	}

	lines := readLogLines(t, logFile)
	if spawns := countPrefix(lines, "SPAWN "); spawns != 1 {
		t.Fatalf("expected 1 SPAWN across all 4 turns + compactions, got %d (%v)", spawns, lines)
	}

	var compactingCount, compactedCount int
	for _, ev := range sw.events {
		switch ev.Name {
		case "on_llm_compacting":
			compactingCount++
		case "on_llm_compacted":
			compactedCount++
		}
	}
	if compactingCount != 3 || compactedCount != 3 {
		t.Fatalf("expected 3 on_llm_compacting and 3 on_llm_compacted events, got %d / %d", compactingCount, compactedCount)
	}
}

func newMockProviderWithOpts(t *testing.T, opts ...Option) (*Provider, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	logFile := filepath.Join(t.TempDir(), "mock_cli.log")
	baseOpts := []Option{
		WithCLIPath(exe),
		WithEnv(mockCLIEnv+"=1", "MOCK_CLAUDE_LOG="+logFile),
		WithIdleTTL(30 * time.Second),
		WithShutdownTimeout(time.Second),
	}
	baseOpts = append(baseOpts, opts...)
	p, err := NewDefaultProvider(baseOpts...)
	if err != nil {
		t.Fatalf("NewDefaultProvider: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, logFile
}

func TestProviderIdleNotificationsAndOutOfSyncReconcile(t *testing.T) {
	idleCh := make(chan message.Message, 8)
	p, logFile := newMockProviderWithOpts(t, WithSyncOnWrite(true))
	unsub := p.OnIdleMessage(func(msg message.Message) {
		idleCh <- msg
	})
	defer unsub()

	sw := &testStreamWriter{}
	ctx := stream.WithWriter(context.Background(), sw)
	model, err := llm.NewModel(p, "sonnet", nil)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}

	// Turn 1 runs and schedules idle background compaction 25ms later.
	msgs := message.MessageList{
		&message.User{Content: message.Content{&message.TextBlock{Text: "IDLE_COMPACT_AFTER"}}},
	}
	a1, err := model.Invoke(ctx, msgs)
	if err != nil {
		t.Fatalf("Turn 1: %v", err)
	}

	// Receive the two unsolicited idle messages (compacting + compacted).
	var gotIdle []message.Message
	deadline := time.After(2 * time.Second)
	for len(gotIdle) < 2 {
		select {
		case m := <-idleCh:
			gotIdle = append(gotIdle, m)
		case <-deadline:
			t.Fatalf("timed out waiting for idle messages; got %d", len(gotIdle))
		}
	}
	if gotIdle[0].Role() != message.RoleSystem {
		t.Fatalf("expected first idle message RoleSystem, got %s", gotIdle[0].Role())
	}
	ext0, ok := gotIdle[0].GetExtensions()[IdleEventExtensionID].(*IdleEventExtension)
	if !ok || ext0.Kind != "compacting" {
		t.Fatalf("expected compacting IdleEventExtension, got %+v", gotIdle[0].GetExtensions())
	}
	if gotIdle[1].Role() != message.RoleAssistant || gotIdle[1].GetContent().Text() != "Summary of turn 1" {
		t.Fatalf("unexpected second idle message: role=%s text=%q", gotIdle[1].Role(), gotIdle[1].GetContent().Text())
	}
	ext1, ok := gotIdle[1].GetExtensions()[IdleEventExtensionID].(*IdleEventExtension)
	if !ok || ext1.Kind != "compacted" || ext1.Trigger != "auto" || ext1.PreTokens != 160000 {
		t.Fatalf("unexpected compacted IdleEventExtension: %+v", gotIdle[1].GetExtensions())
	}

	// Turn 2 with stale uncompacted history -> must return *llm.OutOfSyncError before writing!
	u2 := &message.User{Content: message.Content{&message.TextBlock{Text: "NextAfterIdleCompact"}}}
	staleMsgs := append(slices.Clone(msgs), a1, u2)
	_, err = model.Invoke(ctx, staleMsgs)
	var syncErr *llm.OutOfSyncError
	if !errors.As(err, &syncErr) {
		t.Fatalf("expected *llm.OutOfSyncError, got %v", err)
	}
	if syncErr.Reason != "compacted" || len(syncErr.Messages) != 1 {
		t.Fatalf("unexpected OutOfSyncError: %+v", syncErr)
	}
	if syncErr.Messages[0].GetContent().Text() != "Summary of turn 1" {
		t.Fatalf("unexpected sync replacement text: %q", syncErr.Messages[0].GetContent().Text())
	}

	// Rearrange history using syncErr.Messages and resubmit -> succeeds on same warm CLI process!
	reconciled := append(slices.Clone(syncErr.Messages), u2)
	a2, err := model.Invoke(ctx, reconciled)
	if err != nil {
		t.Fatalf("Resubmit after reconcile: %v", err)
	}
	if !strings.Contains(a2.Content.Text(), "NextAfterIdleCompact") {
		t.Fatalf("unexpected a2 text: %q", a2.Content.Text())
	}
	if got, _ := a2.Metadata[CompactedKey].(bool); !got {
		t.Fatalf("expected %s=true on reconciled turn, got %v", CompactedKey, a2.Metadata)
	}

	// Turn 3 continues seamlessly on the same warm session.
	msgs3 := append(slices.Clone(reconciled), a2, &message.User{Content: message.Content{&message.TextBlock{Text: "Turn3"}}})
	a3, err := model.Invoke(ctx, msgs3)
	if err != nil {
		t.Fatalf("Turn 3: %v", err)
	}
	if !strings.Contains(a3.Content.Text(), "Turn3") {
		t.Fatalf("unexpected a3 text: %q", a3.Content.Text())
	}

	wantRoutes := []string{"fresh", "warm_user", "warm_user"}
	if fmt.Sprint(sw.routes) != fmt.Sprint(wantRoutes) {
		t.Fatalf("routes = %v, want %v", sw.routes, wantRoutes)
	}
	if spawns := countPrefix(readLogLines(t, logFile), "SPAWN "); spawns != 1 {
		t.Fatalf("expected 1 SPAWN across idle reconcile + Turn 3, got %d", spawns)
	}
}

func TestProviderIdlePreAppliedBeforeWrite(t *testing.T) {
	idleCh := make(chan message.Message, 8)
	p, logFile := newMockProviderWithOpts(
		t,
		WithSyncOnWrite(true),
		WithOnIdleMessage(func(msg message.Message) {
			idleCh <- msg
		}),
	)
	model, err := llm.NewModel(p, "sonnet", nil)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}

	if _, err := model.Invoke(context.Background(), message.MessageList{
		&message.User{Content: message.Content{&message.TextBlock{Text: "IDLE_COMPACT_AFTER"}}},
	}); err != nil {
		t.Fatalf("Turn 1: %v", err)
	}

	var compactedMsg message.Message
	deadline := time.After(2 * time.Second)
	for compactedMsg == nil {
		select {
		case m := <-idleCh:
			if m.Role() == message.RoleAssistant {
				compactedMsg = m
			}
		case <-deadline:
			t.Fatal("timed out waiting for idle compacted message")
		}
	}

	// Apply compactedMsg proactively before writing -> first invoke succeeds without OutOfSyncError!
	resp, err := model.Invoke(context.Background(), message.MessageList{
		compactedMsg,
		&message.User{Content: message.Content{&message.TextBlock{Text: "ProactiveWrite"}}},
	})
	if err != nil {
		t.Fatalf("Proactive write failed: %v", err)
	}
	if !strings.Contains(resp.Content.Text(), "ProactiveWrite") {
		t.Fatalf("unexpected response: %q", resp.Content.Text())
	}
	if spawns := countPrefix(readLogLines(t, logFile), "SPAWN "); spawns != 1 {
		t.Fatalf("expected 1 SPAWN, got %d", spawns)
	}
}

func TestProviderSyncOnWritePrefixMismatch(t *testing.T) {
	p, _ := newMockProviderWithOpts(t, WithSyncOnWrite(true))
	model, err := llm.NewModel(p, "sonnet", nil)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}

	a1, err := model.Invoke(context.Background(), message.MessageList{
		&message.User{Content: message.Content{&message.TextBlock{Text: "Original"}}},
	})
	if err != nil {
		t.Fatalf("Turn 1: %v", err)
	}

	// Mutate earlier user message without ForceFresh -> OutOfSyncError(prefix_mismatch).
	edited := message.MessageList{
		&message.User{Content: message.Content{&message.TextBlock{Text: "EditedOutOfBand"}}},
		a1,
		&message.User{Content: message.Content{&message.TextBlock{Text: "Next"}}},
	}
	_, err = model.Invoke(context.Background(), edited)
	var syncErr *llm.OutOfSyncError
	if !errors.As(err, &syncErr) || syncErr.Reason != "prefix_mismatch" {
		t.Fatalf("expected OutOfSyncError(prefix_mismatch), got %v", err)
	}

	// Explicit ForceFresh bypasses SyncOnWrite and opens Path C cleanly.
	a2, err := model.Invoke(context.Background(), edited, llm.WithExtensionOption(Session{ForceFresh: true}))
	if err != nil {
		t.Fatalf("ForceFresh invoke failed: %v", err)
	}
	if !strings.Contains(a2.Content.Text(), "EditedOutOfBand") {
		t.Fatalf("unexpected ForceFresh reply: %q", a2.Content.Text())
	}
}
