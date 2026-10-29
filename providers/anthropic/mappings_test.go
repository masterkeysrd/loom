package loomanthropic

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
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


