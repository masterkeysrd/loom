package message

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
