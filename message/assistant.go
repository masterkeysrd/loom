package message

var _ Message = (*Assistant)(nil)

// Assistant is the message type that represents a response from the AI model.
type Assistant struct {
	Base `json:",inline"`

	Content    Content       `json:"content"`
	Metrics    *TokenMetrics `json:"metrics,omitempty"`
	Done       bool          `json:"done"`
	DoneReason string        `json:"done_reason,omitempty"`
}

func NewAssistantText(text string) *Assistant {
	return &Assistant{
		Content: Content{
			&TextBlock{Text: text},
		},
	}
}

// Role returns [RoleAssistant].
func (a *Assistant) Role() Role {
	return RoleAssistant
}

// GetContent returns the content blocks of the assistant message.
func (a *Assistant) GetContent() Content {
	return a.Content
}

// WithExtension returns the message with the given provider-specific extension set.
func (a *Assistant) WithExtension(ext Extension) *Assistant {
	a.Base.AddExtension(ext)
	return a
}

func (a *Assistant) isMessage() {}

// ToolCalls returns all [ToolCall] blocks present in the assistant's content.
func (a *Assistant) ToolCalls() []*ToolCall {
	var calls []*ToolCall
	for _, b := range a.Content {
		if tc, ok := b.(*ToolCall); ok {
			calls = append(calls, tc)
		}
	}
	return calls
}
