package message

import "maps"

func CloneAssistantChunk(chunk AssistantChunk) AssistantChunk {
	clone := chunk
	clone.Content = CloneContent(chunk.Content)
	if chunk.Metrics != nil {
		metrics := *chunk.Metrics
		clone.Metrics = &metrics
	}
	return clone
}

func CloneContent(content Content) Content {
	if len(content) == 0 {
		return nil
	}

	clone := make(Content, len(content))
	for i, block := range content {
		clone[i] = CloneBlock(block)
	}
	return clone
}

func CloneBlock(block Block) Block {
	switch current := block.(type) {
	case *TextBlock:
		return &TextBlock{Text: current.Text}
	case *ThinkingBlock:
		return &ThinkingBlock{Thinking: current.Thinking}
	case *ImageBlock:
		clone := &ImageBlock{
			MIMEType: current.MIMEType,
			URL:      current.URL,
		}
		if current.Data != nil {
			clone.Data = make([]byte, len(current.Data))
			copy(clone.Data, current.Data)
		}
		if current.Extras != nil {
			clone.Extras = cloneMap(current.Extras)
		}
		return clone
	case *AudioBlock:
		clone := &AudioBlock{
			MIMEType: current.MIMEType,
			URL:      current.URL,
		}
		if current.Data != nil {
			clone.Data = make([]byte, len(current.Data))
			copy(clone.Data, current.Data)
		}
		if current.Extras != nil {
			clone.Extras = cloneMap(current.Extras)
		}
		return clone
	case *VideoBlock:
		clone := &VideoBlock{
			MIMEType: current.MIMEType,
			URL:      current.URL,
		}
		if current.Data != nil {
			clone.Data = make([]byte, len(current.Data))
			copy(clone.Data, current.Data)
		}
		if current.Extras != nil {
			clone.Extras = cloneMap(current.Extras)
		}
		return clone
	case *DocumentBlock:
		clone := &DocumentBlock{
			MIMEType: current.MIMEType,
			URL:      current.URL,
		}
		if current.Data != nil {
			clone.Data = make([]byte, len(current.Data))
			copy(clone.Data, current.Data)
		}
		if current.Extras != nil {
			clone.Extras = cloneMap(current.Extras)
		}
		return clone
	case *ToolCall:
		clone := &ToolCall{
			ID:    current.ID,
			Name:  current.Name,
			Index: current.Index,
		}
		if current.Args != nil {
			clone.Args = cloneMap(current.Args)
		}
		if current.Extras != nil {
			clone.Extras = cloneMap(current.Extras)
		}
		return clone
	case *ToolCallChunk:
		return &ToolCallChunk{
			Index:     current.Index,
			ID:        current.ID,
			Name:      current.Name,
			ArgsChunk: current.ArgsChunk,
		}
	default:
		return block
	}
}

func cloneMap(input map[string]any) map[string]any {
	clone := make(map[string]any, len(input))
	maps.Copy(clone, input)
	return clone
}

// CloneMessage returns a deep copy of the given Message.
func CloneMessage(msg Message) Message {
	if msg == nil {
		return nil
	}

	switch m := msg.(type) {
	case *System:
		return &System{
			Base:    cloneBase(m.Base),
			Content: CloneContent(m.Content),
		}
	case *User:
		return &User{
			Base:    cloneBase(m.Base),
			Content: CloneContent(m.Content),
		}
	case *Assistant:
		return &Assistant{
			Base:    cloneBase(m.Base),
			Content: CloneContent(m.Content),
		}
	case *Tool:
		return &Tool{
			Base:              cloneBase(m.Base),
			ToolCallID:        m.ToolCallID,
			Name:              m.Name,
			Content:           CloneContent(m.Content),
			IsError:           m.IsError,
			StructuredContent: m.StructuredContent,
		}
	default:
		return msg
	}
}

func cloneBase(b Base) Base {
	clone := Base{
		ID: b.ID,
	}
	if b.Metadata != nil {
		clone.Metadata = cloneMap(b.Metadata)
	}
	if b.Extensions != nil {
		clone.Extensions = make(ExtensionMap, len(b.Extensions))
		maps.Copy(clone.Extensions, b.Extensions)
	}
	return clone
}
