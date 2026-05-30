package message

// Chunk is the marker interface for streaming response fragments.
// Concrete chunk types (e.g. [AssistantChunk]) embed [BaseChunk] and are
// accumulated by an aggregator into a full message.
type Chunk interface {
	isChunk()
}

// BaseChunk carries the shared identity fields common to all chunk types.
type BaseChunk struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
}

// AssistantChunk is a single streaming fragment produced by an LLM call.
// Multiple AssistantChunks are merged into an [Assistant] by [AssistantAggregator].
// Metrics is only populated on the final chunk of a stream.
type AssistantChunk struct {
	BaseChunk `json:",inline"`

	Content []Block       `json:"content"`
	Metrics *TokenMetrics `json:"metrics,omitempty"`

	Done       bool   `json:"done"`
	DoneReason string `json:"done_reason,omitempty"`
}

// isChunk marks AssistantChunk as a [Chunk].
func (c *AssistantChunk) isChunk() {}

// ToolCalls returns all [ToolCall] blocks present in this chunk's content.
func (c *AssistantChunk) ToolCalls() []*ToolCall {
	var calls []*ToolCall
	for _, b := range c.Content {
		if tc, ok := b.(*ToolCall); ok {
			calls = append(calls, tc)
		}
	}
	return calls
}
