package message

import "encoding/json"

// ToolCall represents a request from the model to invoke a tool.
// It is produced by the LLM and consumed by the tool executor.
// ToolCall implements [Block] so it can be embedded directly in [Content].
type ToolCall struct {
	// ID uniquely identifies this invocation so the result can be
	// correlated back to the call in the conversation history.
	ID string `json:"id"`

	// Name is the name of the tool to invoke.
	Name string `json:"name"`

	// Index is the position of this call within a multi-tool response.
	Index int `json:"index,omitempty"`

	// Args holds the raw arguments decoded from the model's JSON output.
	Args map[string]any `json:"args,omitempty"`

	// Extras captures any additional fields returned by the
	// model that are not explicitly defined above. This allows for forward compatibility with new features in the model's tool call output.
	Extras map[string]any `json:"extras,omitempty"`
}

func (t *ToolCall) Kind() BlockKind { return BlockKindToolCall }
func (t *ToolCall) isBlock()        {}

// ToolCallChunk is a streaming fragment of a tool call produced during an LLM stream.
// Multiple ToolCallChunks sharing the same Index are merged by [AssistantAggregator]
// into a single [ToolCall] once the stream is complete.
type ToolCallChunk struct {
	// Index identifies which tool call this fragment belongs to.
	// Matches the block index in the Anthropic streaming protocol.
	Index int `json:"index"`

	// ID is only non-empty on the first chunk (content_block_start).
	ID string `json:"id,omitempty"`

	// Name is only non-empty on the first chunk (content_block_start).
	Name string `json:"name,omitempty"`

	// ArgsChunk is a raw JSON fragment of the tool arguments (input_json_delta).
	ArgsChunk string `json:"args_chunk,omitempty"`
}

func (t *ToolCallChunk) Kind() BlockKind { return BlockKindToolCallChunk }
func (t *ToolCallChunk) isBlock()        {}

// Aggregate merges another chunk into this one. Non-empty ID and Name fields
// on the incoming chunk overwrite the existing values; ArgsChunk fragments are
// concatenated in order.
func (t *ToolCallChunk) Aggregate(other *ToolCallChunk) {
	if other.ID != "" {
		t.ID = other.ID
	}
	if other.Name != "" {
		t.Name = other.Name
	}
	t.ArgsChunk += other.ArgsChunk
}

// ToToolCall builds a complete [ToolCall] from the accumulated chunk, parsing
// the concatenated ArgsChunk as JSON into the Args map.
func (t *ToolCallChunk) ToToolCall() (*ToolCall, error) {
	args := map[string]any{}
	if t.ArgsChunk != "" {
		if err := json.Unmarshal([]byte(t.ArgsChunk), &args); err != nil {
			return nil, err
		}
	}
	return &ToolCall{
		Index: t.Index,
		ID:    t.ID,
		Name:  t.Name,
		Args:  args,
	}, nil
}
