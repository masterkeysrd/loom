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
	}
	assistantCloned := CloneMessage(assistant).(*Assistant)
	if assistantCloned == assistant {
		t.Error("expected a new pointer, got same")
	}
	if !reflect.DeepEqual(assistantCloned, assistant) {
		t.Errorf("expected cloned assistant message to equal original, got %v", assistantCloned)
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
