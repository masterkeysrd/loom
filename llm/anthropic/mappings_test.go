package loomanthropic

import (
	"encoding/json"
	"testing"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
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

	blocks := toContentBlocksParams(content)
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
