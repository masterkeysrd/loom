package loomanthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/tool"
)

func TestToMessageNewParamsAssistantToolCallsHaveToolUseBlocks(t *testing.T) {
	req := &llm.Request{
		Model: "claude-3-5-sonnet-latest",
		Messages: []message.Message{
			&message.Assistant{
				Content: message.Content{
					&message.TextBlock{Text: "I will call a tool."},
					&message.ToolCall{
						ID:   "toolu_123",
						Name: "lookup",
						Args: map[string]any{"query": "loom"},
					},
				},
			},
		},
	}

	params, err := toMessageNewParams(req)
	if err != nil {
		t.Fatalf("toMessageNewParams failed: %v", err)
	}

	data, err := json.Marshal(params.Messages[0])
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}

	var got struct {
		Content []map[string]any `json:"content"`
		Role    string           `json:"role"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}

	if got.Role != "assistant" {
		t.Fatalf("expected assistant role, got %q", got.Role)
	}
	if len(got.Content) != 2 {
		t.Fatalf("expected text and tool_use blocks, got %d: %s", len(got.Content), string(data))
	}
	if got.Content[0]["type"] != "text" {
		t.Fatalf("expected first block to be text, got %#v", got.Content[0])
	}
	if got.Content[1]["type"] != "tool_use" {
		t.Fatalf("expected second block to be tool_use, got %#v", got.Content[1])
	}
	if got.Content[1]["id"] != "toolu_123" {
		t.Fatalf("expected tool_use id, got %#v", got.Content[1]["id"])
	}
}

func TestToMessageNewParams_Caching(t *testing.T) {
	req := &llm.Request{
		Model: "claude-3-5-sonnet-latest",
		Extensions: map[string]llm.Extension{
			PromptCaching{}.ExtensionID(): PromptCaching{CacheHeader: true},
		},
		Messages: []message.Message{
			&message.System{
				Content: message.Content{
					&message.TextBlock{Text: "System prompt"},
				},
			},
			message.NewUserText("User message").WithExtension(&MessageCache{Enabled: true}),
		},
		Tools: []tool.Definition{
			{
				Name:        "get_weather",
				Description: "Get weather",
			},
		},
	}

	params, err := toMessageNewParams(req)
	if err != nil {
		t.Fatalf("toMessageNewParams failed: %v", err)
	}

	// Check system prompt caching
	if len(params.System) == 0 {
		t.Fatal("expected system prompt")
	}
	if params.System[0].CacheControl.Type != "ephemeral" {
		t.Errorf("expected system prompt to have ephemeral cache control, got %v", params.System[0].CacheControl.Type)
	}

	// Check user message caching
	if len(params.Messages) == 0 {
		t.Fatal("expected messages")
	}
	userMsg := params.Messages[0]
	if len(userMsg.Content) == 0 {
		t.Fatal("expected user message content")
	}
	if userMsg.Content[0].GetCacheControl().Type != "ephemeral" {
		t.Errorf("expected user message to have ephemeral cache control, got %v", userMsg.Content[0].GetCacheControl().Type)
	}

	// Check tool caching
	if len(params.Tools) == 0 {
		t.Fatal("expected tools")
	}
	if params.Tools[0].OfTool.CacheControl.Type != "ephemeral" {
		t.Errorf("expected tool to have ephemeral cache control, got %v", params.Tools[0].OfTool.CacheControl.Type)
	}
}

func TestToMessageNewParams_Caching_Auto(t *testing.T) {
	req := &llm.Request{
		Model: "claude-3-5-sonnet-latest",
		Extensions: map[string]llm.Extension{
			PromptCaching{}.ExtensionID(): PromptCaching{Auto: true},
		},
		Messages: []message.Message{
			message.NewUserText("Hello Claude"),
		},
	}

	params, err := toMessageNewParams(req)
	if err != nil {
		t.Fatalf("toMessageNewParams failed: %v", err)
	}

	if params.CacheControl.Type != "ephemeral" {
		t.Errorf("expected top-level cache control to be ephemeral, got %v", params.CacheControl.Type)
	}
}

func TestToContentBlocksParamsMultimodal(t *testing.T) {
	content := message.Content{
		&message.TextBlock{Text: "Analyze this image."},
		&message.ImageBlock{
			Data:     []byte("fake-image-data"),
			MIMEType: "image/png",
		},
		&message.DocumentBlock{
			Data:     []byte("fake-pdf-data"),
			MIMEType: "application/pdf",
		},
	}

	blocks := toContentBlocksParams(content, false)
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(blocks))
	}

	// Marshal to check JSON structure
	data, _ := json.Marshal(blocks)
	var got []map[string]any
	json.Unmarshal(data, &got)

	if got[0]["type"] != "text" {
		t.Errorf("expected block 0 to be text, got %v", got[0]["type"])
	}
	if got[1]["type"] != "image" {
		t.Errorf("expected block 1 to be image, got %v", got[1]["type"])
	}
	if got[2]["type"] != "document" {
		t.Errorf("expected block 2 to be document, got %v", got[2]["type"])
	}
}

func TestToMessageNewParams_PendingToolCallsFlushedInOrder(t *testing.T) {
	req := &llm.Request{
		Model: "claude-3-5-sonnet",
		Messages: message.MessageList{
			&message.Assistant{
				Content: message.Content{
					&message.ToolCall{ID: "call_alpha", Name: "tool_alpha"},
					&message.ToolCall{ID: "call_beta", Name: "tool_beta"},
					&message.ToolCall{ID: "call_gamma", Name: "tool_gamma"},
				},
			},
			// Only call_beta was answered
			&message.Tool{
				ToolCallID: "call_beta",
				Name:       "tool_beta",
				Content:    message.Content{&message.TextBlock{Text: "beta result"}},
			},
		},
	}

	params, err := toMessageNewParams(req)
	if err != nil {
		t.Fatalf("toMessageNewParams failed: %v", err)
	}

	// Messages should be:
	// 0: Assistant with 3 tool calls
	// 1: User with tool result for call_beta
	// 2: User with synthetic tool result for call_alpha (first pending)
	// 3: User with synthetic tool result for call_gamma (second pending)
	if len(params.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(params.Messages))
	}

	// Verify order of flushed messages
	msg2 := params.Messages[2]
	if len(msg2.Content) == 0 || msg2.Content[0].OfToolResult == nil || msg2.Content[0].OfToolResult.ToolUseID != "call_alpha" {
		t.Errorf("expected message 2 to be flushed result for call_alpha, got %+v", msg2)
	}

	msg3 := params.Messages[3]
	if len(msg3.Content) == 0 || msg3.Content[0].OfToolResult == nil || msg3.Content[0].OfToolResult.ToolUseID != "call_gamma" {
		t.Errorf("expected message 3 to be flushed result for call_gamma, got %+v", msg3)
	}
}

func TestToMessageNewParams_MidConversationSystemMessage(t *testing.T) {
	req := &llm.Request{
		Model: "claude-3-5-sonnet",
		Messages: message.MessageList{
			&message.System{
				Content: message.Content{&message.TextBlock{Text: "Initial root instructions"}},
			},
			&message.User{
				Content: message.Content{&message.TextBlock{Text: "Hello"}},
			},
			&message.Assistant{
				Content: message.Content{&message.TextBlock{Text: "Hi there"}},
			},
			&message.System{
				Content: message.Content{&message.TextBlock{Text: "Updated mid-conversation instructions"}},
			},
			&message.User{
				Content: message.Content{&message.TextBlock{Text: "Proceed"}},
			},
		},
	}

	params, err := toMessageNewParams(req)
	if err != nil {
		t.Fatalf("toMessageNewParams failed: %v", err)
	}

	// Root system should contain the initial instruction
	if len(params.System) != 1 || params.System[0].Text != "Initial root instructions" {
		t.Errorf("expected initial system in params.System, got %+v", params.System)
	}

	// Messages should be: User, Assistant, System (mid-conversation), User
	if len(params.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(params.Messages))
	}

	midSys := params.Messages[2]
	if midSys.Role != anthropic.MessageParamRoleSystem {
		t.Errorf("expected message 2 to have role 'system', got %q", midSys.Role)
	}
	if len(midSys.Content) != 1 || midSys.Content[0].OfText == nil || midSys.Content[0].OfText.Text != "Updated mid-conversation instructions" {
		t.Errorf("expected mid-conversation system content, got %+v", midSys.Content)
	}
}

func TestToMessageNewParams_DeferredTools(t *testing.T) {
	req := &llm.Request{
		Model: "claude-sonnet-4-6",
		Tools: []tool.Definition{
			{
				Name:         "deferred_calc",
				Description:  "Calculator",
				DeferLoading: true,
			},
			{
				Name:         "normal_search",
				Description:  "Search",
				DeferLoading: false,
			},
		},
	}

	params, err := toMessageNewParams(req)
	if err != nil {
		t.Fatalf("toMessageNewParams failed: %v", err)
	}

	if len(params.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(params.Tools))
	}

	data0, err := json.Marshal(params.Tools[0])
	if err != nil {
		t.Fatalf("marshal tool 0: %v", err)
	}
	var raw0 map[string]any
	if err := json.Unmarshal(data0, &raw0); err != nil {
		t.Fatalf("unmarshal tool 0: %v", err)
	}
	if defLoading, ok := raw0["defer_loading"].(bool); !ok || !defLoading {
		t.Errorf("expected tool 0 to have defer_loading=true, got %v", raw0["defer_loading"])
	}

	data1, err := json.Marshal(params.Tools[1])
	if err != nil {
		t.Fatalf("marshal tool 1: %v", err)
	}
	var raw1 map[string]any
	if err := json.Unmarshal(data1, &raw1); err != nil {
		t.Fatalf("unmarshal tool 1: %v", err)
	}
	if defLoading, ok := raw1["defer_loading"]; ok && defLoading == true {
		t.Errorf("expected tool 1 not to have defer_loading=true, got %v", defLoading)
	}
}

func TestToMessageNewParams_Caching_DeferredAndToolSearch(t *testing.T) {
	t.Run("last non-deferred tool gets cache control", func(t *testing.T) {
		req := &llm.Request{
			Model: "claude-sonnet-4-6",
			Extensions: map[string]llm.Extension{
				PromptCaching{}.ExtensionID(): PromptCaching{CacheHeader: true},
			},
			Tools: []tool.Definition{
				{Name: "tool_1", DeferLoading: false},
				{Name: "tool_2", DeferLoading: false},
				{Name: "tool_3", DeferLoading: true},
			},
		}

		params, err := toMessageNewParams(req)
		if err != nil {
			t.Fatalf("toMessageNewParams failed: %v", err)
		}

		// tool_1 (idx 0) should not have cache_control
		if params.Tools[0].OfTool.CacheControl.Type != "" {
			t.Errorf("expected tool 0 to not have cache control, got %v", params.Tools[0].OfTool.CacheControl.Type)
		}
		// tool_2 (idx 1, last non-deferred) should have cache_control
		if params.Tools[1].OfTool.CacheControl.Type != "ephemeral" {
			t.Errorf("expected tool 1 to have ephemeral cache control, got %v", params.Tools[1].OfTool.CacheControl.Type)
		}
		// tool_3 (idx 2, deferred) must NOT have cache_control
		if params.Tools[2].OfTool.CacheControl.Type != "" {
			t.Errorf("expected deferred tool 2 to not have cache control, got %v", params.Tools[2].OfTool.CacheControl.Type)
		}
	})

	t.Run("all deferred tools get no cache control", func(t *testing.T) {
		req := &llm.Request{
			Model: "claude-sonnet-4-6",
			Extensions: map[string]llm.Extension{
				PromptCaching{}.ExtensionID(): PromptCaching{CacheHeader: true},
			},
			Tools: []tool.Definition{
				{Name: "tool_1", DeferLoading: true},
				{Name: "tool_2", DeferLoading: true},
			},
		}

		params, err := toMessageNewParams(req)
		if err != nil {
			t.Fatalf("toMessageNewParams failed: %v", err)
		}

		for i, tool := range params.Tools {
			if tool.OfTool.CacheControl.Type != "" {
				t.Errorf("tool %d: expected no cache control on deferred tool, got %v", i, tool.OfTool.CacheControl.Type)
			}
		}
	})

	t.Run("tool search gets cache control when enabled", func(t *testing.T) {
		req := &llm.Request{
			Model: "claude-sonnet-4-6",
			Extensions: map[string]llm.Extension{
				PromptCaching{}.ExtensionID(): PromptCaching{CacheHeader: true},
				ToolSearch{}.ExtensionID():    ToolSearch{Strategy: ToolSearchBM25},
			},
			Tools: []tool.Definition{
				{Name: "tool_1", DeferLoading: false},
				{Name: "tool_2", DeferLoading: true},
			},
		}

		params, err := toMessageNewParams(req)
		if err != nil {
			t.Fatalf("toMessageNewParams failed: %v", err)
		}

		if len(params.Tools) != 3 {
			t.Fatalf("expected 3 tools, got %d", len(params.Tools))
		}
		// Neither regular tool should have cache control
		if params.Tools[0].OfTool.CacheControl.Type != "" {
			t.Errorf("expected tool 0 not to have cache control, got %v", params.Tools[0].OfTool.CacheControl.Type)
		}
		if params.Tools[1].OfTool.CacheControl.Type != "" {
			t.Errorf("expected tool 1 not to have cache control, got %v", params.Tools[1].OfTool.CacheControl.Type)
		}
		// The appended ToolSearch tool should have cache control
		if params.Tools[2].OfToolSearchToolBm25_20251119 == nil {
			t.Fatalf("expected tool 2 to be bm25 tool search")
		}
		if params.Tools[2].OfToolSearchToolBm25_20251119.CacheControl.Type != "ephemeral" {
			t.Errorf("expected bm25 tool search to have ephemeral cache control, got %v", params.Tools[2].OfToolSearchToolBm25_20251119.CacheControl.Type)
		}
	})
}

func TestToMessageNewParams_ToolSearch(t *testing.T) {
	t.Run("BM25", func(t *testing.T) {
		req := &llm.Request{
			Model: "claude-sonnet-4-6",
			Extensions: map[string]llm.Extension{
				ToolSearch{}.ExtensionID(): ToolSearch{Strategy: ToolSearchBM25},
			},
		}
		params, err := toMessageNewParams(req)
		if err != nil {
			t.Fatalf("toMessageNewParams failed: %v", err)
		}
		if len(params.Tools) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(params.Tools))
		}
		data, err := json.Marshal(params.Tools[0])
		if err != nil {
			t.Fatalf("marshal tool: %v", err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("unmarshal tool: %v", err)
		}
		if raw["type"] != "tool_search_tool_bm25_20251119" {
			t.Errorf("expected type tool_search_tool_bm25_20251119, got %v", raw["type"])
		}
	})

	t.Run("Regex", func(t *testing.T) {
		req := &llm.Request{
			Model: "claude-sonnet-4-6",
			Extensions: map[string]llm.Extension{
				ToolSearch{}.ExtensionID(): ToolSearch{Strategy: ToolSearchRegex},
			},
		}
		params, err := toMessageNewParams(req)
		if err != nil {
			t.Fatalf("toMessageNewParams failed: %v", err)
		}
		if len(params.Tools) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(params.Tools))
		}
		data, err := json.Marshal(params.Tools[0])
		if err != nil {
			t.Fatalf("marshal tool: %v", err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("unmarshal tool: %v", err)
		}
		if raw["type"] != "tool_search_tool_regex_20251119" {
			t.Errorf("expected type tool_search_tool_regex_20251119, got %v", raw["type"])
		}
	})
}

func TestToMessageNewParams_ToolReferenceInToolResult(t *testing.T) {
	req := &llm.Request{
		Model: "claude-sonnet-4-6",
		Messages: []message.Message{
			&message.Assistant{
				Content: message.Content{
					&message.ToolCall{ID: "call_search", Name: "search_tools"},
				},
			},
			&message.Tool{
				ToolCallID: "call_search",
				Content: message.Content{
					&message.ToolReferenceBlock{ToolName: "calculator"},
				},
			},
		},
	}

	params, err := toMessageNewParams(req)
	if err != nil {
		t.Fatalf("toMessageNewParams failed: %v", err)
	}

	if len(params.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(params.Messages))
	}

	toolMsg := params.Messages[1]
	data, err := json.Marshal(toolMsg)
	if err != nil {
		t.Fatalf("marshal tool message: %v", err)
	}

	var raw struct {
		Role    string `json:"role"`
		Content []struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
			Content   []struct {
				Type     string `json:"type"`
				ToolName string `json:"tool_name"`
			} `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal tool message: %v", err)
	}

	if raw.Role != "user" {
		t.Fatalf("expected role user, got %q", raw.Role)
	}
	if len(raw.Content) != 1 || raw.Content[0].Type != "tool_result" {
		t.Fatalf("expected 1 tool_result block, got %+v", raw.Content)
	}
	if len(raw.Content[0].Content) != 1 {
		t.Fatalf("expected 1 nested content block, got %d", len(raw.Content[0].Content))
	}
	ref := raw.Content[0].Content[0]
	if ref.Type != "tool_reference" || ref.ToolName != "calculator" {
		t.Errorf("expected tool_reference with calculator, got %+v", ref)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestToMessageNewParams_MidConversationToolAddition(t *testing.T) {
	req := &llm.Request{
		Model: "claude-sonnet-4-6",
		Messages: []message.Message{
			&message.User{
				Content: message.Content{&message.TextBlock{Text: "Please calculate 2+2"}},
			},
			&message.System{
				Content: message.Content{
					&message.ToolReferenceBlock{ToolName: "calculator"},
				},
			},
		},
	}

	if !hasMidConversationToolAddition(req) {
		t.Fatal("expected hasMidConversationToolAddition to return true")
	}

	opts := requestOptions(req)
	if len(opts) == 0 {
		t.Fatal("expected request options to contain beta header")
	}

	// Verify header addition via dummy client round trip
	var capturedBetaHeader string
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			capturedBetaHeader = r.Header.Get("anthropic-beta")
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")),
			}, nil
		}),
	}
	client := anthropic.NewClient(
		option.WithAPIKey("test-key"),
		option.WithHTTPClient(httpClient),
	)
	p := NewProvider(&client)
	streamRes, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range streamRes {
	}
	if !strings.Contains(capturedBetaHeader, string(anthropic.AnthropicBetaInlineTools2026_09_15)) {
		t.Errorf("expected anthropic-beta header %q, got %q", anthropic.AnthropicBetaInlineTools2026_09_15, capturedBetaHeader)
	}

	params, err := toMessageNewParams(req)
	if err != nil {
		t.Fatalf("toMessageNewParams failed: %v", err)
	}

	if len(params.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(params.Messages))
	}

	sysMsg := params.Messages[1]
	data, err := json.Marshal(sysMsg)
	if err != nil {
		t.Fatalf("marshal system message: %v", err)
	}

	var raw struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Tool struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"tool"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal system message: %v", err)
	}

	if raw.Role != "system" {
		t.Fatalf("expected role system, got %q", raw.Role)
	}
	if len(raw.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(raw.Content))
	}
	block := raw.Content[0]
	if block.Type != "tool_addition" {
		t.Errorf("expected block type tool_addition, got %q", block.Type)
	}
	if block.Tool.Type != "tool_reference" || block.Tool.Name != "calculator" {
		t.Errorf("expected tool {type: tool_reference, name: calculator}, got %+v", block.Tool)
	}
}

func TestToMessageNewParams_ServerToolCallAndResultInAssistant(t *testing.T) {
	req := &llm.Request{
		Model: "claude-sonnet-4-6",
		Messages: []message.Message{
			&message.Assistant{
				Content: message.Content{
					&message.ServerToolCall{
						ID:   "srv_1",
						Name: "tool_search_tool_bm25",
						Args: map[string]any{"query": "calc"},
					},
					&message.ServerToolResult{
						ToolCallID: "srv_1",
						Name:       "tool_search_tool",
						Content: message.Content{
							&message.ToolReferenceBlock{ToolName: "calc"},
						},
					},
					&message.ServerToolResult{
						ToolCallID: "srv_2",
						Name:       "tool_search_tool",
						IsError:    true,
						Content: message.Content{
							&message.TextBlock{Text: "failed to search"},
						},
						Extras: map[string]any{"error_code": "unavailable"},
					},
				},
			},
		},
	}

	params, err := toMessageNewParams(req)
	if err != nil {
		t.Fatalf("toMessageNewParams failed: %v", err)
	}

	if len(params.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(params.Messages))
	}

	data, err := json.Marshal(params.Messages[0])
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}

	var raw struct {
		Role    string           `json:"role"`
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}

	if len(raw.Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(raw.Content))
	}

	// 1. Server tool use block
	b0 := raw.Content[0]
	if b0["type"] != "server_tool_use" {
		t.Errorf("block 0: expected type server_tool_use, got %v", b0["type"])
	}
	if b0["id"] != "srv_1" {
		t.Errorf("block 0: expected id srv_1, got %v", b0["id"])
	}
	if b0["name"] != "tool_search_tool_bm25" {
		t.Errorf("block 0: expected name tool_search_tool_bm25, got %v", b0["name"])
	}

	// 2. Server tool search result block
	b1 := raw.Content[1]
	if b1["type"] != "tool_search_tool_result" {
		t.Errorf("block 1: expected type tool_search_tool_result, got %v", b1["type"])
	}
	if b1["tool_use_id"] != "srv_1" {
		t.Errorf("block 1: expected tool_use_id srv_1, got %v", b1["tool_use_id"])
	}
	c1, ok := b1["content"].(map[string]any)
	if !ok {
		t.Fatalf("block 1: expected content object, got %T", b1["content"])
	}
	if c1["type"] != "tool_search_tool_search_result" {
		t.Errorf("block 1: expected content type tool_search_tool_search_result, got %v", c1["type"])
	}
	refs, ok := c1["tool_references"].([]any)
	if !ok || len(refs) != 1 {
		t.Fatalf("block 1: expected 1 tool reference, got %+v", c1["tool_references"])
	}
	ref0 := refs[0].(map[string]any)
	if ref0["tool_name"] != "calc" {
		t.Errorf("block 1: expected tool_name calc, got %v", ref0["tool_name"])
	}

	// 3. Server tool search error block
	b2 := raw.Content[2]
	if b2["type"] != "tool_search_tool_result" {
		t.Errorf("block 2: expected type tool_search_tool_result, got %v", b2["type"])
	}
	if b2["tool_use_id"] != "srv_2" {
		t.Errorf("block 2: expected tool_use_id srv_2, got %v", b2["tool_use_id"])
	}
	c2, ok := b2["content"].(map[string]any)
	if !ok {
		t.Fatalf("block 2: expected content object, got %T", b2["content"])
	}
	if c2["type"] != "tool_search_tool_result_error" {
		t.Errorf("block 2: expected content type tool_search_tool_result_error, got %v", c2["type"])
	}
	if c2["error_code"] != "unavailable" {
		t.Errorf("block 2: expected error_code unavailable, got %v", c2["error_code"])
	}
	if c2["error_message"] != "failed to search" {
		t.Errorf("block 2: expected error_message 'failed to search', got %v", c2["error_message"])
	}
}

func TestHandleStreamEvent_ServerToolAggregation(t *testing.T) {
	serverToolIndices := make(map[int]bool)
	agg := message.NewAssistantAggregator()

	// 1. ContentBlockStart: ServerToolUseBlock
	rawStart := `{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srv_call_1","name":"tool_search_tool_bm25","input":{}}}`
	var startEvent anthropic.MessageStreamEventUnion
	if err := json.Unmarshal([]byte(rawStart), &startEvent); err != nil {
		t.Fatalf("unmarshal startEvent: %v", err)
	}
	chunk1, shouldYield1, err := handleStreamEvent(startEvent, serverToolIndices)
	if err != nil {
		t.Fatalf("handleStreamEvent start: %v", err)
	}
	if !shouldYield1 {
		t.Fatal("expected shouldYield1 to be true")
	}
	agg.Add(&chunk1)

	// 2. ContentBlockDelta: InputJSONDelta
	rawDelta := `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"calculator\"}"}}`
	var deltaEvent anthropic.MessageStreamEventUnion
	if err := json.Unmarshal([]byte(rawDelta), &deltaEvent); err != nil {
		t.Fatalf("unmarshal deltaEvent: %v", err)
	}
	chunk2, shouldYield2, err := handleStreamEvent(deltaEvent, serverToolIndices)
	if err != nil {
		t.Fatalf("handleStreamEvent delta: %v", err)
	}
	if !shouldYield2 {
		t.Fatal("expected shouldYield2 to be true")
	}
	agg.Add(&chunk2)

	// 3. ContentBlockStart: ToolSearchToolResultBlock
	rawResult := `{"type":"content_block_start","index":1,"content_block":{"type":"tool_search_tool_result","tool_use_id":"srv_call_1","content":{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"calculator"}]}}}`
	var resultEvent anthropic.MessageStreamEventUnion
	if err := json.Unmarshal([]byte(rawResult), &resultEvent); err != nil {
		t.Fatalf("unmarshal resultEvent: %v", err)
	}
	chunk3, shouldYield3, err := handleStreamEvent(resultEvent, serverToolIndices)
	if err != nil {
		t.Fatalf("handleStreamEvent result: %v", err)
	}
	if !shouldYield3 {
		t.Fatal("expected shouldYield3 to be true")
	}
	agg.Add(&chunk3)

	// Build the aggregated assistant message
	msg, err := agg.Build()
	if err != nil {
		t.Fatalf("agg.Build: %v", err)
	}

	serverCalls := msg.ServerToolCalls()
	if len(serverCalls) != 1 {
		t.Fatalf("expected 1 ServerToolCall, got %d", len(serverCalls))
	}
	stc := serverCalls[0]
	if stc.ID != "srv_call_1" || stc.Name != "tool_search_tool_bm25" {
		t.Errorf("unexpected ServerToolCall: %+v", stc)
	}
	if query, ok := stc.Args["query"].(string); !ok || query != "calculator" {
		t.Errorf("expected arg query=calculator, got %v", stc.Args["query"])
	}

	serverResults := msg.ServerToolResults()
	if len(serverResults) != 1 {
		t.Fatalf("expected 1 ServerToolResult, got %d", len(serverResults))
	}
	str := serverResults[0]
	if str.ToolCallID != "srv_call_1" || str.Name != "tool_search_tool" {
		t.Errorf("unexpected ServerToolResult: %+v", str)
	}
	if len(str.Content) != 1 {
		t.Fatalf("expected 1 content block in result, got %d", len(str.Content))
	}
	ref, ok := str.Content[0].(*message.ToolReferenceBlock)
	if !ok || ref.ToolName != "calculator" {
		t.Errorf("expected *message.ToolReferenceBlock with calculator, got %+v", str.Content[0])
	}
}

func TestFromToolSearchToolResultBlock_Error(t *testing.T) {
	raw := `{"type":"tool_search_tool_result","tool_use_id":"call_err_1","content":{"type":"tool_search_tool_result_error","error_code":"execution_time_exceeded","error_message":"search timed out"}}`
	var block anthropic.ToolSearchToolResultBlock
	if err := json.Unmarshal([]byte(raw), &block); err != nil {
		t.Fatalf("unmarshal error block: %v", err)
	}
	res := fromToolSearchToolResultBlock(block)
	if res.ToolCallID != "call_err_1" {
		t.Errorf("expected tool_use_id call_err_1, got %v", res.ToolCallID)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true")
	}
	if res.Extras["error_code"] != "execution_time_exceeded" {
		t.Errorf("expected error_code execution_time_exceeded, got %v", res.Extras["error_code"])
	}
	if len(res.Content) != 1 || res.Content.Text() != "search timed out" {
		t.Errorf("expected text 'search timed out', got %v", res.Content.Text())
	}
}

func TestModelSupportsToolSearch(t *testing.T) {
	supported := []string{
		"claude-fable-5",
		"claude-fable-5-20261101",
		"claude-mythos-5",
		"claude-mythos-5-1",
		"claude-opus-5",
		"claude-opus-5-5",
		"claude-sonnet-5",
		"claude-haiku-5",
		"claude-opus-4-5",
		"claude-opus-4.5",
		"claude-opus-4-6",
		"claude-opus-4.6",
		"claude-opus-4-7",
		"claude-opus-4.7",
		"claude-opus-4-8",
		"claude-opus-4.8",
		"claude-sonnet-4-5",
		"claude-sonnet-4.5",
		"claude-sonnet-4-6",
		"claude-sonnet-4.6",
		"claude-haiku-4-5",
		"claude-haiku-4.5",
		" Claude-Sonnet-4-6 ",
		"CLAUDE-OPUS-5",
	}

	for _, model := range supported {
		if !modelSupportsToolSearch(model) {
			t.Errorf("expected model %q to support tool search", model)
		}
	}

	unsupported := []string{
		"claude-opus-4-1",
		"claude-opus-4-0",
		"claude-sonnet-4-0",
		"claude-3-5-sonnet-latest",
		"claude-3-opus-20240229",
		"glm-4.5",
		"gpt-4o",
		"",
	}

	for _, model := range unsupported {
		if modelSupportsToolSearch(model) {
			t.Errorf("expected model %q to NOT support tool search", model)
		}
	}
}

func TestToMessageNewParams_UnsupportedModelValidation(t *testing.T) {
	unsupportedModels := []string{"claude-opus-4-1", "claude-3-5-sonnet-latest"}

	for _, model := range unsupportedModels {
		t.Run("ToolSearch with "+model, func(t *testing.T) {
			req := &llm.Request{
				Model: model,
				Extensions: map[string]llm.Extension{
					ToolSearch{}.ExtensionID(): ToolSearch{Strategy: ToolSearchBM25},
				},
			}
			_, err := toMessageNewParams(req)
			if err == nil {
				t.Fatalf("expected error for unsupported model %q with ToolSearch", model)
			}
			expectedMsg := fmt.Sprintf("model %q does not support server-side tool search", model)
			if err.Error() != expectedMsg {
				t.Errorf("got error %q, want %q", err.Error(), expectedMsg)
			}
		})

		t.Run("DeferLoading with "+model, func(t *testing.T) {
			req := &llm.Request{
				Model: model,
				Tools: []tool.Definition{
					{Name: "search_tool", DeferLoading: true},
				},
			}
			_, err := toMessageNewParams(req)
			if err == nil {
				t.Fatalf("expected error for unsupported model %q with DeferLoading", model)
			}
			expectedMsg := fmt.Sprintf("model %q does not support deferred tool loading (tool %q)", model, "search_tool")
			if err.Error() != expectedMsg {
				t.Errorf("got error %q, want %q", err.Error(), expectedMsg)
			}
		})

		t.Run("ToolReferenceBlock direct with "+model, func(t *testing.T) {
			req := &llm.Request{
				Model: model,
				Messages: []message.Message{
					&message.User{
						Content: message.Content{
							&message.ToolReferenceBlock{ToolName: "calc"},
						},
					},
				},
			}
			_, err := toMessageNewParams(req)
			if err == nil {
				t.Fatalf("expected error for unsupported model %q with direct ToolReferenceBlock", model)
			}
			expectedMsg := fmt.Sprintf("model %q does not support tool reference blocks", model)
			if err.Error() != expectedMsg {
				t.Errorf("got error %q, want %q", err.Error(), expectedMsg)
			}
		})

		t.Run("ToolReferenceBlock nested in ServerToolResult with "+model, func(t *testing.T) {
			req := &llm.Request{
				Model: model,
				Messages: []message.Message{
					&message.Assistant{
						Content: message.Content{
							&message.ServerToolResult{
								ToolCallID: "srv_1",
								Name:       "tool_search_tool",
								Content: message.Content{
									&message.ToolReferenceBlock{ToolName: "calc"},
								},
							},
						},
					},
				},
			}
			_, err := toMessageNewParams(req)
			if err == nil {
				t.Fatalf("expected error for unsupported model %q with nested ToolReferenceBlock", model)
			}
			expectedMsg := fmt.Sprintf("model %q does not support tool reference blocks", model)
			if err.Error() != expectedMsg {
				t.Errorf("got error %q, want %q", err.Error(), expectedMsg)
			}
		})
	}
}
