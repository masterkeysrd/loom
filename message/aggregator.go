package message

import (
	"fmt"
	"maps"
)

// AssistantAggregator accumulates a stream of [AssistantChunk] values into
// coherent content blocks. Consecutive text chunks are merged into a single
// [TextBlock], and consecutive thinking chunks are merged into a single
// [ThinkingBlock], to avoid excessive fragmentation when the full response is
// assembled after streaming. ToolCallChunk fragments are merged by index into
// complete [ToolCall] blocks, mirroring the LangChain streaming convention.
type AssistantAggregator struct {
	id           string
	metadata     map[string]any
	blocks       []Block
	metrics      *TokenMetrics
	pendingCalls map[int]*ToolCallChunk
	done         bool
	doneReason   string
}

// NewAssistantAggregator returns an empty [AssistantAggregator] ready to accept chunks.
func NewAssistantAggregator() *AssistantAggregator {
	return &AssistantAggregator{
		blocks:       make([]Block, 0),
		pendingCalls: make(map[int]*ToolCallChunk),
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
	if len(chunk.Metadata) > 0 {
		if a.metadata == nil {
			a.metadata = make(map[string]any)
		}
		maps.Copy(a.metadata, chunk.Metadata)
	}
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

// GetBlocks flushes any pending tool call chunks into complete [ToolCall] blocks
// and returns the fully merged slice of content blocks.
func (a *AssistantAggregator) GetBlocks() ([]Block, error) {
	blocks := make([]Block, 0, len(a.blocks)+len(a.pendingCalls))
	blocks = append(blocks, a.blocks...)
	for _, call := range a.pendingCalls {
		tc, err := call.ToToolCall()
		if err != nil {
			return nil, fmt.Errorf("failed to convert tool call chunk to tool call: %w", err)
		}
		blocks = append(blocks, tc)
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
				if len(incoming.Extras) > 0 {
					if txt.Extras == nil {
						txt.Extras = make(map[string]any)
					}
					maps.Copy(txt.Extras, incoming.Extras)
				}
				return
			}
		}
		// Store an independent copy so that mutations to a.blocks[n].Text
		// (e.g. by a sibling aggregator sharing the same *TextBlock pointer)
		// do not affect the block owned by this aggregator, and vice-versa.
		txt := &TextBlock{Text: incoming.Text}
		if len(incoming.Extras) > 0 {
			txt.Extras = make(map[string]any)
			maps.Copy(txt.Extras, incoming.Extras)
		}
		a.blocks = append(a.blocks, txt)
	case *ThinkingBlock:
		if len(a.blocks) > 0 {
			last := a.blocks[len(a.blocks)-1]
			if last.Kind() == BlockKindThinking {
				tb := last.(*ThinkingBlock)
				tb.Thinking += incoming.Thinking
				if len(incoming.Extras) > 0 {
					if tb.Extras == nil {
						tb.Extras = make(map[string]any)
					}
					maps.Copy(tb.Extras, incoming.Extras)
				}
				return
			}
		}
		// Same defensive copy as TextBlock above.
		tb := &ThinkingBlock{Thinking: incoming.Thinking}
		if len(incoming.Extras) > 0 {
			tb.Extras = make(map[string]any)
			maps.Copy(tb.Extras, incoming.Extras)
		}
		a.blocks = append(a.blocks, tb)
	case *ToolCallChunk:
		pt, ok := a.pendingCalls[incoming.Index]
		if !ok {
			pt = &ToolCallChunk{Index: incoming.Index}
			a.pendingCalls[incoming.Index] = pt
		}
		pt.Aggregate(incoming)
	default:
		a.blocks = append(a.blocks, incoming)
	}
}
