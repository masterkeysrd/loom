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
