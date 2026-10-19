package message

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Content is an ordered sequence of [Block] values that makes up the body of a message.
// Most messages contain a single [TextBlock], but assistant responses may also
// include [ToolCall] blocks when the model requests tool invocations.
type Content []Block

func (c Content) Text() string {
	var sb strings.Builder
	first := true
	for _, block := range c {
		if textBlock, ok := block.(*TextBlock); ok {
			if !first {
				sb.WriteString("\n")
			}
			sb.WriteString(textBlock.Text)
			first = false
		}
	}
	return sb.String()
}

func (c Content) Thought() string {
	var sb strings.Builder
	first := true
	for _, block := range c {
		if thinkingBlock, ok := block.(*ThinkingBlock); ok {
			if !first {
				sb.WriteString("\n")
			}
			sb.WriteString(thinkingBlock.Thinking)
			first = false
		}
	}
	return sb.String()
}

// MarshalJSON customizes the JSON encoding of Content to include the block kind.
//
// The output will have the following structure:
// [
//
//	{
//	  "kind": "text",
//	  "field1": "value1",
//	  "field2": "value2"
//	},
//	...
//
// ]
func (c Content) MarshalJSON() ([]byte, error) {
	blocks := make([]json.RawMessage, len(c))
	for i, block := range c {
		data, err := json.Marshal(block)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal block: %w", err)
		}

		buf := make([]byte, 0, len(data)+len(block.Kind())+20) // extra space for kind and JSON overhead
		buf = append(buf, '{')
		buf = append(buf, fmt.Sprintf(`"kind":"%s",`, block.Kind())...)
		if len(data) > 2 { // if the marshaled block is not an empty JSON object
			buf = append(buf, data[1:len(data)-1]...) // strip the outer braces
		}
		buf = append(buf, '}')

		blocks[i] = buf
	}

	return json.Marshal(blocks)
}

// UnmarshalJSON decodes a JSON array of typed block objects back into a [Content]
// slice. Each element must carry a "kind" discriminator field that selects the
// concrete [Block] implementation used for decoding.
func (c *Content) UnmarshalJSON(data []byte) error {
	var rawBlocks []json.RawMessage
	if err := json.Unmarshal(data, &rawBlocks); err != nil {
		return fmt.Errorf("failed to unmarshal content: %w", err)
	}

	var blocks []Block
	for _, raw := range rawBlocks {
		var header struct {
			Kind BlockKind `json:"kind"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			return fmt.Errorf("failed to unmarshal block header: %w", err)
		}

		var block Block
		switch header.Kind {
		case BlockKindText:
			block = &TextBlock{}
		case BlockKindToolCall:
			block = &ToolCall{}
		case BlockKindThinking:
			block = &ThinkingBlock{}
		case BlockKindImage:
			block = &ImageBlock{}
		case BlockKindAudio:
			block = &AudioBlock{}
		case BlockKindVideo:
			block = &VideoBlock{}
		case BlockKindDocument:
			block = &DocumentBlock{}
		default:
			return fmt.Errorf("unknown block kind: %s", header.Kind)
		}

		if err := json.Unmarshal(raw, block); err != nil {
			return fmt.Errorf("failed to unmarshal block: %w", err)
		}

		blocks = append(blocks, block)
	}

	*c = blocks
	return nil
}

// Block is the sealed content unit that can appear inside a [Content] slice.
// Concrete implementations are [TextBlock], [ToolCall], and [ThinkingBlock].
type Block interface {
	Kind() BlockKind
	isBlock()
}

// BlockKind is the discriminator value embedded in serialised blocks so that
// [Content.UnmarshalJSON] can reconstruct the correct concrete type.
type BlockKind string

const (
	BlockKindText          BlockKind = "text"
	BlockKindToolCall      BlockKind = "tool_call"
	BlockKindToolCallChunk BlockKind = "tool_call_chunk"
	BlockKindThinking      BlockKind = "thinking"
	BlockKindImage         BlockKind = "image"
	BlockKindAudio         BlockKind = "audio"
	BlockKindVideo         BlockKind = "video"
	BlockKindDocument      BlockKind = "document"
)

// TextBlock is a plain-text content block. It is the most common block type,
// used for human messages, system prompts, and LLM text responses.
type TextBlock struct {
	Text   string         `json:"text"`
	Extras map[string]any `json:"extras,omitempty"`
}

// Kind returns [BlockKindText], identifying this block in serialised content.
func (b *TextBlock) Kind() BlockKind {
	return BlockKindText
}

func (b *TextBlock) isBlock() {}

// ImageBlock carries image data or a URL to an image.
type ImageBlock struct {
	Data     []byte         `json:"data,omitempty"`
	MIMEType string         `json:"mime_type,omitempty"`
	URL      string         `json:"url,omitempty"`
	Extras   map[string]any `json:"extras,omitempty"`
}

// Kind returns [BlockKindImage], identifying this block in serialised content.
func (b *ImageBlock) Kind() BlockKind {
	return BlockKindImage
}

func (b *ImageBlock) isBlock() {}

// AudioBlock carries audio data or a URL to an audio file.
type AudioBlock struct {
	Data     []byte         `json:"data,omitempty"`
	MIMEType string         `json:"mime_type,omitempty"`
	URL      string         `json:"url,omitempty"`
	Extras   map[string]any `json:"extras,omitempty"`
}

// Kind returns [BlockKindAudio], identifying this block in serialised content.
func (b *AudioBlock) Kind() BlockKind {
	return BlockKindAudio
}

func (b *AudioBlock) isBlock() {}

// VideoBlock carries video data or a URL to a video file.
type VideoBlock struct {
	Data     []byte         `json:"data,omitempty"`
	MIMEType string         `json:"mime_type,omitempty"`
	URL      string         `json:"url,omitempty"`
	Extras   map[string]any `json:"extras,omitempty"`
}

// Kind returns [BlockKindVideo], identifying this block in serialised content.
func (b *VideoBlock) Kind() BlockKind {
	return BlockKindVideo
}

func (b *VideoBlock) isBlock() {}

// DocumentBlock carries document data (like PDF) or a URL to a document.
type DocumentBlock struct {
	Data     []byte         `json:"data,omitempty"`
	MIMEType string         `json:"mime_type,omitempty"`
	URL      string         `json:"url,omitempty"`
	Extras   map[string]any `json:"extras,omitempty"`
}

// Kind returns [BlockKindDocument], identifying this block in serialised content.
func (b *DocumentBlock) Kind() BlockKind {
	return BlockKindDocument
}

func (b *DocumentBlock) isBlock() {}

// ThinkingBlock carries the model's internal chain-of-thought reasoning.
// It is produced by models that support extended thinking and is surfaced as a
// first-class block so callers can inspect or display the reasoning separately
// from the final answer.
type ThinkingBlock struct {
	Thinking string         `json:"thinking"`
	Extras   map[string]any `json:"extras,omitempty"`
}

// Kind returns [BlockKindThinking], identifying this block in serialised content.
func (b *ThinkingBlock) Kind() BlockKind {
	return BlockKindThinking
}

func (b *ThinkingBlock) isBlock() {}
