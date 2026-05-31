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
