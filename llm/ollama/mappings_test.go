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
