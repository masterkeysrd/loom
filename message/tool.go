package message

// Tool represents the result of a tool call returned by the application.
// It mirrors the structure expected by most LLM providers when reporting
// tool execution results back into the conversation.
type Tool struct {
	Base `json:",inline"`

	ToolCallID string  `json:"tool_call_id"`
	Name       string  `json:"name"`
	Content    Content `json:"content"`

	// Structured holds the original Go value returned by the tool handler.
	StructuredContent any `json:"structured_content,omitempty"`
}

// Role returns [RoleTool].
func (t *Tool) Role() Role {
	return RoleTool
}

// GetContent returns the text content of the tool result as seen by the LLM.
func (t *Tool) GetContent() Content {
	return t.Content
}

func (t *Tool) isMessage() {}
