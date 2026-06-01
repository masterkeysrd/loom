package message

// User is the message type that represents input from the human participant
// in the conversation.
type User struct {
	Base `json:",inline"`

	Content Content `json:"content"`
}

// NewUser constructs a [User] message with the given content blocks.
func NewUser(content ...Block) *User {
	return &User{
		Base:    Base{},
		Content: content,
	}
}

// NewUserText is a convenience constructor that creates a [User] message
// containing a single text block.
func NewUserText(text string) *User {
	return NewUser(
		&TextBlock{Text: text},
	)
}

// NewUserTextMeta creates a [User] message with a single text block and
// the given metadata attached.
func NewUserTextMeta(text string, meta map[string]any) *User {
	return &User{
		Base:    Base{Metadata: meta},
		Content: Content{&TextBlock{Text: text}},
	}
}

// NewUserImage creates a [User] message with a single image block.
func NewUserImage(data []byte, mimeType string) *User {
	return NewUser(&ImageBlock{Data: data, MIMEType: mimeType})
}

// NewUserImageURL creates a [User] message with a single image URL.
func NewUserImageURL(url string) *User {
	return NewUser(&ImageBlock{URL: url})
}

// NewUserDocument creates a [User] message with a single document block.
func NewUserDocument(data []byte, mimeType string) *User {
	return NewUser(&DocumentBlock{Data: data, MIMEType: mimeType})
}

// Role returns [RoleUser].
func (u *User) Role() Role {
	return RoleUser
}

// GetContent returns the content blocks of the user message.
func (u *User) GetContent() Content {
	return u.Content
}

// WithExtension returns the message with the given provider-specific extension set.
func (u *User) WithExtension(ext Extension) *User {
	u.Base.AddExtension(ext)
	return u
}

func (u *User) isMessage() {}
