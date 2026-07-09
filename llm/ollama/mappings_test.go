package loomollama

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/llm"
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
