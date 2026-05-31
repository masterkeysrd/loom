package loomgenai

import (
	"reflect"
	"testing"

	"github.com/masterkeysrd/loom/message"
	"google.golang.org/genai"
)

func TestToModelParts(t *testing.T) {
	signature := []byte("test-signature")
	encodedSignature := encodeThoughtSignature(signature)

	content := message.Content{
		&message.ThinkingBlock{
			Thinking: "thinking content",
			Extras: map[string]any{
				ThoughtSignatureKey: encodedSignature,
			},
		},
		&message.TextBlock{
			Text: "final answer",
			Extras: map[string]any{
				ThoughtSignatureKey: encodedSignature,
			},
		},
		&message.ToolCall{
			ID:   "call_123",
			Name: "test_tool",
			Args: map[string]any{"arg1": "val1"},
			Extras: map[string]any{
				ThoughtSignatureKey: encodedSignature,
			},
		},
	}

	parts, err := toModelParts(content)
	if err != nil {
		t.Fatalf("toModelParts failed: %v", err)
	}

	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}

	// Check ThinkingBlock part
	if parts[0].Text != "thinking content" || !parts[0].Thought {
		t.Errorf("parts[0] mismatch: Text=%q, Thought=%v", parts[0].Text, parts[0].Thought)
	}
	if !reflect.DeepEqual(parts[0].ThoughtSignature, signature) {
		t.Errorf("parts[0] ThoughtSignature mismatch: expected %v, got %v", signature, parts[0].ThoughtSignature)
	}

	// Check TextBlock part
	if parts[1].Text != "final answer" || parts[1].Thought {
		t.Errorf("parts[1] mismatch: Text=%q, Thought=%v", parts[1].Text, parts[1].Thought)
	}
	if !reflect.DeepEqual(parts[1].ThoughtSignature, signature) {
		t.Errorf("parts[1] ThoughtSignature mismatch: expected %v, got %v", signature, parts[1].ThoughtSignature)
	}

	// Check ToolCall part
	if parts[2].FunctionCall == nil || parts[2].FunctionCall.ID != "call_123" {
		t.Errorf("parts[2] FunctionCall mismatch")
	}
	if !reflect.DeepEqual(parts[2].ThoughtSignature, signature) {
		t.Errorf("parts[2] ThoughtSignature mismatch: expected %v, got %v", signature, parts[2].ThoughtSignature)
	}
}

func TestToModelParts_DummySignature(t *testing.T) {
	content := message.Content{
		&message.ToolCall{
			ID:   "call_1",
			Name: "tool1",
		},
		&message.ToolCall{
			ID:   "call_2",
			Name: "tool2",
		},
	}

	parts, err := toModelParts(content)
	if err != nil {
		t.Fatalf("toModelParts failed: %v", err)
	}

	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}

	// First ToolCall should have dummy signature
	decodedDummy, _ := decodeThoughtSignature(DummyThoughtSignature)
	if !reflect.DeepEqual(parts[0].ThoughtSignature, decodedDummy) {
		t.Errorf("parts[0] expected dummy signature, got %v", parts[0].ThoughtSignature)
	}

	// Second ToolCall should NOT have a signature (only first one needs it if missing)
	if len(parts[1].ThoughtSignature) > 0 {
		t.Errorf("parts[1] should not have a signature, got %q", string(parts[1].ThoughtSignature))
	}
}

func TestToAssistantChunk(t *testing.T) {
	signature := []byte("test-signature")
	encodedSignature := encodeThoughtSignature(signature)

	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{
							Text:             "thinking content",
							Thought:          true,
							ThoughtSignature: signature,
						},
						{
							Text:             "final answer",
							ThoughtSignature: signature,
						},
						{
							FunctionCall: &genai.FunctionCall{
								ID:   "call_123",
								Name: "test_tool",
								Args: map[string]any{"arg1": "val1"},
							},
							ThoughtSignature: signature,
						},
					},
				},
			},
		},
	}

	chunk, err := toAssistantChunk(resp)
	if err != nil {
		t.Fatalf("toAssistantChunk failed: %v", err)
	}

	if len(chunk.Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(chunk.Content))
	}

	// Check ThinkingBlock
	tb, ok := chunk.Content[0].(*message.ThinkingBlock)
	if !ok {
		t.Fatalf("expected ThinkingBlock, got %T", chunk.Content[0])
	}
	if tb.Thinking != "thinking content" {
		t.Errorf("ThinkingBlock mismatch: expected %q, got %q", "thinking content", tb.Thinking)
	}
	if tb.Extras[ThoughtSignatureKey] != encodedSignature {
		t.Errorf("ThinkingBlock signature mismatch: expected %q, got %q", encodedSignature, tb.Extras[ThoughtSignatureKey])
	}

	// Check TextBlock
	txt, ok := chunk.Content[1].(*message.TextBlock)
	if !ok {
		t.Fatalf("expected TextBlock, got %T", chunk.Content[1])
	}
	if txt.Text != "final answer" {
		t.Errorf("TextBlock mismatch: expected %q, got %q", "final answer", txt.Text)
	}
	if txt.Extras[ThoughtSignatureKey] != encodedSignature {
		t.Errorf("TextBlock signature mismatch: expected %q, got %q", encodedSignature, txt.Extras[ThoughtSignatureKey])
	}

	// Check ToolCall
	tc, ok := chunk.Content[2].(*message.ToolCall)
	if !ok {
		t.Fatalf("expected ToolCall, got %T", chunk.Content[2])
	}
	if tc.ID != "call_123" {
		t.Errorf("ToolCall ID mismatch: expected %q, got %q", "call_123", tc.ID)
	}
	if tc.Extras[ThoughtSignatureKey] != encodedSignature {
		t.Errorf("ToolCall signature mismatch: expected %q, got %q", encodedSignature, tc.Extras[ThoughtSignatureKey])
	}
}

func TestToUserPartsMultimodal(t *testing.T) {
	content := message.Content{
		&message.TextBlock{Text: "Look at this image"},
		&message.ImageBlock{
			Data:     []byte("data"),
			MIMEType: "image/png",
		},
	}

	parts, err := toUserParts(content)
	if err != nil {
		t.Fatalf("toUserParts failed: %v", err)
	}

	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}

	if parts[0].Text != "Look at this image" {
		t.Errorf("expected parts[0] to be text, got %q", parts[0].Text)
	}
	if parts[1].InlineData == nil || parts[1].InlineData.MIMEType != "image/png" {
		t.Errorf("expected parts[1] to be inline data image/png, got %#v", parts[1].InlineData)
	}
}
