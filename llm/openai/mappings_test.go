package loomopenai

import (
	"encoding/json"
	"testing"

	"github.com/masterkeysrd/loom/message"
)

func TestToAssistantMessageParamToolCallsUseNullContent(t *testing.T) {
	msg := &message.Assistant{
		Content: message.Content{
			&message.ToolCall{
				ID:   "call_123",
				Name: "lookup",
				Args: map[string]any{"query": "loom"},
			},
		},
	}

	param := toAssistantMessageParam(msg)
	data, err := json.Marshal(param)
	if err != nil {
		t.Fatalf("marshal assistant message: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal assistant message: %v", err)
	}

	if _, ok := got["content"]; !ok {
		t.Fatalf("expected explicit content field, got JSON %s", string(data))
	}
	if got["content"] != nil {
		t.Fatalf("expected content to be null, got %#v", got["content"])
	}

	toolCalls, ok := got["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", got["tool_calls"])
	}
}

func TestToAssistantMessageParamPreservesTextWithToolCalls(t *testing.T) {
	msg := &message.Assistant{
		Content: message.Content{
			&message.TextBlock{Text: "I will call a tool."},
			&message.ToolCall{
				ID:   "call_123",
				Name: "lookup",
				Args: map[string]any{"query": "loom"},
			},
		},
	}

	param := toAssistantMessageParam(msg)
	data, err := json.Marshal(param)
	if err != nil {
		t.Fatalf("marshal assistant message: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal assistant message: %v", err)
	}

	if got["content"] != "I will call a tool." {
		t.Fatalf("expected text content, got %#v", got["content"])
	}
}

func TestToUserMessageParamMultimodal(t *testing.T) {
	msg := &message.User{
		Content: message.Content{
			&message.TextBlock{Text: "Analyze this image."},
			&message.ImageBlock{
				Data:     []byte("fake-image-data"),
				MIMEType: "image/png",
			},
		},
	}

	param, err := toUserMessageParam(msg)
	if err != nil {
		t.Fatalf("toUserMessageParam failed: %v", err)
	}

	data, err := json.Marshal(param)
	if err != nil {
		t.Fatalf("marshal user message: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal user message: %v", err)
	}

	content, ok := got["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("expected 2 content parts, got %#v", got["content"])
	}

	if content[0].(map[string]any)["type"] != "text" {
		t.Errorf("expected first part to be text, got %#v", content[0])
	}
	if content[1].(map[string]any)["type"] != "image_url" {
		t.Errorf("expected second part to be image_url, got %#v", content[1])
	}
}

func TestToUserMessageParamDocument(t *testing.T) {
	msg := &message.User{
		Content: message.Content{
			&message.DocumentBlock{
				URL: "file-123",
			},
		},
	}

	param, err := toUserMessageParam(msg)
	if err != nil {
		t.Fatalf("toUserMessageParam failed: %v", err)
	}

	data, err := json.Marshal(param)
	if err != nil {
		t.Fatalf("marshal user message: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal user message: %v", err)
	}

	content, ok := got["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("expected 1 content part, got %#v", got["content"])
	}

	if content[0].(map[string]any)["type"] != "file" {
		t.Errorf("expected part to be file, got %#v", content[0])
	}
	file := content[0].(map[string]any)["file"].(map[string]any)
	if file["file_id"] != "file-123" {
		t.Errorf("expected file_id to be file-123, got %#v", file["file_id"])
	}
}
