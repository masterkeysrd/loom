package loomollama

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
)

func TestToChatRequestStructuredOutput(t *testing.T) {
	schema, _ := jsonschema.For[map[string]string](nil)

	req := &llm.Request{
		Model:          "llama3",
		ResponseSchema: schema,
	}

	ollamaReq, err := toChatRequest(req)
	if err != nil {
		t.Fatalf("toChatRequest failed: %v", err)
	}

	if len(ollamaReq.Format) == 0 {
		t.Fatal("Format should be set")
	}

	var gotSchema jsonschema.Schema
	if err := json.Unmarshal(ollamaReq.Format, &gotSchema); err != nil {
		t.Fatalf("failed to unmarshal format as schema: %v", err)
	}
}

func TestToChatRequestJSONMode(t *testing.T) {
	req := &llm.Request{
		Model:          "llama3",
		ResponseFormat: "json_object",
	}

	ollamaReq, err := toChatRequest(req)
	if err != nil {
		t.Fatalf("toChatRequest failed: %v", err)
	}

	if string(ollamaReq.Format) != `"json"` {
		t.Errorf("expected Format to be \"json\", got %s", string(ollamaReq.Format))
	}
}

func TestToChatRequestMaxTokensAndContextWindow(t *testing.T) {
	req := &llm.Request{
		Model:         "llama3",
		MaxTokens:     100,
		ContextWindow: 8192,
	}

	ollamaReq, err := toChatRequest(req)
	if err != nil {
		t.Fatalf("toChatRequest failed: %v", err)
	}

	if val, ok := ollamaReq.Options["num_predict"].(int); !ok || val != 100 {
		t.Errorf("expected num_predict to be 100, got %v", ollamaReq.Options["num_predict"])
	}

	if val, ok := ollamaReq.Options["num_ctx"].(int); !ok || val != 8192 {
		t.Errorf("expected num_ctx to be 8192, got %v", ollamaReq.Options["num_ctx"])
	}
}

func TestToChatRequestThinkingToggle(t *testing.T) {
	// Test thinking enabled
	reqEnabled := &llm.Request{
		Model: "llama3",
		Thinking: &llm.ThinkingConfig{
			Enabled: true,
		},
	}
	ollamaReqEnabled, err := toChatRequest(reqEnabled)
	if err != nil {
		t.Fatalf("toChatRequest failed: %v", err)
	}
	if ollamaReqEnabled.Think == nil || ollamaReqEnabled.Think.Value != true {
		t.Errorf("expected Think to be true, got %v", ollamaReqEnabled.Think)
	}

	// Test thinking disabled
	reqDisabled := &llm.Request{
		Model: "llama3",
		Thinking: &llm.ThinkingConfig{
			Enabled: false,
		},
	}
	ollamaReqDisabled, err := toChatRequest(reqDisabled)
	if err != nil {
		t.Fatalf("toChatRequest failed: %v", err)
	}
	if ollamaReqDisabled.Think == nil || ollamaReqDisabled.Think.Value != false {
		t.Errorf("expected Think to be false, got %v", ollamaReqDisabled.Think)
	}
}

func TestToAPIToolCall_DeterministicArgumentOrder(t *testing.T) {
	tc := &message.ToolCall{
		ID:   "call_1",
		Name: "test_fn",
		Args: map[string]any{
			"alpha":   1,
			"beta":    2,
			"gamma":   3,
			"delta":   4,
			"epsilon": 5,
			"zeta":    6,
			"eta":     7,
			"theta":   8,
			"iota":    9,
			"kappa":   10,
		},
	}

	firstBytes, err := json.Marshal(toAPIToolCall(tc).Function.Arguments)
	if err != nil {
		t.Fatalf("failed to marshal first arguments: %v", err)
	}

	for i := range 50 {
		iterBytes, err := json.Marshal(toAPIToolCall(tc).Function.Arguments)
		if err != nil {
			t.Fatalf("failed to marshal iter arguments: %v", err)
		}
		if string(firstBytes) != string(iterBytes) {
			t.Fatalf("expected deterministic argument ordering across iterations, but iteration %d differed:\nFirst: %s\nIter:  %s", i, string(firstBytes), string(iterBytes))
		}
	}
}
