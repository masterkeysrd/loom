package loomcodex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/masterkeysrd/loom/auth"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	loomopenai "github.com/masterkeysrd/loom/providers/openai"
	"github.com/masterkeysrd/loom/tool"
)

func TestBindToolsAndReplay(t *testing.T) {
	var mu sync.Mutex
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer access-token" {
			t.Errorf("unexpected request method, path or credential: %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		mu.Lock()
		requests = append(requests, payload)
		requestNumber := len(requests)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		writeEvent(w, `{"type":"response.created","response":{"id":"resp_turn"}}`)
		if requestNumber == 1 {
			writeEvent(w, `{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","encrypted_content":"opaque1"}}`)
			writeEvent(w, `{"type":"response.output_item.done","output_index":1,"item":{"type":"reasoning","id":"rs_2","encrypted_content":"opaque2"}}`)
			writeEvent(w, `{"type":"response.output_item.done","output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"query\":\"hello\"}"}}`)
		} else {
			writeEvent(w, `{"type":"response.output_text.delta","delta":"Found it"}`)
		}
		writeEvent(w, `{"type":"response.completed","response":{"id":"resp_turn","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`)
	}))
	defer server.Close()

	provider, err := NewProvider(auth.StaticTokenSource("access-token"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	provider.baseURL = server.URL + "/v1"
	temperature := float32(0.5)
	model, err := llm.NewModel(provider, "gpt-6.2-sol", &llm.ModelConfig{MaxTokens: 200, Temperature: &temperature})
	if err != nil {
		t.Fatal(err)
	}
	type input struct {
		Query string `json:"query"`
	}
	var toolRuns int
	lookup, err := tool.New("lookup", "Look up", "Look up a phrase", func(_ context.Context, args input) (string, error) {
		toolRuns++
		return "found " + args.Query, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	model = model.BindTools(lookup)
	history := message.MessageList{
		message.NewSystemText("Use lookup when needed."),
		message.NewUserText("Find hello"),
		message.NewSystemText("Keep the response short."),
	}
	first, err := model.Invoke(t.Context(), history)
	if err != nil {
		t.Fatal(err)
	}
	calls := first.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "lookup" || calls[0].Args["query"] != "hello" {
		t.Fatalf("unexpected first turn: %#v", first.Content)
	}
	history = append(history, first)
	stored, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	var restored message.MessageList
	if err := json.Unmarshal(stored, &restored); err != nil {
		t.Fatal(err)
	}
	restoredCalls := restored[len(restored)-1].(*message.Assistant).ToolCalls()
	if len(restoredCalls) != 1 || toolRuns != 0 {
		t.Fatalf("expected one pending tool call, got %d pending and %d executed", len(restoredCalls), toolRuns)
	}
	result, err := tool.NewContainer(lookup).Call(t.Context(), restoredCalls[0])
	if err != nil {
		t.Fatal(err)
	}
	restored = append(restored, result)
	resumed, err := NewProvider(auth.StaticTokenSource("access-token"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	resumed.baseURL = provider.baseURL
	model, err = llm.NewModel(resumed, "gpt-6.2-sol", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := model.BindTools(lookup).Invoke(t.Context(), restored)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Done || second.Content.Text() != "Found it" || second.Metrics == nil || second.Metrics.TotalTokens != 15 {
		t.Fatalf("unexpected second turn: %#v", second)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("got %d requests, want two", len(requests))
	}
	if toolRuns != 1 {
		t.Fatalf("tool ran %d times, want one", toolRuns)
	}
	for _, req := range requests {
		if req["store"] != false || req["stream"] != true || req["instructions"] != "Use lookup when needed." {
			t.Fatalf("invalid request fields: %#v", req)
		}
		for _, key := range []string{"max_output_tokens", "temperature", "top_p", "tools", "previous_response_id"} {
			if _, ok := req[key]; ok {
				t.Fatalf("unexpected field %s in %#v", key, req)
			}
		}
		include, ok := req["include"].([]any)
		if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
			t.Fatalf("missing encrypted reasoning include: %#v", req)
		}
		items, ok := req["input"].([]any)
		if !ok || len(items) < 3 {
			t.Fatalf("missing input items: %#v", req)
		}
		additional := items[0].(map[string]any)
		tools := additional["tools"].([]any)
		if additional["type"] != "additional_tools" || len(tools) != 1 || tools[0].(map[string]any)["name"] != "lookup" {
			t.Fatalf("invalid function binding: %#v", additional)
		}
		if items[2].(map[string]any)["role"] != "developer" {
			t.Fatalf("expected developer role: %#v", items[2])
		}
	}
	items := requests[1]["input"].([]any)
	if len(items) != 7 {
		t.Fatalf("unexpected replay input: %#v", items)
	}
	for i, want := range []string{"opaque1", "opaque2"} {
		if item := items[i+3].(map[string]any); item["encrypted_content"] != want || item["type"] != "reasoning" {
			t.Fatalf("unexpected reasoning replay: %#v", item)
		}
	}
	for i, want := range []string{"function_call", "function_call_output"} {
		if item := items[i+5].(map[string]any); item["call_id"] != "call_1" || item["type"] != want {
			t.Fatalf("unexpected tool replay: %#v", item)
		}
	}
	if output := items[6].(map[string]any)["output"]; !strings.Contains(fmt.Sprint(output), "found hello") {
		t.Fatalf("result did not contain the tool output: %v", output)
	}
}

func TestResponsesTerminalErrors(t *testing.T) {
	for _, tc := range []struct{ event, want string }{
		{`{"type":"response.failed","response":{"error":{"code":"subscription_sharing_usage_limit_exceeded","message":"Try again later"}}}`, "subscription_sharing_usage_limit_exceeded"},
		{`{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`, "incomplete (max_output_tokens)"},
		{`{"type":"error","code":"invalid_request_error","message":"Invalid request"}`, "invalid_request_error"},
		{"", "without response.completed"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				writeEvent(w, `{"type":"response.output_text.delta","delta":"partial"}`)
				if tc.event != "" {
					writeEvent(w, tc.event)
				}
			}))
			defer server.Close()
			p, err := NewProvider(auth.StaticTokenSource("token"), server.Client())
			if err != nil {
				t.Fatal(err)
			}
			p.baseURL = server.URL
			m, err := llm.NewModel(p, "gpt-6.2-sol", nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = m.Invoke(t.Context(), []message.Message{message.NewUserText("hello")})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestModelsAndUnsupportedTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("unexpected model request path or credential: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"models":[{"slug":"x","display_name":"X","visibility":"list"},{"slug":"y","visibility":"hidden"}]}`)
	}))
	defer server.Close()
	p, err := NewProvider(auth.StaticTokenSource("token"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	p.baseURL = server.URL + "/v1"
	models, err := p.ListModels(t.Context())
	if err != nil || len(models) != 1 || models[0].ID != "x" || models[0].Name != "X" {
		t.Fatalf("unexpected model catalog: %#v, %v", models, err)
	}
	_, err = p.Stream(t.Context(), &llm.Request{Model: "x", Extensions: map[string]llm.Extension{
		loomopenai.ResponsesExtensionID: loomopenai.WithToolSearch(),
	}})
	if err == nil || !strings.Contains(err.Error(), "tool_search") {
		t.Fatalf("expected unsupported tool error, got %v", err)
	}
}

func writeEvent(w http.ResponseWriter, data string) {
	fmt.Fprintf(w, "data: %s\n\n", data)
}
