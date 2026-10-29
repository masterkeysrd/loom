package message

import (
	"encoding/json"

	"github.com/masterkeysrd/loom/internal/jsonutils"
)

// Tool represents the result of a tool call returned by the application.
// It mirrors the structure expected by most LLM providers when reporting
// tool execution results back into the conversation.
type Tool struct {
	Base `json:",inline"`

	ToolCallID string  `json:"tool_call_id"`
	Name       string  `json:"name"`
	Content    Content `json:"content"`

	// IsError indicates if the tool execution resulted in a functional error.
	IsError bool `json:"is_error,omitempty"`

	// Structured holds the original Go value returned by the tool handler.
	StructuredContent any `json:"structured_content,omitempty"`
}

type toolShadow Tool

// MarshalJSON serializes Tool to JSON, canonicalizing StructuredContent per RFC 8785
// when present so that serialized bytes are deterministic across checkpoint round-trips.
func (t *Tool) MarshalJSON() ([]byte, error) {
	if t.StructuredContent == nil {
		return json.Marshal((*toolShadow)(t))
	}

	canonicalData, err := jsonutils.Canonical(t.StructuredContent)
	if err != nil {
		return nil, err
	}

	type toolWire struct {
		*toolShadow
		StructuredContent json.RawMessage `json:"structured_content,omitempty"`
	}

	return json.Marshal(toolWire{
		toolShadow:        (*toolShadow)(t),
		StructuredContent: json.RawMessage(canonicalData),
	})
}

// CanonicalStructuredContent returns the RFC 8785 canonical JSON bytes of StructuredContent.
func (t *Tool) CanonicalStructuredContent() ([]byte, error) {
	if t.StructuredContent == nil {
		return nil, nil
	}
	return jsonutils.Canonical(t.StructuredContent)
}

// Role returns [RoleTool].
func (t *Tool) Role() Role {
	return RoleTool
}

// GetContent returns the text content of the tool result as seen by the LLM.
func (t *Tool) GetContent() Content {
	return t.Content
}

// WithExtension returns the message with the given provider-specific extension set.
func (t *Tool) WithExtension(ext Extension) *Tool {
	t.AddExtension(ext)
	return t
}

func (t *Tool) isMessage() {}
