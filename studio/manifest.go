package studio

import (
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
)

// Manifest represents the discovery message sent by a Go worker to the Studio.
type Manifest struct {
	Type     string          `json:"type"` // "manifest"
	WorkerID string          `json:"worker_id"`
	Graphs   []GraphManifest `json:"graphs"`
}

// GraphManifest describes a single graph's topology and schemas.
type GraphManifest struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	MermaidDiagram string              `json:"mermaid_diagram"`
	InputSchema    *jsonschema.Schema  `json:"input_schema"`
	Commands       []CommandDefinition `json:"commands"`
}

// CommandDefinition describes a custom command supported by the graph.
type CommandDefinition struct {
	Name   string             `json:"name"`
	Schema *jsonschema.Schema `json:"schema"`
}

// Message represents a generic message sent over the control channel.
type Message struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}
