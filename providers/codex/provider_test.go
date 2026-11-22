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

	"github.com/google/uuid"
	"github.com/masterkeysrd/loom/auth"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	loomopenai "github.com/masterkeysrd/loom/providers/openai"
	"github.com/masterkeysrd/loom/tool"
)

func TestBindToolsAndReplay(t *testing.T) {
	var mu sync.Mutex
	var requests []map[string]any
	var sessionHeaders []string
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
		sessionHeaders = append(sessionHeaders, r.Header.Get("session-id"))
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
		message.NewSystemText("Cite the lookup result."),
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
	if sessionHeaders[0] == "" || sessionHeaders[1] != sessionHeaders[0] || first.Metadata[SessionIDKey] != sessionHeaders[0] || second.Metadata[SessionIDKey] != sessionHeaders[1] {
		t.Fatalf("checkpoint replay lost Codex session: %v", sessionHeaders)
	}
	if toolRuns != 1 {
		t.Fatalf("tool ran %d times, want one", toolRuns)
	}
	for _, req := range requests {
		if req["store"] != false || req["stream"] != true {
			t.Fatalf("invalid request fields: %#v", req)
		}
		for _, key := range []string{"instructions", "max_output_tokens", "temperature", "top_p", "tools", "previous_response_id"} {
			if _, ok := req[key]; ok {
				t.Fatalf("unexpected field %s in %#v", key, req)
			}
		}
		include, ok := req["include"].([]any)
		if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
			t.Fatalf("missing encrypted reasoning include: %#v", req)
		}
		items, ok := req["input"].([]any)
		if !ok || len(items) < 5 {
			t.Fatalf("missing input items: %#v", req)
		}
		prefix := items[0].(map[string]any)
		if prefix["role"] != "developer" || prefix["content"] != "Use lookup when needed." {
			t.Fatalf("unexpected developer prefix: %#v", prefix)
		}
		secondInstruction := items[1].(map[string]any)
		if secondInstruction["role"] != "developer" || secondInstruction["content"] != "Cite the lookup result." {
			t.Fatalf("unexpected second developer message: %#v", secondInstruction)
		}
		additional := items[2].(map[string]any)
		tools := additional["tools"].([]any)
		if additional["type"] != "additional_tools" || len(tools) != 1 || tools[0].(map[string]any)["name"] != "lookup" {
			t.Fatalf("invalid function binding: %#v", additional)
		}
		if items[4].(map[string]any)["role"] != "developer" {
			t.Fatalf("expected developer role: %#v", items[4])
		}
	}
	items := requests[1]["input"].([]any)
	if len(items) != 9 {
		t.Fatalf("unexpected replay input: %#v", items)
	}
	for i, want := range []string{"opaque1", "opaque2"} {
		if item := items[i+5].(map[string]any); item["encrypted_content"] != want || item["type"] != "reasoning" {
			t.Fatalf("unexpected reasoning replay: %#v", item)
		}
	}
	for i, want := range []string{"function_call", "function_call_output"} {
		if item := items[i+7].(map[string]any); item["call_id"] != "call_1" || item["type"] != want {
			t.Fatalf("unexpected tool replay: %#v", item)
		}
	}
	if output := items[8].(map[string]any)["output"]; !strings.Contains(fmt.Sprint(output), "found hello") {
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
	_, err = p.Stream(t.Context(), &llm.Request{
		Model: "x",
		Messages: []message.Message{
			&message.Assistant{Content: message.Content{&message.ServerToolCall{ID: "srv_1", Name: "tool_search"}}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "tool_search") {
		t.Fatalf("expected unsupported server tool_search history error, got %v", err)
	}
}

func writeEvent(w http.ResponseWriter, data string) {
	fmt.Fprintf(w, "data: %s\n\n", data)
}

func TestSessionHeader(t *testing.T) {
	headers := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Get("session-id")
		w.Header().Set("Content-Type", "text/event-stream")
		writeEvent(w, `{"type":"response.completed","response":{"id":"resp_test"}}`)
	}))
	defer server.Close()
	newModel := func() *llm.Model {
		p, err := NewProvider(auth.StaticTokenSource("token"), server.Client())
		if err != nil {
			t.Fatal(err)
		}
		p.baseURL = server.URL
		m, err := llm.NewModel(p, "gpt-6.2-sol", nil)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	invoke := func(m *llm.Model, opts ...llm.CallOption) string {
		t.Helper()
		if _, err := m.Invoke(t.Context(), message.MessageList{message.NewUserText("hello")}, opts...); err != nil {
			t.Fatal(err)
		}
		return <-headers
	}
	m := newModel()
	first := invoke(m)
	if _, err := uuid.Parse(first); err != nil {
		t.Fatalf("expected UUID, got %q", first)
	}
	thread := llm.WithExtensionOption(SessionID("thread-123"))
	if got := invoke(m, thread); got != "thread-123" {
		t.Fatalf("override = %q", got)
	}
	if got := invoke(m, llm.WithExtensionOption(SessionID(""))); got != "" {
		t.Fatalf("disabled header = %q", got)
	}
	if got := invoke(m); got == first || got == "" {
		t.Fatalf("independent conversation reused session %q", got)
	}
	resumed := newModel()
	if got := invoke(resumed); got == first || got == "" {
		t.Fatalf("second provider default = %q", got)
	}
	if got := invoke(resumed, thread); got != "thread-123" {
		t.Fatalf("resumed override = %q", got)
	}
}

func TestClientToolSearchAndDeferredTools(t *testing.T) {
	var mu sync.Mutex
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		mu.Lock()
		requests = append(requests, payload)
		turn := len(requests)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		writeEvent(w, `{"type":"response.created","response":{"id":"resp_turn"}}`)
		switch turn {
		case 1:
			writeEvent(w, `{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_search","name":"tool_search_bm25","arguments":"{\"query\":\"weather\"}"}}`)
		case 2:
			writeEvent(w, `{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_2","call_id":"call_weather","name":"get_weather","arguments":"{\"city\":\"Tokyo\"}"}}`)
		default:
			writeEvent(w, `{"type":"response.output_text.delta","delta":"Tokyo is 20C."}`)
		}
		writeEvent(w, `{"type":"response.completed","response":{"id":"resp_turn","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`)
	}))
	defer server.Close()

	provider, err := NewProvider(auth.StaticTokenSource("access-token"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	provider.baseURL = server.URL + "/v1"

	type weatherInput struct {
		City string `json:"city"`
	}
	var weatherRuns int
	weatherTool, err := tool.New("get_weather", "Weather", "Get weather for a city", func(_ context.Context, in weatherInput) (string, error) {
		weatherRuns++
		return in.City + ": 20C", nil
	}, tool.WithDeferLoading())
	if err != nil {
		t.Fatal(err)
	}
	currencyTool, err := tool.New("convert_currency", "Currency", "Convert currency amounts", func(_ context.Context, _ struct{}) (string, error) {
		return "100 USD", nil
	}, tool.WithDeferLoading())
	if err != nil {
		t.Fatal(err)
	}
	searchTool, err := tool.NewBM25Search([]*tool.Tool{weatherTool, currencyTool}, tool.WithSearchTopK(1))
	if err != nil {
		t.Fatal(err)
	}

	model, err := llm.NewModel(provider, "gpt-5.3-codex", nil)
	if err != nil {
		t.Fatal(err)
	}
	model = model.
		WithMiddleware(llm.DeferredToolsMiddleware(nil)).
		WithMiddleware(llm.InlineToolsMiddleware(nil)).
		BindTools(searchTool, weatherTool, currencyTool)
	container := tool.NewContainer(searchTool, weatherTool, currencyTool)

	history := message.MessageList{
		message.NewSystemText("Search for tools before calling them."),
		message.NewUserText("What is the weather in Tokyo?"),
	}

	turn1, err := model.Invoke(t.Context(), history)
	if err != nil {
		t.Fatal(err)
	}
	calls := turn1.ToolCalls()
	if len(calls) != 1 || calls[0].Name != "tool_search_bm25" {
		t.Fatalf("unexpected turn 1 tool calls: %#v", calls)
	}
	searchRes, err := container.Call(t.Context(), calls[0])
	if err != nil {
		t.Fatal(err)
	}
	history = append(history, turn1, searchRes)

	turn2, err := model.Invoke(t.Context(), history)
	if err != nil {
		t.Fatal(err)
	}
	calls = turn2.ToolCalls()
	if len(calls) != 1 || calls[0].Name != "get_weather" {
		t.Fatalf("unexpected turn 2 tool calls: %#v", calls)
	}
	weatherRes, err := container.Call(t.Context(), calls[0])
	if err != nil {
		t.Fatal(err)
	}
	history = append(history, turn2, weatherRes)

	turn3, err := model.Invoke(t.Context(), history)
	if err != nil {
		t.Fatal(err)
	}
	if turn3.Content.Text() != "Tokyo is 20C." || weatherRuns != 1 {
		t.Fatalf("unexpected turn 3 result: %q (weatherRuns=%d)", turn3.Content.Text(), weatherRuns)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("got %d requests, want 3", len(requests))
	}
	for i, req := range requests {
		if _, ok := req["tools"]; ok {
			t.Fatalf("request %d unexpectedly sent top-level tools: %#v", i, req["tools"])
		}
		items := req["input"].([]any)
		prefix := items[0].(map[string]any)
		if prefix["role"] != "developer" {
			t.Fatalf("request %d item 0 = %#v, want developer", i, prefix)
		}
		initialToolsItem := items[1].(map[string]any)
		if initialToolsItem["type"] != "additional_tools" {
			t.Fatalf("request %d item 1 = %#v, want additional_tools", i, initialToolsItem)
		}
		initialTools := initialToolsItem["tools"].([]any)
		if len(initialTools) != 1 || initialTools[0].(map[string]any)["name"] != "tool_search_bm25" {
			t.Fatalf("request %d initial additional_tools = %#v, want only tool_search_bm25", i, initialTools)
		}
		if _, hasDefer := initialTools[0].(map[string]any)["defer_loading"]; hasDefer {
			t.Fatalf("request %d unexpectedly set defer_loading: %#v", i, initialTools[0])
		}
	}

	turn2Items := requests[1]["input"].([]any)
	if len(turn2Items) != 6 {
		t.Fatalf("turn 2 input len = %d, want 6: %#v", len(turn2Items), turn2Items)
	}
	if output := turn2Items[4].(map[string]any)["output"]; strings.Contains(fmt.Sprint(output), "<tools>") {
		t.Fatalf("turn 2 fell back to text tool expansion instead of native additional_tools: %v", output)
	}
	discovered := turn2Items[5].(map[string]any)
	if discovered["type"] != "additional_tools" {
		t.Fatalf("turn 2 item 5 = %#v, want additional_tools", discovered)
	}
	discoveredTools := discovered["tools"].([]any)
	if len(discoveredTools) != 1 {
		t.Fatalf("turn 2 discovered tools = %#v, want 1 tool", discoveredTools)
	}
	weatherParam := discoveredTools[0].(map[string]any)
	if weatherParam["name"] != "get_weather" {
		t.Fatalf("turn 2 discovered tool = %#v, want get_weather", weatherParam)
	}
	if _, hasDefer := weatherParam["defer_loading"]; hasDefer {
		t.Fatalf("turn 2 discovered tool unexpectedly set defer_loading: %#v", weatherParam)
	}
}

func TestInlineToolDefinition(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeEvent(w, `{"type":"response.output_text.delta","delta":"done"}`)
		writeEvent(w, `{"type":"response.completed","response":{"id":"resp_inline"}}`)
	}))
	defer server.Close()

	provider, err := NewProvider(auth.StaticTokenSource("access-token"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	provider.baseURL = server.URL + "/v1"

	inlineTool, err := tool.New("inline_calc", "Calculator", "Add numbers", func(_ context.Context, _ struct {
		A int `json:"a"`
	}) (string, error) {
		return "2", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	staticTool, err := tool.New("ping", "Ping", "Ping service", func(_ context.Context, _ struct{}) (string, error) {
		return "pong", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	model, err := llm.NewModel(provider, "gpt-5.3-codex", nil)
	if err != nil {
		t.Fatal(err)
	}
	model = model.
		WithMiddleware(llm.InlineToolsMiddleware(nil)).
		BindTools(staticTool, inlineTool)

	_, err = model.Invoke(t.Context(), message.MessageList{
		message.NewSystemText("System prompt"),
		message.NewUser(&message.TextBlock{Text: "Calculate"}, inlineTool.ToBlock()),
	})
	if err != nil {
		t.Fatal(err)
	}

	items := payload["input"].([]any)
	if len(items) != 4 {
		t.Fatalf("input len = %d, want 4: %#v", len(items), items)
	}
	initialTools := items[1].(map[string]any)["tools"].([]any)
	if len(initialTools) != 1 || initialTools[0].(map[string]any)["name"] != "ping" {
		t.Fatalf("initial additional_tools = %#v, want only ping", initialTools)
	}
	if items[2].(map[string]any)["content"] != "Calculate" {
		t.Fatalf("user item unexpectedly expanded inline tool to text: %#v", items[2])
	}
	inlineItem := items[3].(map[string]any)
	inlineTools := inlineItem["tools"].([]any)
	if inlineItem["type"] != "additional_tools" || len(inlineTools) != 1 || inlineTools[0].(map[string]any)["name"] != "inline_calc" {
		t.Fatalf("inline additional_tools = %#v, want inline_calc", inlineItem)
	}
}
