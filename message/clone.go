package message

import (
	"maps"
	"slices"
)

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
		return &TextBlock{
			Text:   current.Text,
			Extras: maps.Clone(current.Extras),
		}
	case *ThinkingBlock:
		return &ThinkingBlock{
			Thinking: current.Thinking,
			Extras:   maps.Clone(current.Extras),
		}
	case *ImageBlock:
		return &ImageBlock{
			Data:     slices.Clone(current.Data),
			MIMEType: current.MIMEType,
			URL:      current.URL,
			Extras:   maps.Clone(current.Extras),
		}
	case *AudioBlock:
		return &AudioBlock{
			Data:     slices.Clone(current.Data),
			MIMEType: current.MIMEType,
			URL:      current.URL,
			Extras:   maps.Clone(current.Extras),
		}
	case *VideoBlock:
		return &VideoBlock{
			Data:     slices.Clone(current.Data),
			MIMEType: current.MIMEType,
			URL:      current.URL,
			Extras:   maps.Clone(current.Extras),
		}
	case *DocumentBlock:
		return &DocumentBlock{
			Data:     slices.Clone(current.Data),
			MIMEType: current.MIMEType,
			URL:      current.URL,
			Extras:   maps.Clone(current.Extras),
		}
	case *ToolCall:
		return &ToolCall{
			ID:     current.ID,
			Name:   current.Name,
			Index:  current.Index,
			Args:   maps.Clone(current.Args),
			Extras: maps.Clone(current.Extras),
		}
	case *ToolCallChunk:
		return &ToolCallChunk{
			Index:     current.Index,
			ID:        current.ID,
			Name:      current.Name,
			ArgsChunk: current.ArgsChunk,
			Extras:    maps.Clone(current.Extras),
		}
	case *ServerToolCall:
		return &ServerToolCall{
			ID:     current.ID,
			Name:   current.Name,
			Index:  current.Index,
			Args:   maps.Clone(current.Args),
			Extras: maps.Clone(current.Extras),
		}
	case *ServerToolCallChunk:
		return &ServerToolCallChunk{
			Index:     current.Index,
			ID:        current.ID,
			Name:      current.Name,
			ArgsChunk: current.ArgsChunk,
			Extras:    maps.Clone(current.Extras),
		}
	case *ServerToolResult:
		clone := &ServerToolResult{
			ToolCallID: current.ToolCallID,
			Name:       current.Name,
			Content:    CloneContent(current.Content),
			Output:     current.Output,
			IsError:    current.IsError,
			Extras:     maps.Clone(current.Extras),
		}
		if m, ok := current.Output.(map[string]any); ok {
			clone.Output = maps.Clone(m)
		}
		return clone
	case *ToolReferenceBlock:
		return &ToolReferenceBlock{
			ToolName: current.ToolName,
			Extras:   maps.Clone(current.Extras),
		}
	case *ToolDefinitionBlock:
		return &ToolDefinitionBlock{
			Name:        current.Name,
			Description: current.Description,
			InputSchema: current.InputSchema,
			Extras:      maps.Clone(current.Extras),
		}
	default:
		return block
	}
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
		var metrics *TokenMetrics
		if m.Metrics != nil {
			mc := *m.Metrics
			metrics = &mc
		}
		return &Assistant{
			Base:       cloneBase(m.Base),
			Content:    CloneContent(m.Content),
			Metrics:    metrics,
			Done:       m.Done,
			DoneReason: m.DoneReason,
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
	return Base{
		ID:         b.ID,
		Metadata:   maps.Clone(b.Metadata),
		Extensions: maps.Clone(b.Extensions),
	}
}
