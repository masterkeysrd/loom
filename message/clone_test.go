package message

import (
	"reflect"
	"testing"
)

func TestCloneMessage(t *testing.T) {
	// Test System Message
	sys := &System{
		Base: Base{
			ID:       "sys-1",
			Metadata: map[string]any{"key": "val"},
		},
		Content: Content{&TextBlock{Text: "system prompt"}},
	}
	sysCloned := CloneMessage(sys).(*System)
	if sysCloned == sys {
		t.Error("expected a new pointer, got same")
	}
	if !reflect.DeepEqual(sysCloned, sys) {
		t.Errorf("expected cloned system message to equal original, got %v", sysCloned)
	}
	// Verify deep copy of metadata
	sysCloned.Metadata["key"] = "newval"
	if sys.Metadata["key"] != "val" {
		t.Error("metadata was not deep copied")
	}

	// Test User Message
	user := &User{
		Base: Base{
			ID: "user-1",
		},
		Content: Content{&TextBlock{Text: "hello"}},
	}
	userCloned := CloneMessage(user).(*User)
	if userCloned == user {
		t.Error("expected a new pointer, got same")
	}
	if !reflect.DeepEqual(userCloned, user) {
		t.Errorf("expected cloned user message to equal original, got %v", userCloned)
	}

	// Test Assistant Message
	assistant := &Assistant{
		Base: Base{
			ID: "assist-1",
		},
		Content: Content{
			&TextBlock{Text: "response"},
			&ToolCall{ID: "call-1", Name: "my_tool", Args: map[string]any{"arg": 1}},
		},
		Metrics: &TokenMetrics{
			TotalTokens: 150,
			Tokens: TokenDetails{
				Input:     100,
				Output:    50,
				CacheRead: 40,
			},
		},
		Done:       true,
		DoneReason: "stop",
	}
	assistantCloned := CloneMessage(assistant).(*Assistant)
	if assistantCloned == assistant {
		t.Error("expected a new pointer, got same")
	}
	if !reflect.DeepEqual(assistantCloned, assistant) {
		t.Errorf("expected cloned assistant message to equal original, got %v", assistantCloned)
	}
	// Verify deep copy of metrics
	assistantCloned.Metrics.TotalTokens = 200
	if assistant.Metrics.TotalTokens != 150 {
		t.Error("metrics were not deep copied")
	}
	if !assistantCloned.Done || assistantCloned.DoneReason != "stop" {
		t.Errorf("done or done_reason not cloned: got Done=%v, DoneReason=%s", assistantCloned.Done, assistantCloned.DoneReason)
	}
	// Verify deep copy of content block args
	assistantCloned.Content[1].(*ToolCall).Args["arg"] = 2
	if assistant.Content[1].(*ToolCall).Args["arg"].(int) != 1 {
		t.Error("content block arguments were not deep copied")
	}

	// Test Tool Message
	tool := &Tool{
		Base: Base{
			ID: "tool-msg-1",
		},
		ToolCallID:        "call-1",
		Name:              "my_tool",
		Content:           Content{&TextBlock{Text: "result"}},
		IsError:           false,
		StructuredContent: map[string]any{"out": "ok"},
	}
	toolCloned := CloneMessage(tool).(*Tool)
	if toolCloned == tool {
		t.Error("expected a new pointer, got same")
	}
	if !reflect.DeepEqual(toolCloned, tool) {
		t.Errorf("expected cloned tool message to equal original, got %v", toolCloned)
	}
}

func TestCloneBlock_ExtrasAndServerTools(t *testing.T) {
	// 1. TextBlock with Extras
	txt := &TextBlock{Text: "hello", Extras: map[string]any{"color": "blue"}}
	txtCloned := CloneBlock(txt).(*TextBlock)
	if txtCloned == txt {
		t.Error("expected new pointer for TextBlock")
	}
	if !reflect.DeepEqual(txtCloned, txt) {
		t.Errorf("cloned TextBlock mismatch: got %#v, want %#v", txtCloned, txt)
	}
	txtCloned.Extras["color"] = "red"
	if txt.Extras["color"] != "blue" {
		t.Error("TextBlock.Extras was not deep copied")
	}

	// 2. ThinkingBlock with Extras
	th := &ThinkingBlock{Thinking: "hmm", Extras: map[string]any{"speed": "fast"}}
	thCloned := CloneBlock(th).(*ThinkingBlock)
	if thCloned == th {
		t.Error("expected new pointer for ThinkingBlock")
	}
	if !reflect.DeepEqual(thCloned, th) {
		t.Errorf("cloned ThinkingBlock mismatch: got %#v, want %#v", thCloned, th)
	}
	thCloned.Extras["speed"] = "slow"
	if th.Extras["speed"] != "fast" {
		t.Error("ThinkingBlock.Extras was not deep copied")
	}

	// 3. ToolCallChunk with Extras
	tcc := &ToolCallChunk{
		Index:     1,
		ID:        "call_1",
		Name:      "search",
		ArgsChunk: `{"q":`,
		Extras:    map[string]any{"origin": "client"},
	}
	tccCloned := CloneBlock(tcc).(*ToolCallChunk)
	if tccCloned == tcc {
		t.Error("expected new pointer for ToolCallChunk")
	}
	if !reflect.DeepEqual(tccCloned, tcc) {
		t.Errorf("cloned ToolCallChunk mismatch: got %#v, want %#v", tccCloned, tcc)
	}
	tccCloned.Extras["origin"] = "modified"
	if tcc.Extras["origin"] != "client" {
		t.Error("ToolCallChunk.Extras was not deep copied")
	}

	// 4. ServerToolCall with Args and Extras
	stc := &ServerToolCall{
		ID:     "srv_1",
		Name:   "web_search",
		Index:  2,
		Args:   map[string]any{"q": "golang"},
		Extras: map[string]any{"provider": "google"},
	}
	stcCloned := CloneBlock(stc).(*ServerToolCall)
	if stcCloned == stc {
		t.Error("expected new pointer for ServerToolCall")
	}
	if !reflect.DeepEqual(stcCloned, stc) {
		t.Errorf("cloned ServerToolCall mismatch: got %#v, want %#v", stcCloned, stc)
	}
	stcCloned.Args["q"] = "rust"
	stcCloned.Extras["provider"] = "bing"
	if stc.Args["q"] != "golang" || stc.Extras["provider"] != "google" {
		t.Error("ServerToolCall Args/Extras was not deep copied")
	}

	// 5. ServerToolCallChunk with Extras
	stcc := &ServerToolCallChunk{
		Index:     2,
		ID:        "srv_1",
		Name:      "web_search",
		ArgsChunk: `{"q": "go"`,
		Extras:    map[string]any{"mode": "streaming"},
	}
	stccCloned := CloneBlock(stcc).(*ServerToolCallChunk)
	if stccCloned == stcc {
		t.Error("expected new pointer for ServerToolCallChunk")
	}
	if !reflect.DeepEqual(stccCloned, stcc) {
		t.Errorf("cloned ServerToolCallChunk mismatch: got %#v, want %#v", stccCloned, stcc)
	}
	stccCloned.Extras["mode"] = "batch"
	if stcc.Extras["mode"] != "streaming" {
		t.Error("ServerToolCallChunk.Extras was not deep copied")
	}

	// 6. ToolReferenceBlock with Extras
	trb := &ToolReferenceBlock{
		ToolName: "calculator",
		Extras:   map[string]any{"source": "deferred"},
	}
	trbCloned := CloneBlock(trb).(*ToolReferenceBlock)
	if trbCloned == trb {
		t.Error("expected new pointer for ToolReferenceBlock")
	}
	if !reflect.DeepEqual(trbCloned, trb) {
		t.Errorf("cloned ToolReferenceBlock mismatch: got %#v, want %#v", trbCloned, trb)
	}
	trbCloned.Extras["source"] = "active"
	if trb.Extras["source"] != "deferred" {
		t.Error("ToolReferenceBlock.Extras was not deep copied")
	}

	// 7. ServerToolResult with Content, Output map, and Extras
	str := &ServerToolResult{
		ToolCallID: "srv_1",
		Name:       "web_search",
		Content: Content{
			&ToolReferenceBlock{
				ToolName: "sub_tool",
				Extras:   map[string]any{"p": 1},
			},
		},
		Output:  map[string]any{"found": true},
		IsError: false,
		Extras:  map[string]any{"latency": 150},
	}
	strCloned := CloneBlock(str).(*ServerToolResult)
	if strCloned == str {
		t.Error("expected new pointer for ServerToolResult")
	}
	if !reflect.DeepEqual(strCloned, str) {
		t.Errorf("cloned ServerToolResult mismatch: got %#v, want %#v", strCloned, str)
	}
	strCloned.Extras["latency"] = 200
	if str.Extras["latency"] != 150 {
		t.Error("ServerToolResult.Extras was not deep copied")
	}
	strCloned.Output.(map[string]any)["found"] = false
	if str.Output.(map[string]any)["found"] != true {
		t.Error("ServerToolResult.Output map was not deep copied")
	}
	strCloned.Content[0].(*ToolReferenceBlock).Extras["p"] = 2
	if str.Content[0].(*ToolReferenceBlock).Extras["p"] != 1 {
		t.Error("ServerToolResult.Content blocks were not deep copied")
	}
}

func TestCloneMessage_AssistantWithServerTools(t *testing.T) {
	assistant := &Assistant{
		Base: Base{
			ID: "assist-server-1",
		},
		Content: Content{
			&ThinkingBlock{Thinking: "pondering"},
			&ServerToolCall{
				ID:     "srv-1",
				Name:   "code_execution",
				Args:   map[string]any{"code": "print(1)"},
				Extras: map[string]any{"lang": "python"},
			},
			&ServerToolResult{
				ToolCallID: "srv-1",
				Name:       "code_execution",
				Content: Content{
					&ToolReferenceBlock{
						ToolName: "plot_viewer",
						Extras:   map[string]any{"chart": "line"},
					},
				},
				Output: map[string]any{"stdout": "1\n"},
				Extras: map[string]any{"cpu_ms": 12},
			},
			&TextBlock{Text: "Result is 1"},
		},
		Done: true,
	}

	cloned := CloneMessage(assistant).(*Assistant)
	if cloned == assistant {
		t.Fatal("expected different Assistant pointer")
	}
	if !reflect.DeepEqual(cloned, assistant) {
		t.Fatalf("cloned Assistant mismatch: got %#v, want %#v", cloned, assistant)
	}

	// Mutate cloned server tool call
	cloned.ServerToolCalls()[0].Args["code"] = "print(2)"
	cloned.ServerToolCalls()[0].Extras["lang"] = "ruby"
	if assistant.ServerToolCalls()[0].Args["code"] != "print(1)" {
		t.Error("Assistant ServerToolCall Args was not deep copied")
	}
	if assistant.ServerToolCalls()[0].Extras["lang"] != "python" {
		t.Error("Assistant ServerToolCall Extras was not deep copied")
	}

	// Mutate cloned server tool result
	cloned.ServerToolResults()[0].Extras["cpu_ms"] = 99
	if assistant.ServerToolResults()[0].Extras["cpu_ms"] != 12 {
		t.Error("Assistant ServerToolResult Extras was not deep copied")
	}
}

func TestCloneToolDefinitionBlock(t *testing.T) {
	orig := &ToolDefinitionBlock{
		Name:        "custom_tool",
		Description: "A custom tool",
		Extras:      map[string]any{"source": "user"},
	}
	cloned := CloneBlock(orig).(*ToolDefinitionBlock)
	if cloned == orig {
		t.Fatal("expected new pointer")
	}
	if cloned.Name != orig.Name || cloned.Description != orig.Description {
		t.Fatalf("mismatch: got %#v, want %#v", cloned, orig)
	}
	cloned.Extras["source"] = "system"
	if orig.Extras["source"] != "user" {
		t.Error("extras map was not cloned")
	}
}
