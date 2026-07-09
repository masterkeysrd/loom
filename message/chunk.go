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

	Model   string        `json:"model,omitempty"`
	Content []Block       `json:"content"`
	Metrics *TokenMetrics `json:"metrics,omitempty"`

	Done       bool   `json:"done"`
	DoneReason string `json:"done_reason,omitempty"`
}

// isChunk marks AssistantChunk as a [Chunk].
func (c *AssistantChunk) isChunk() {}

// ToolChunk is a single streaming fragment produced by a tool execution.
// It can carry progress updates for the UI and/or content blocks for the LLM result.
type ToolChunk struct {
	BaseChunk `json:",inline"`

	// Progress is an ephemeral status update intended for UI display only.
	Progress string `json:"progress,omitempty"`

	// ProgressCurrent and ProgressTotal provide optional numerical progress (e.g. 50/100).
	ProgressCurrent *float64 `json:"progress_current,omitempty"`
	ProgressTotal   *float64 `json:"progress_total,omitempty"`

	// Content is a sequence of blocks to be aggregated into the final tool result.
	Content Content `json:"content,omitempty"`

	// StructuredContent holds the structured Go value returned by the tool.
	StructuredContent any `json:"structured_content,omitempty"`

	// IsError indicates if this chunk represents a functional error.
	IsError bool `json:"is_error,omitempty"`

	// Metadata carries any response metadata set by the tool runtime.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// isChunk marks ToolChunk as a [Chunk].
func (c *ToolChunk) isChunk() {}
