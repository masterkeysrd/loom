package message

import (
	"fmt"
	"maps"
	"slices"

	"github.com/masterkeysrd/loom/internal/collection"
)

// AssistantAggregator accumulates a stream of [AssistantChunk] values into
// coherent content blocks. Consecutive text chunks are merged into a single
// [TextBlock], and consecutive thinking chunks are merged into a single
// [ThinkingBlock], to avoid excessive fragmentation when the full response is
// assembled after streaming. ToolCallChunk and ServerToolCallChunk fragments
// are merged by index into complete [ToolCall] and [ServerToolCall] blocks in place.
type AssistantAggregator struct {
	id         string
	metadata   map[string]any
	blocks     []Block
	metrics    *TokenMetrics
	done       bool
	doneReason string
}

// NewAssistantAggregator returns an empty [AssistantAggregator] ready to accept chunks.
func NewAssistantAggregator() *AssistantAggregator {
	return &AssistantAggregator{
		blocks: make([]Block, 0),
	}
}

// Add incorporates all blocks from chunk into the aggregator.
// If the chunk carries non-nil Metrics they are accumulated into the running totals.
func (a *AssistantAggregator) Add(chunk *AssistantChunk) {
	if a.id == "" {
		a.id = chunk.ID
	}

	if chunk.Done {
		a.done = true
		a.doneReason = chunk.DoneReason
	}

	for _, block := range chunk.Content {
		a.upsert(block)
	}
	a.metadata = collection.MergeMap(a.metadata, chunk.Metadata)
	if chunk.Metrics != nil {
		if a.metrics == nil {
			a.metrics = &TokenMetrics{}
		}
		// Streaming chunks report cumulative token metrics. We overwrite the running total
		// with the latest non-nil metrics instead of adding them, which avoids double-counting
		// on providers that emit usage metadata on every chunk (e.g., Gemini).
		*a.metrics = *chunk.Metrics
	}
}

func (a *AssistantAggregator) Build() (*Assistant, error) {
	blocks, err := a.GetBlocks()
	if err != nil {
		return nil, fmt.Errorf("failed to build assistant message: %w", err)
	}

	return &Assistant{
		Base: Base{
			ID:       a.id,
			Metadata: a.metadata,
		},
		Content:    blocks,
		Metrics:    a.metrics,
		Done:       a.done,
		DoneReason: a.doneReason,
	}, nil
}

func (a *AssistantAggregator) ID() string {
	return a.id
}

// GetBlocks flushes any pending tool call chunks and server tool call chunks into
// complete blocks in place, and returns the fully merged slice of content blocks.
func (a *AssistantAggregator) GetBlocks() ([]Block, error) {
	blocks := make([]Block, len(a.blocks))
	for i, b := range a.blocks {
		switch block := b.(type) {
		case *ToolCallChunk:
			tc, err := block.ToToolCall()
			if err != nil {
				return nil, fmt.Errorf("failed to convert tool call chunk to tool call: %w", err)
			}
			blocks[i] = tc
		case *ServerToolCallChunk:
			stc, err := block.ToServerToolCall()
			if err != nil {
				return nil, fmt.Errorf("failed to convert server tool call chunk to server tool call: %w", err)
			}
			blocks[i] = stc
		default:
			blocks[i] = block
		}
	}
	return blocks, nil
}

// GetMetrics returns the token metrics collected from the stream, or nil if
// no metrics were reported.
func (a *AssistantAggregator) GetMetrics() *TokenMetrics {
	return a.metrics
}

func (a *AssistantAggregator) upsert(incoming Block) {
	switch incoming := incoming.(type) {
	case *TextBlock:
		if len(a.blocks) > 0 {
			last := a.blocks[len(a.blocks)-1]
			if last.Kind() == BlockKindText {
				txt := last.(*TextBlock)
				txt.Text += incoming.Text
				txt.Extras = collection.MergeMap(txt.Extras, incoming.Extras)
				return
			}
		}
		// Store an independent copy so that mutations to a.blocks[n].Text
		// (e.g. by a sibling aggregator sharing the same *TextBlock pointer)
		// do not affect the block owned by this aggregator, and vice-versa.
		txt := &TextBlock{
			Text:   incoming.Text,
			Extras: maps.Clone(incoming.Extras),
		}
		a.blocks = append(a.blocks, txt)
	case *ThinkingBlock:
		if len(a.blocks) > 0 {
			last := a.blocks[len(a.blocks)-1]
			if last.Kind() == BlockKindThinking {
				tb := last.(*ThinkingBlock)
				tb.Thinking += incoming.Thinking
				tb.Extras = collection.MergeMap(tb.Extras, incoming.Extras)
				return
			}
		}
		// Same defensive copy as TextBlock above.
		tb := &ThinkingBlock{
			Thinking: incoming.Thinking,
			Extras:   maps.Clone(incoming.Extras),
		}
		a.blocks = append(a.blocks, tb)
	case *ToolCallChunk:
		for _, v := range slices.Backward(a.blocks) {
			if pt, ok := v.(*ToolCallChunk); ok && pt.Index == incoming.Index {
				pt.Aggregate(incoming)
				return
			}
		}
		pt := &ToolCallChunk{Index: incoming.Index}
		pt.Aggregate(incoming)
		a.blocks = append(a.blocks, pt)
	case *ServerToolCallChunk:
		for _, v := range slices.Backward(a.blocks) {
			if pt, ok := v.(*ServerToolCallChunk); ok && pt.Index == incoming.Index {
				pt.Aggregate(incoming)
				return
			}
		}
		pt := &ServerToolCallChunk{Index: incoming.Index}
		pt.Aggregate(incoming)
		a.blocks = append(a.blocks, pt)
	default:
		// Make a defensive copy of incoming block so two aggregators
		// sharing a chunk stream do not mutate shared pointers.
		a.blocks = append(a.blocks, CloneBlock(incoming))
	}
}
