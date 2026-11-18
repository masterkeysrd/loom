package loomcopilot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	"github.com/masterkeysrd/loom/graph"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/stream"
	"github.com/masterkeysrd/loom/tool"
)

const mockCLIEnv = "LOOM_COPILOT_MOCK_CLI"

func TestMain(m *testing.M) {
	if os.Getenv(mockCLIEnv) == "1" {
		runMockCopilotCLI()
		return
	}
	os.Exit(m.Run())
}

type rpcFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   any             `json:"error,omitempty"`
}

// runMockCopilotCLI simulates the Copilot CLI `--headless --stdio` JSON-RPC 2.0
// server using Content-Length LSP framing for deterministic race tests.
func runMockCopilotCLI() {
	logFile := os.Getenv("MOCK_COPILOT_LOG")
	scenario := os.Getenv("MOCK_COPILOT_SCENARIO")

	var writeMu sync.Mutex
	appendLog := func(line string) {
		if logFile == "" {
			return
		}
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = fmt.Fprintln(f, line)
	}

	writeFrame := func(payload any) {
		data, err := json.Marshal(payload)
		if err != nil {
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(data), data)
	}

	emitSessionEvent := func(sessionID, evType string, data any) {
		writeFrame(map[string]any{
			"jsonrpc": "2.0",
			"method":  "session.event",
			"params": map[string]any{
				"sessionId": sessionID,
				"event": map[string]any{
					"id":        uuid.NewString(),
					"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
					"type":      evType,
					"data":      data,
				},
			},
		})
	}

	type mockSessionState struct {
		id             string
		model          string
		turns          int
		pendingToolsMu sync.Mutex
		pendingTools   map[string]chan string
		pendingPermMu  sync.Mutex
		pendingPermCh  map[string]chan string
	}

	var sessMu sync.Mutex
	sessions := make(map[string]*mockSessionState)
	var seq atomic.Int64

	reader := bufio.NewReader(os.Stdin)
	for {
		var contentLen int
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				break
			}
			if after, ok := strings.CutPrefix(trimmed, "Content-Length:"); ok {
				n, _ := strconv.Atoi(strings.TrimSpace(after))
				contentLen = n
			}
		}
		if contentLen <= 0 {
			continue
		}
		body := make([]byte, contentLen)
		if _, err := io.ReadFull(reader, body); err != nil {
			return
		}

		var req rpcFrame
		if err := json.Unmarshal(body, &req); err != nil {
			continue
		}

		switch req.Method {
		case "connect":
			writeFrame(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": 3,
				},
			})

		case "ping":
			writeFrame(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"message":         "pong",
					"timestamp":       time.Now().UnixMilli(),
					"protocolVersion": 3,
				},
			})

		case "models.list":
			writeFrame(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"models": []map[string]any{
						{
							"id":   "gpt-5",
							"name": "GPT-5 (Copilot)",
							"capabilities": map[string]any{
								"supports": map[string]any{"vision": true, "reasoningEffort": true},
								"limits":   map[string]any{"max_context_window_tokens": 128000, "max_output_tokens": 16384},
							},
							"supportedReasoningEfforts": []string{"low", "medium", "high"},
							"defaultReasoningEffort":    "medium",
						},
					},
				},
			})

		case "account.getQuota":
			writeFrame(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"quotaSnapshots": map[string]any{
						"premium_interactions": map[string]any{
							"entitlementRequests":    300,
							"isUnlimitedEntitlement": false,
							"remainingPercentage":    75.0,
							"usedRequests":           75,
						},
						"chat": map[string]any{
							"entitlementRequests":    -1,
							"isUnlimitedEntitlement": true,
							"remainingPercentage":    0,
							"usedRequests":           12,
						},
					},
				},
			})

		case "session.create":
			var p struct {
				SessionID string `json:"sessionId"`
				Model     string `json:"model"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.SessionID == "" {
				p.SessionID = uuid.NewString()
			}
			appendLog("CREATE:" + string(req.Params))
			sessMu.Lock()
			sessions[p.SessionID] = &mockSessionState{
				id:            p.SessionID,
				model:         p.Model,
				pendingTools:  make(map[string]chan string),
				pendingPermCh: make(map[string]chan string),
			}
			sessMu.Unlock()

			writeFrame(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"sessionId":    p.SessionID,
					"capabilities": map[string]any{},
				},
			})

		case "session.detach":
			writeFrame(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"success": true},
			})

		case "session.abort":
			writeFrame(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{},
			})

		case "runtime.shutdown":
			writeFrame(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{},
			})
			return

		case "session.history.compact":
			var p struct {
				SessionID          string  `json:"sessionId"`
				CustomInstructions *string `json:"customInstructions"`
			}
			_ = json.Unmarshal(req.Params, &p)
			focus := ""
			if p.CustomInstructions != nil {
				focus = *p.CustomInstructions
			}
			appendLog("COMPACT:" + p.SessionID + ":" + focus)
			emitSessionEvent(p.SessionID, "session.compaction_start", map[string]any{})
			summary := "Compacted summary"
			if focus != "" {
				summary += " (" + focus + ")"
			}
			emitSessionEvent(p.SessionID, "session.compaction_complete", map[string]any{
				"success":         true,
				"tokensRemoved":   450,
				"messagesRemoved": 6,
				"summaryContent":  summary,
			})
			writeFrame(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"success":         true,
					"tokensRemoved":   450,
					"messagesRemoved": 6,
					"summaryContent":  summary,
				},
			})

		case "session.tools.handlePendingToolCall":
			var p struct {
				SessionID string `json:"sessionId"`
				RequestID string `json:"requestId"`
				Result    *struct {
					TextResultForLlm string `json:"textResultForLlm"`
					Error            string `json:"error"`
				} `json:"result"`
				Error *string `json:"error"`
			}
			_ = json.Unmarshal(req.Params, &p)
			sessMu.Lock()
			st := sessions[p.SessionID]
			sessMu.Unlock()

			out := ""
			if p.Error != nil {
				out = "ERR:" + *p.Error
			} else if p.Result != nil {
				out = p.Result.TextResultForLlm
			}
			if st != nil {
				st.pendingToolsMu.Lock()
				ch := st.pendingTools[p.RequestID]
				delete(st.pendingTools, p.RequestID)
				st.pendingToolsMu.Unlock()
				if ch != nil {
					ch <- out
				}
			}
			writeFrame(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"success": true},
			})

		case "session.permissions.handlePendingPermissionRequest":
			var p struct {
				SessionID string `json:"sessionId"`
				RequestID string `json:"requestId"`
				Result    struct {
					Kind string `json:"kind"`
				} `json:"result"`
			}
			_ = json.Unmarshal(req.Params, &p)
			appendLog("PERM_DECISION:" + p.Result.Kind)
			sessMu.Lock()
			st := sessions[p.SessionID]
			sessMu.Unlock()
			if st != nil {
				st.pendingPermMu.Lock()
				ch := st.pendingPermCh[p.RequestID]
				delete(st.pendingPermCh, p.RequestID)
				st.pendingPermMu.Unlock()
				if ch != nil {
					ch <- p.Result.Kind
				}
			}
			writeFrame(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"success": true},
			})

		case "session.send":
			var p struct {
				SessionID      string            `json:"sessionId"`
				Prompt         string            `json:"prompt"`
				Attachments    []json.RawMessage `json:"attachments"`
				ResponseFormat any               `json:"responseFormat"`
			}
			_ = json.Unmarshal(req.Params, &p)
			appendLog("SEND:" + p.SessionID + ":" + p.Prompt)
			if len(p.Attachments) > 0 {
				attBytes, _ := json.Marshal(p.Attachments)
				appendLog("ATTACHMENTS:" + string(attBytes))
			}
			if p.ResponseFormat != nil {
				rfBytes, _ := json.Marshal(p.ResponseFormat)
				appendLog("RESPONSE_FORMAT:" + string(rfBytes))
			}

			msgID := fmt.Sprintf("msg-%d", seq.Add(1))
			writeFrame(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"messageId": msgID},
			})

			sessMu.Lock()
			st := sessions[p.SessionID]
			sessMu.Unlock()
			if st == nil {
				continue
			}

			st.turns++
			turnNum := st.turns

			go func(st *mockSessionState, turnNum int, prompt string) {
				sid := st.id
				switch scenario {
				case "tool_loop":
					if turnNum == 1 {
						reqID := fmt.Sprintf("req-%d", seq.Add(1))
						ch := make(chan string, 1)
						st.pendingToolsMu.Lock()
						st.pendingTools[reqID] = ch
						st.pendingToolsMu.Unlock()

						emitSessionEvent(sid, "assistant.reasoning_delta", map[string]any{
							"reasoningId":  "r1",
							"deltaContent": "Checking directory. ",
						})
						emitSessionEvent(sid, "assistant.message", map[string]any{
							"messageId": "m1",
							"content":   "",
							"toolRequests": []map[string]any{
								{
									"toolCallId": "call_1",
									"name":       "echo_tool",
									"arguments":  map[string]any{"text": "hello-from-tool"},
								},
							},
						})
						emitSessionEvent(sid, "assistant.usage", map[string]any{
							"model":           st.model,
							"inputTokens":     100,
							"outputTokens":    25,
							"cacheReadTokens": 40,
							"apiEndpoint":     "/responses",
						})
						emitSessionEvent(sid, "external_tool.requested", map[string]any{
							"requestId":  reqID,
							"sessionId":  sid,
							"toolCallId": "call_1",
							"toolName":   "echo_tool",
							"arguments":  map[string]any{"text": "hello-from-tool"},
						})

						toolOut := <-ch
						emitSessionEvent(sid, "external_tool.completed", map[string]any{
							"requestId": reqID,
						})
						reply := fmt.Sprintf("Tool returned: %s", toolOut)
						emitSessionEvent(sid, "assistant.message_delta", map[string]any{
							"messageId":    "m2",
							"deltaContent": reply,
						})
						emitSessionEvent(sid, "assistant.message", map[string]any{
							"messageId": "m2",
							"content":   reply,
						})
						emitSessionEvent(sid, "assistant.usage", map[string]any{
							"model":           st.model,
							"inputTokens":     150,
							"outputTokens":    18,
							"cacheReadTokens": 120,
							"apiEndpoint":     "/responses",
						})
						emitSessionEvent(sid, "session.idle", map[string]any{})
						return
					}

					reply := fmt.Sprintf("Turn %d ok for %s", turnNum, prompt)
					emitSessionEvent(sid, "assistant.message_delta", map[string]any{
						"messageId":    "m-next",
						"deltaContent": reply,
					})
					emitSessionEvent(sid, "assistant.message", map[string]any{
						"messageId": "m-next",
						"content":   reply,
					})
					emitSessionEvent(sid, "session.idle", map[string]any{})

				case "parallel_tools":
					req1 := fmt.Sprintf("req-%d", seq.Add(1))
					req2 := fmt.Sprintf("req-%d", seq.Add(1))
					ch1 := make(chan string, 1)
					ch2 := make(chan string, 1)
					st.pendingToolsMu.Lock()
					st.pendingTools[req1] = ch1
					st.pendingTools[req2] = ch2
					st.pendingToolsMu.Unlock()

					emitSessionEvent(sid, "assistant.message", map[string]any{
						"messageId": "m-par",
						"content":   "",
						"toolRequests": []map[string]any{
							{"toolCallId": "c1", "name": "echo_tool", "arguments": map[string]any{"text": "A"}},
							{"toolCallId": "c2", "name": "echo_tool", "arguments": map[string]any{"text": "B"}},
						},
					})
					emitSessionEvent(sid, "external_tool.requested", map[string]any{
						"requestId":  req1,
						"sessionId":  sid,
						"toolCallId": "c1",
						"toolName":   "echo_tool",
						"arguments":  map[string]any{"text": "A"},
					})
					emitSessionEvent(sid, "external_tool.requested", map[string]any{
						"requestId":  req2,
						"sessionId":  sid,
						"toolCallId": "c2",
						"toolName":   "echo_tool",
						"arguments":  map[string]any{"text": "B"},
					})
					o1 := <-ch1
					o2 := <-ch2
					reply := fmt.Sprintf("Both: %s & %s", o1, o2)
					emitSessionEvent(sid, "assistant.message_delta", map[string]any{
						"messageId":    "m-par-end",
						"deltaContent": reply,
					})
					emitSessionEvent(sid, "assistant.message", map[string]any{
						"messageId": "m-par-end",
						"content":   reply,
					})
					emitSessionEvent(sid, "session.idle", map[string]any{})

				case "permission_check":
					pReqID := fmt.Sprintf("perm-%d", seq.Add(1))
					pch := make(chan string, 1)
					st.pendingPermMu.Lock()
					st.pendingPermCh[pReqID] = pch
					st.pendingPermMu.Unlock()

					emitSessionEvent(sid, "permission.requested", map[string]any{
						"requestId": pReqID,
						"permissionRequest": map[string]any{
							"kind":                    "shell",
							"canOfferSessionApproval": false,
							"commands":                []map[string]any{{"identifier": "ls", "readOnly": true}},
							"fullCommandText":         "ls",
							"hasWriteFileRedirection": false,
							"intention":               "list files",
						},
					})
					decision := <-pch
					reply := "perm=" + decision
					emitSessionEvent(sid, "assistant.message_delta", map[string]any{
						"messageId":    "m-perm",
						"deltaContent": reply,
					})
					emitSessionEvent(sid, "assistant.message", map[string]any{
						"messageId": "m-perm",
						"content":   reply,
					})
					emitSessionEvent(sid, "session.idle", map[string]any{})

				case "idle_compaction":
					reply := fmt.Sprintf("Turn %d: %s", turnNum, prompt)
					emitSessionEvent(sid, "assistant.message_delta", map[string]any{
						"messageId":    fmt.Sprintf("m-%d", turnNum),
						"deltaContent": reply,
					})
					emitSessionEvent(sid, "assistant.message", map[string]any{
						"messageId": fmt.Sprintf("m-%d", turnNum),
						"content":   reply,
					})
					emitSessionEvent(sid, "session.idle", map[string]any{})
					if turnNum == 1 {
						time.Sleep(80 * time.Millisecond)
						emitSessionEvent(sid, "session.compaction_start", map[string]any{})
						emitSessionEvent(sid, "session.compaction_complete", map[string]any{
							"success":         true,
							"tokensRemoved":   900,
							"messagesRemoved": 12,
							"summaryContent":  "Background compacted summary",
						})
					}

				case "idle_shutdown":
					reply := "hello before shutdown"
					emitSessionEvent(sid, "assistant.message_delta", map[string]any{
						"messageId":    "m-shut",
						"deltaContent": reply,
					})
					emitSessionEvent(sid, "assistant.message", map[string]any{
						"messageId": "m-shut",
						"content":   reply,
					})
					emitSessionEvent(sid, "session.idle", map[string]any{})
					if turnNum == 1 {
						time.Sleep(80 * time.Millisecond)
						emitSessionEvent(sid, "session.shutdown", map[string]any{
							"shutdownType": "routine",
						})
					}

				default:
					emitSessionEvent(sid, "assistant.reasoning_delta", map[string]any{
						"reasoningId":  "r1",
						"deltaContent": "Thinking...",
					})
					reply := fmt.Sprintf("Mock reply %d: %s", turnNum, prompt)
					emitSessionEvent(sid, "assistant.message_delta", map[string]any{
						"messageId":    fmt.Sprintf("m-%d", turnNum),
						"deltaContent": reply,
					})
					emitSessionEvent(sid, "assistant.message", map[string]any{
						"messageId": fmt.Sprintf("m-%d", turnNum),
						"content":   reply,
					})
					emitSessionEvent(sid, "assistant.usage", map[string]any{
						"model":            st.model,
						"inputTokens":      200,
						"outputTokens":     30,
						"cacheReadTokens":  50,
						"cacheWriteTokens": 20,
						"reasoningTokens":  10,
						"apiEndpoint":      "/v1/messages",
						"duration":         150,
					})
					emitSessionEvent(sid, "session.idle", map[string]any{})
				}
			}(st, turnNum, p.Prompt)

		default:
			if len(req.ID) > 0 {
				writeFrame(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{},
				})
			}
		}
	}
}

func newTestProvider(t *testing.T, scenario string, opts ...Option) (*Provider, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "mock-copilot.log")
	baseOpts := []Option{
		WithCLIPath(exe),
		WithEnv(
			mockCLIEnv+"=1",
			"MOCK_COPILOT_LOG="+logPath,
			"MOCK_COPILOT_SCENARIO="+scenario,
		),
	}
	baseOpts = append(baseOpts, opts...)
	p, err := NewDefaultProvider(baseOpts...)
	if err != nil {
		t.Fatalf("NewDefaultProvider: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, logPath
}

func readMockLog(t *testing.T, logPath string) string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	return string(data)
}

func countLogPrefix(log, prefix string) int {
	n := 0
	for line := range strings.SplitSeq(log, "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

func TestCopilotStreamingAndMetrics(t *testing.T) {
	p, logPath := newTestProvider(t, "basic",
		WithBYOKProvider(&copilot.ProviderConfig{
			Type:    "openai",
			BaseURL: "http://localhost:11434/v1",
		}),
	)

	model, err := llm.NewModel(p, "gpt-5", nil)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	model = model.WithThinkingEffort("high")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var textChunks []string
	var thinkChunks []string
	var finalChunk message.AssistantChunk

	seq, err := model.Stream(ctx, []message.Message{
		message.NewSystem(&message.TextBlock{Text: "Be concise."}),
		message.NewUser(&message.TextBlock{Text: "Hello Copilot"}),
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for ch, serr := range seq {
		if serr != nil {
			t.Fatalf("stream err: %v", serr)
		}
		for _, blk := range ch.Content {
			switch b := blk.(type) {
			case *message.TextBlock:
				textChunks = append(textChunks, b.Text)
			case *message.ThinkingBlock:
				thinkChunks = append(thinkChunks, b.Thinking)
			}
		}
		if ch.Done {
			finalChunk = ch
		}
	}

	if got := strings.Join(thinkChunks, ""); got != "Thinking..." {
		t.Fatalf("unexpected thinking: %q", got)
	}
	if got := strings.Join(textChunks, ""); got != "Mock reply 1: Hello Copilot" {
		t.Fatalf("unexpected text: %q", got)
	}
	if finalChunk.Metrics == nil {
		t.Fatal("expected metrics on final chunk")
	}
	// v1_messages adds cacheRead + cacheWrite into Input (200 + 50 + 20 = 270).
	if finalChunk.Metrics.Tokens.Input != 270 || finalChunk.Metrics.Tokens.Output != 30 {
		t.Fatalf("unexpected token metrics: %+v", finalChunk.Metrics.Tokens)
	}
	if finalChunk.Metadata[SessionIDKey] == "" || finalChunk.Metadata[TurnTokenKey] == "" {
		t.Fatalf("missing session metadata: %+v", finalChunk.Metadata)
	}

	log := readMockLog(t, logPath)
	if !strings.Contains(log, `"reasoningEffort":"high"`) {
		t.Fatalf("expected reasoningEffort=high in CREATE log: %s", log)
	}
	if !strings.Contains(log, `"baseUrl":"http://localhost:11434/v1"`) {
		t.Fatalf("expected BYOK baseUrl in CREATE log: %s", log)
	}
	if !strings.Contains(log, `"mode":"replace"`) || !strings.Contains(log, `"content":"Be concise."`) {
		t.Fatalf("expected SystemMessage replace in CREATE log: %s", log)
	}
}

type testAgentState struct {
	Messages message.MessageList
}

func (s testAgentState) Copy() testAgentState {
	return testAgentState{Messages: slices.Clone(s.Messages)}
}

type testStreamWriter struct {
	mu     sync.Mutex
	events []string
}

func (w *testStreamWriter) Write(_ context.Context, data any) error {
	if ev, ok := data.(stream.Event); ok {
		w.mu.Lock()
		w.events = append(w.events, ev.Name)
		w.mu.Unlock()
	}
	return nil
}

func TestCopilotAgentToolLoopWarmReuseAndParallelTools(t *testing.T) {
	t.Run("single parked tool + warm follow-up", func(t *testing.T) {
		p, logPath := newTestProvider(t, "tool_loop")
		model, err := llm.NewModel(p, "gpt-5", nil)
		if err != nil {
			t.Fatalf("NewModel: %v", err)
		}

		echoTool, err := tool.New("echo_tool", "Echo", "Echo text",
			func(_ context.Context, in struct {
				Text string `json:"text"`
			}) (string, error) {
				return "ECHO:" + in.Text, nil
			})
		if err != nil {
			t.Fatalf("tool.New: %v", err)
		}
		model = model.BindTools(echoTool)

		b := graph.New[testAgentState]()
		container := tool.NewContainer(echoTool)
		b.AddNode("agent", graph.NodeFunc(func(ctx context.Context, s testAgentState) (graph.Command[testAgentState], error) {
			resp, invErr := model.Invoke(ctx, s.Messages)
			if invErr != nil {
				return nil, invErr
			}
			return graph.Update[testAgentState](func(cur testAgentState) testAgentState {
				cur.Messages = append(cur.Messages, resp)
				return cur
			}), nil
		}))
		b.AddNode("tools", graph.NodeFunc(func(ctx context.Context, s testAgentState) (graph.Command[testAgentState], error) {
			last := s.Messages[len(s.Messages)-1].(*message.Assistant)
			var results []message.Message
			for _, tc := range last.ToolCalls() {
				res, cerr := container.Call(ctx, tc)
				if cerr != nil {
					return nil, cerr
				}
				results = append(results, res)
			}
			return graph.Update[testAgentState](func(cur testAgentState) testAgentState {
				cur.Messages = append(cur.Messages, results...)
				return cur
			}), nil
		}))
		b.AddEdge(graph.START, "agent")
		b.AddRouteEdge("agent", func(s testAgentState) (string, error) {
			last := s.Messages[len(s.Messages)-1].(*message.Assistant)
			if len(last.ToolCalls()) > 0 {
				return "tools", nil
			}
			return graph.END, nil
		}, map[string]string{"tools": "tools", graph.END: graph.END})
		b.AddEdge("tools", "agent")

		g, err := b.Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		snap, err := g.Execute(ctx, graph.Update[testAgentState](func(s testAgentState) testAgentState {
			s.Messages = message.MessageList{message.NewUser(&message.TextBlock{Text: "Run echo"})}
			return s
		}), nil)
		if err != nil {
			t.Fatalf("Execute 1: %v", err)
		}

		final := snap.State.Messages[len(snap.State.Messages)-1].(*message.Assistant)
		if !strings.Contains(final.Content.Text(), "ECHO:hello-from-tool") {
			t.Fatalf("unexpected final response: %q", final.Content.Text())
		}

		// Turn 2 on the same history must reuse the warm session (no second CREATE).
		nextMsgs := append(slices.Clone(snap.State.Messages), message.NewUser(&message.TextBlock{Text: "Follow up"}))
		resp2, err := model.Invoke(ctx, nextMsgs)
		if err != nil {
			t.Fatalf("Invoke 2: %v", err)
		}
		if resp2.Content.Text() != "Turn 2 ok for Follow up" {
			t.Fatalf("unexpected warm follow-up reply: %q", resp2.Content.Text())
		}

		log := readMockLog(t, logPath)
		if cnt := countLogPrefix(log, "CREATE:"); cnt != 1 {
			t.Fatalf("expected exactly 1 session.create across parked tool loop + warm turn, got %d:\n%s", cnt, log)
		}
	})

	t.Run("parallel parked tools", func(t *testing.T) {
		p, _ := newTestProvider(t, "parallel_tools")
		model, err := llm.NewModel(p, "gpt-5", nil)
		if err != nil {
			t.Fatalf("NewModel: %v", err)
		}
		echoTool, err := tool.New("echo_tool", "Echo", "Echo text",
			func(_ context.Context, in struct {
				Text string `json:"text"`
			}) (string, error) {
				return "OUT:" + in.Text, nil
			})
		if err != nil {
			t.Fatalf("tool.New: %v", err)
		}
		model = model.BindTools(echoTool)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		msgs := message.MessageList{message.NewUser(&message.TextBlock{Text: "Call both"})}
		resp1, err := model.Invoke(ctx, msgs)
		if err != nil {
			t.Fatalf("Invoke 1: %v", err)
		}
		calls := resp1.ToolCalls()
		if len(calls) != 2 {
			t.Fatalf("expected 2 parallel tool calls, got %d", len(calls))
		}

		container := tool.NewContainer(echoTool)
		msgs = append(msgs, resp1)
		for _, tc := range calls {
			tm, cerr := container.Call(ctx, tc)
			if cerr != nil {
				t.Fatalf("Call: %v", cerr)
			}
			msgs = append(msgs, tm)
		}

		resp2, err := model.Invoke(ctx, msgs)
		if err != nil {
			t.Fatalf("Invoke 2: %v", err)
		}
		if !strings.Contains(resp2.Content.Text(), "OUT:A") || !strings.Contains(resp2.Content.Text(), "OUT:B") {
			t.Fatalf("unexpected parallel result: %q", resp2.Content.Text())
		}
	})
}

func TestCopilotPermissionModes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("default denies built-in permission", func(t *testing.T) {
		p, logPath := newTestProvider(t, "permission_check")
		model, _ := llm.NewModel(p, "gpt-5", nil)
		resp, err := model.Invoke(ctx, []message.Message{message.NewUser(&message.TextBlock{Text: "go"})})
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if resp.Content.Text() != "perm=reject" {
			t.Fatalf("unexpected response: %q", resp.Content.Text())
		}
		log := readMockLog(t, logPath)
		if !strings.Contains(log, "PERM_DECISION:reject") {
			t.Fatalf("expected denied permission in log: %s", log)
		}
	})

	t.Run("allow all approves permission", func(t *testing.T) {
		p, _ := newTestProvider(t, "permission_check", WithAllowAllPermissions(true))
		model, _ := llm.NewModel(p, "gpt-5", nil)
		resp, err := model.Invoke(ctx, []message.Message{message.NewUser(&message.TextBlock{Text: "go"})})
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if resp.Content.Text() != "perm=approve-once" {
			t.Fatalf("unexpected response: %q", resp.Content.Text())
		}
	})
}

func TestCopilotModelListerQuotaAndResponseSchema(t *testing.T) {
	p, logPath := newTestProvider(t, "basic")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if len(p.ListProfiles()) != 0 {
		t.Fatalf("expected empty ListProfiles before discovery, got %+v", p.ListProfiles())
	}
	if _, ok := p.GetProfile("gpt-5.4"); ok {
		t.Fatal("expected static catalog model gpt-5.4 not exposed before discovery")
	}

	models, err := p.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gpt-5" {
		t.Fatalf("unexpected models: %+v", models)
	}

	gotModel, err := p.GetModel(ctx, "gpt-5")
	if err != nil || gotModel.Limits.Context != 128000 {
		t.Fatalf("unexpected GetModel: %+v, err=%v", gotModel, err)
	}

	snap, err := p.GetQuotaSnapshot(ctx)
	if err != nil {
		t.Fatalf("GetQuotaSnapshot: %v", err)
	}
	prem := snap.Buckets["premium_interactions"]
	if prem.RemainingFraction != 0.75 || prem.Limit != 300 || prem.Used != 75 || prem.Unit != llm.QuotaUnitRequests {
		t.Fatalf("unexpected premium quota bucket: %+v", prem)
	}
	chat := snap.Buckets["chat"]
	if chat.RemainingFraction != 1.0 || !chat.Unlimited {
		t.Fatalf("expected unlimited chat quota=1.0, got %+v", chat)
	}

	buckets, ok, err := p.GetQuota(ctx, "gpt-5")
	if err != nil || !ok {
		t.Fatalf("unexpected GetQuota(gpt-5): %+v ok=%v err=%v", buckets, ok, err)
	}
	bottleneck, ok := buckets.Bottleneck()
	if !ok || bottleneck.RemainingFraction != 0.75 || bottleneck.ID != "premium_interactions" {
		t.Fatalf("unexpected GetQuota(gpt-5) bottleneck: %+v ok=%v", bottleneck, ok)
	}

	model, _ := llm.NewModel(p, "gpt-5", nil)
	schema := &jsonschema.Schema{Type: "object"}
	model = model.WithStructuredOutput(schema)
	_, err = model.Invoke(ctx, []message.Message{message.NewUser(&message.TextBlock{Text: "json"})})
	if err != nil {
		t.Fatalf("Invoke with schema: %v", err)
	}
	log := readMockLog(t, logPath)
	if !strings.Contains(log, `"type":"json_schema"`) || !strings.Contains(log, `"schema":{"type":"object"}`) {
		t.Fatalf("expected json_schema ResponseFormat in log: %s", log)
	}
}

func TestCopilotManualCompactAndIdleSyncOnWrite(t *testing.T) {
	t.Run("manual Session.Compact", func(t *testing.T) {
		p, logPath := newTestProvider(t, "basic")
		model, _ := llm.NewModel(p, "gpt-5", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		sw := &testStreamWriter{}
		sctx := stream.WithWriter(ctx, sw)

		r1, err := model.Invoke(sctx, []message.Message{message.NewUser(&message.TextBlock{Text: "first"})})
		if err != nil {
			t.Fatalf("Invoke 1: %v", err)
		}

		// Compaction-only turn.
		r2, err := model.Invoke(sctx, []message.Message{
			message.NewUser(&message.TextBlock{Text: "first"}),
			r1,
		}, llm.WithExtensionOption(Session{
			Compact:             true,
			CompactInstructions: "keep decisions",
		}))
		if err != nil {
			t.Fatalf("Compact Invoke: %v", err)
		}
		if r2.DoneReason != "compacted" || r2.Metadata[CompactedKey] != true {
			t.Fatalf("expected compacted metadata, got done=%q meta=%+v", r2.DoneReason, r2.Metadata)
		}
		sw.mu.Lock()
		events := slices.Clone(sw.events)
		sw.mu.Unlock()
		if !slices.Contains(events, "on_llm_compacting") || !slices.Contains(events, "on_llm_compacted") {
			t.Fatalf("expected compaction stream events, got %v", events)
		}

		// Subsequent turn continues cleanly on same warm session after compact-only turn.
		r3, err := model.Invoke(sctx, []message.Message{
			message.NewUser(&message.TextBlock{Text: "first"}),
			r1,
			r2,
			message.NewUser(&message.TextBlock{Text: "second"}),
		})
		if err != nil {
			t.Fatalf("Invoke 3: %v", err)
		}
		if r3.Content.Text() != "Mock reply 2: second" {
			t.Fatalf("unexpected reply after compact: %q", r3.Content.Text())
		}
		if cnt := countLogPrefix(readMockLog(t, logPath), "CREATE:"); cnt != 1 {
			t.Fatalf("expected 1 session across manual compact, got %d", cnt)
		}
	})

	t.Run("idle background compaction + SyncOnWrite CAS reconciliation", func(t *testing.T) {
		idleCh := make(chan message.Message, 8)
		p, logPath := newTestProvider(t, "idle_compaction",
			WithSyncOnWrite(true),
			WithOnIdleMessage(func(msg message.Message) {
				idleCh <- msg
			}),
		)
		model, _ := llm.NewModel(p, "gpt-5", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		r1, err := model.Invoke(ctx, []message.Message{message.NewUser(&message.TextBlock{Text: "turn 1"})})
		if err != nil {
			t.Fatalf("Invoke 1: %v", err)
		}

		// Wait for idle compacting + compacted notifications.
		var compactedMsg message.Message
		deadline := time.After(3 * time.Second)
		for compactedMsg == nil {
			select {
			case m := <-idleCh:
				if ext, ok := m.GetExtensions()[IdleEventExtensionID].(*IdleEventExtension); ok && ext.Kind == "compacted" {
					compactedMsg = m
				}
			case <-deadline:
				t.Fatal("timed out waiting for idle compacted notification")
			}
		}

		// Writing with stale un-synced history must return *llm.OutOfSyncError.
		_, err = model.Invoke(ctx, []message.Message{
			message.NewUser(&message.TextBlock{Text: "turn 1"}),
			r1,
			message.NewUser(&message.TextBlock{Text: "turn 2"}),
		})
		var syncErr *llm.OutOfSyncError
		if !errors.As(err, &syncErr) {
			t.Fatalf("expected *llm.OutOfSyncError, got %v", err)
		}
		if syncErr.Reason != "background_compaction" || len(syncErr.Messages) == 0 {
			t.Fatalf("unexpected OutOfSyncError: %+v", syncErr)
		}

		// Re-base with syncErr.Messages and resubmit on the SAME warm session.
		rebased := append(slices.Clone(syncErr.Messages), message.NewUser(&message.TextBlock{Text: "turn 2"}))
		r2, err := model.Invoke(ctx, rebased)
		if err != nil {
			t.Fatalf("Invoke after rebase: %v", err)
		}
		if r2.Content.Text() != "Turn 2: turn 2" {
			t.Fatalf("unexpected reply after rebase: %q", r2.Content.Text())
		}
		if cnt := countLogPrefix(readMockLog(t, logPath), "CREATE:"); cnt != 1 {
			t.Fatalf("expected warm session reuse after SyncOnWrite rebase, got %d creates", cnt)
		}
	})

	t.Run("idle shutdown + SyncOnWrite OutOfSyncError", func(t *testing.T) {
		idleCh := make(chan message.Message, 4)
		p, _ := newTestProvider(t, "idle_shutdown",
			WithSyncOnWrite(true),
			WithOnIdleMessage(func(msg message.Message) {
				idleCh <- msg
			}),
		)
		model, _ := llm.NewModel(p, "gpt-5", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		r1, err := model.Invoke(ctx, []message.Message{message.NewUser(&message.TextBlock{Text: "hi"})})
		if err != nil {
			t.Fatalf("Invoke 1: %v", err)
		}

		select {
		case msg := <-idleCh:
			if ext, ok := msg.GetExtensions()[IdleEventExtensionID].(*IdleEventExtension); !ok || ext.Kind != "exited" {
				t.Fatalf("expected exited idle extension, got %+v", msg.GetExtensions())
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for idle shutdown notification")
		}

		_, err = model.Invoke(ctx, []message.Message{
			message.NewUser(&message.TextBlock{Text: "hi"}),
			r1,
			message.NewUser(&message.TextBlock{Text: "next"}),
		})
		var syncErr *llm.OutOfSyncError
		if !errors.As(err, &syncErr) || syncErr.Reason != "session_exited" {
			t.Fatalf("expected OutOfSyncError(session_exited), got %v", err)
		}
	})

	t.Run("history edit falls back to fresh session", func(t *testing.T) {
		p, logPath := newTestProvider(t, "basic")
		model, _ := llm.NewModel(p, "gpt-5", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		r1, err := model.Invoke(ctx, []message.Message{message.NewUser(&message.TextBlock{Text: "hi"})})
		if err != nil {
			t.Fatalf("Invoke 1: %v", err)
		}

		// Mutate earlier user message -> prefix digest mismatch -> fresh session.
		r2, err := model.Invoke(ctx, []message.Message{
			message.NewUser(&message.TextBlock{Text: "edited hi"}),
			r1,
			message.NewUser(&message.TextBlock{Text: "next"}),
		})
		if err != nil {
			t.Fatalf("Invoke 2: %v", err)
		}
		if !strings.Contains(r2.Content.Text(), "edited hi") {
			t.Fatalf("expected fresh transcript replay to include edited history, got %q", r2.Content.Text())
		}
		if cnt := countLogPrefix(readMockLog(t, logPath), "CREATE:"); cnt != 2 {
			t.Fatalf("expected 2 creates after history edit fallback, got %d", cnt)
		}
	})
}

func TestCopilotModalityAndCapabilityValidation(t *testing.T) {
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

	t.Run("profile override rejects unsupported modality and tool_call without spawning CLI", func(t *testing.T) {
		p, logPath := newTestProvider(t, "basic")
		p.OverrideProfile("text-only", profile.Profile{
			ID:   "text-only",
			Name: "Text Only",
			Capabilities: profile.Capabilities{
				Completion: true,
				ToolCall:   false,
			},
			Modalities: profile.Modalities{
				Inputs:  []profile.Modality{profile.ModalityText},
				Outputs: []profile.Modality{profile.ModalityText},
			},
		})
		p.OverrideProfile("embed-only", profile.Profile{
			ID:   "embed-only",
			Name: "Embed Only",
			Capabilities: profile.Capabilities{
				Completion: false,
				Embedding:  true,
			},
			Modalities: profile.Modalities{
				Inputs: []profile.Modality{profile.ModalityText},
			},
		})

		ctx := context.Background()

		// Image input rejected by profile.
		_, err := p.Stream(ctx, &llm.Request{
			Model: "text-only",
			Messages: []message.Message{
				message.NewUser(&message.ImageBlock{Data: pngBytes, MIMEType: "image/png"}),
			},
		})
		if !errors.Is(err, profile.ErrUnsupportedModality) {
			t.Fatalf("expected ErrUnsupportedModality, got %v", err)
		}

		// Tools rejected by profile.
		_, err = p.Stream(ctx, &llm.Request{
			Model:    "text-only",
			Messages: []message.Message{message.NewUser(&message.TextBlock{Text: "hi"})},
			Tools: []tool.Definition{{
				Name:        "echo_tool",
				Description: "echo",
				InputSchema: &jsonschema.Schema{Type: "object"},
			}},
		})
		if !errors.Is(err, profile.ErrUnsupportedCapability) {
			t.Fatalf("expected ErrUnsupportedCapability for tool_call, got %v", err)
		}

		// Non-completion profile rejected.
		_, err = p.Stream(ctx, &llm.Request{
			Model:    "embed-only",
			Messages: []message.Message{message.NewUser(&message.TextBlock{Text: "hi"})},
		})
		if !errors.Is(err, profile.ErrUnsupportedCapability) {
			t.Fatalf("expected ErrUnsupportedCapability for completion, got %v", err)
		}

		if log := readMockLog(t, logPath); log != "" {
			t.Fatalf("expected zero CLI spawns on profile rejection, got log:\n%s", log)
		}
	})

	t.Run("discovered profile enforces vision modality", func(t *testing.T) {
		p, _ := newTestProvider(t, "basic", WithOnListModels(func(context.Context) ([]copilot.ModelInfo, error) {
			return []copilot.ModelInfo{
				{
					ID:   "custom-no-vision",
					Name: "Custom No Vision",
					Capabilities: copilot.ModelCapabilities{
						Supports: copilot.ModelSupports{Vision: false},
					},
				},
			}, nil
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, err := p.ListModels(ctx); err != nil {
			t.Fatalf("ListModels: %v", err)
		}
		_, err := p.Stream(ctx, &llm.Request{
			Model: "custom-no-vision",
			Messages: []message.Message{
				message.NewUser(&message.ImageBlock{Data: pngBytes, MIMEType: "image/png"}),
			},
		})
		if !errors.Is(err, profile.ErrUnsupportedModality) {
			t.Fatalf("expected ErrUnsupportedModality from discovered profile, got %v", err)
		}
	})

	t.Run("unlisted model passes through inline user images across fresh and warm turns, rejects unsupported wire modalities", func(t *testing.T) {
		p, logPath := newTestProvider(t, "basic")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Confirm model is unknown on cold cache.
		if _, ok := p.GetProfile("unlisted-vision-model"); ok {
			t.Fatal("expected unlisted-vision-model to be absent from cold profile cache")
		}

		// Wire-level errors fail before creating any session.
		for _, req := range []*llm.Request{
			{
				Model: "unlisted-vision-model",
				Messages: []message.Message{
					message.NewSystem(&message.TextBlock{Text: "sys"}, &message.ImageBlock{Data: pngBytes, MIMEType: "image/png"}),
					message.NewUser(&message.TextBlock{Text: "hi"}),
				},
			},
			{
				Model:    "unlisted-vision-model",
				Messages: []message.Message{message.NewUser(&message.AudioBlock{Data: []byte("wav"), MIMEType: "audio/wav"})},
			},
			{
				Model:    "unlisted-vision-model",
				Messages: []message.Message{message.NewUser(&message.VideoBlock{Data: []byte("mp4"), MIMEType: "video/mp4"})},
			},
			{
				Model:    "unlisted-vision-model",
				Messages: []message.Message{message.NewUser(&message.DocumentBlock{Data: []byte("%PDF"), MIMEType: "application/pdf"})},
			},
			{
				Model:    "unlisted-vision-model",
				Messages: []message.Message{message.NewUser(&message.ImageBlock{})},
			},
			{
				Model:    "unlisted-vision-model",
				Messages: []message.Message{message.NewUser(&message.ImageBlock{URL: "https://example.com/a.png", MIMEType: "image/png"})},
			},
		} {
			if _, err := p.Stream(ctx, req); !errors.Is(err, profile.ErrUnsupportedModality) {
				t.Fatalf("expected ErrUnsupportedModality, got %v", err)
			}
		}
		if cnt := countLogPrefix(readMockLog(t, logPath), "CREATE:"); cnt != 0 {
			t.Fatalf("expected 0 session creates for invalid wire modalities, got %d", cnt)
		}

		// Valid inline image on fresh turn sends AttachmentBlob over JSON-RPC.
		m, err := llm.NewModel(p, "unlisted-vision-model", nil)
		if err != nil {
			t.Fatalf("NewModel: %v", err)
		}
		r1, err := m.Invoke(ctx, []message.Message{
			message.NewUser(
				&message.TextBlock{Text: "look at this"},
				&message.ImageBlock{Data: pngBytes, MIMEType: "image/png"},
			),
		})
		if err != nil {
			t.Fatalf("Invoke fresh image: %v", err)
		}

		// Warm follow-up turn with another inline image reuses the same session.
		_, err = m.Invoke(ctx, []message.Message{
			message.NewUser(
				&message.TextBlock{Text: "look at this"},
				&message.ImageBlock{Data: pngBytes, MIMEType: "image/png"},
			),
			r1,
			message.NewUser(
				&message.TextBlock{Text: "and this"},
				&message.ImageBlock{Data: pngBytes, MIMEType: "image/png"},
			),
		})
		if err != nil {
			t.Fatalf("Invoke warm image: %v", err)
		}

		log := readMockLog(t, logPath)
		if cnt := countLogPrefix(log, "CREATE:"); cnt != 1 {
			t.Fatalf("expected 1 session create across fresh+warm image turns, got %d", cnt)
		}
		if cnt := countLogPrefix(log, "ATTACHMENTS:"); cnt != 2 {
			t.Fatalf("expected 2 ATTACHMENTS entries in log, got %d\nlog:\n%s", cnt, log)
		}
		if !strings.Contains(log, `"type":"blob"`) || !strings.Contains(log, `"mimeType":"image/png"`) {
			t.Fatalf("expected blob image/png attachment in log:\n%s", log)
		}
	})
}
