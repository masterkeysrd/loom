package message

var _ Message = (*System)(nil)

// System is the message type that carries instructions from the application
// developer to the LLM, typically placed at the beginning of the conversation.
type System struct {
	Base `json:",inline"`

	Content Content `json:"content"`
}

// NewSystem constructs a [System] message with the given content blocks.
func NewSystem(content ...Block) *System {
	return &System{
		Base:    Base{},
		Content: content,
	}
}

// NewSystemText is a convenience constructor that creates a [System] message
// containing a single text block.
func NewSystemText(text string) *System {
	return NewSystem(
		&TextBlock{Text: text},
	)
}

// Role returns [RoleSystem].
func (s *System) Role() Role {
	return RoleSystem
}

// GetContent returns the content blocks of the system message.
func (s *System) GetContent() Content {
	return s.Content
}

func (s *System) isMessage() {}
