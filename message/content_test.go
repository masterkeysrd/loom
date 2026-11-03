package message_test

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/message"
)

func TestContent_ToolDefinitionBlock_JSONRoundTrip(t *testing.T) {
	origContent := message.Content{
		&message.TextBlock{Text: "Please use this tool:"},
		&message.ToolDefinitionBlock{
			Name:        "calculator",
			Description: "Calculates expressions",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"expr": {Type: "string"},
				},
			},
			Extras: map[string]any{"version": "1.0"},
		},
	}

	data, err := json.Marshal(origContent)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded message.Content
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if len(decoded) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(decoded))
	}

	tdb, ok := decoded[1].(*message.ToolDefinitionBlock)
	if !ok {
		t.Fatalf("expected *message.ToolDefinitionBlock, got %T", decoded[1])
	}
	if tdb.Kind() != message.BlockKindToolDefinition {
		t.Errorf("expected Kind %q, got %q", message.BlockKindToolDefinition, tdb.Kind())
	}
	if tdb.Name != "calculator" || tdb.Description != "Calculates expressions" {
		t.Errorf("unexpected name/desc: %s, %s", tdb.Name, tdb.Description)
	}
	if tdb.InputSchema == nil || tdb.InputSchema.Type != "object" {
		t.Errorf("unexpected input schema: %#v", tdb.InputSchema)
	}
	if tdb.Extras["version"] != "1.0" {
		t.Errorf("unexpected extras: %#v", tdb.Extras)
	}
}
