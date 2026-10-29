package message

import (
	"encoding/json"
	"testing"
)

type sampleStruct struct {
	Zebra string `json:"zebra"`
	Apple string `json:"apple"`
	Count int64  `json:"count"`
}

func TestTool_MarshalJSON_CanonicalStructuredContent(t *testing.T) {
	toolMsg := &Tool{
		ToolCallID:        "tc_1",
		Name:              "test_tool",
		StructuredContent: sampleStruct{Zebra: "stripes", Apple: "fruit", Count: 100},
	}

	beforeBytes, err := json.Marshal(toolMsg)
	if err != nil {
		t.Fatalf("Marshal before checkpoint failed: %v", err)
	}

	// Simulate checkpoint round trip
	var restored Tool
	if err := json.Unmarshal(beforeBytes, &restored); err != nil {
		t.Fatalf("Unmarshal checkpoint failed: %v", err)
	}

	afterBytes, err := json.Marshal(&restored)
	if err != nil {
		t.Fatalf("Marshal after checkpoint failed: %v", err)
	}

	if string(beforeBytes) != string(afterBytes) {
		t.Errorf("Tool JSON differed before and after round-trip:\nBefore: %s\nAfter:  %s", string(beforeBytes), string(afterBytes))
	}

	canonBefore, err := toolMsg.CanonicalStructuredContent()
	if err != nil {
		t.Fatalf("CanonicalStructuredContent before failed: %v", err)
	}

	canonAfter, err := restored.CanonicalStructuredContent()
	if err != nil {
		t.Fatalf("CanonicalStructuredContent after failed: %v", err)
	}

	if string(canonBefore) != string(canonAfter) {
		t.Errorf("CanonicalStructuredContent differed before and after round-trip:\nBefore: %s\nAfter:  %s", string(canonBefore), string(canonAfter))
	}
}
