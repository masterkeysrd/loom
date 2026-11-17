// Package protocol defines the internal wire types and framing helpers for the
// Claude Code CLI `--input-format stream-json` / `--output-format stream-json`
// NDJSON and bidirectional `control` protocol.
package protocol

import "encoding/json"

// UserInput represents a `user` turn frame written to the CLI stdin.
type UserInput struct {
	Type      string      `json:"type"`
	SessionID string      `json:"session_id,omitempty"`
	Message   UserMessage `json:"message"`
}

// UserMessage carries the role and content payload inside [UserInput].
type UserMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// ControlRequest represents an outgoing `control_request` frame written to stdin.
type ControlRequest struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}

// InitializeRequest is the `initialize` control subtype payload.
type InitializeRequest struct {
	Subtype       string         `json:"subtype"`
	Hooks         map[string]any `json:"hooks,omitempty"`
	SDKMCPServers []string       `json:"sdkMcpServers,omitempty"`
}

// ControlResponse represents a bidirectional `control_response` frame.
type ControlResponse struct {
	Type     string              `json:"type"`
	Response ControlResponseBody `json:"response"`
}

// ControlResponseBody holds the status, correlated request ID, and payload or error.
type ControlResponseBody struct {
	Subtype   string `json:"subtype"` // "success" or "error"
	RequestID string `json:"request_id"`
	Response  any    `json:"response,omitempty"`
	Error     string `json:"error,omitempty"`
}

// MCPResponseEnvelope wraps JSON-RPC responses sent back to the CLI over
// the control channel as required by Claude Code's SDK MCP handler.
type MCPResponseEnvelope struct {
	MCPResponse JSONRPCResponse `json:"mcp_response"`
}

// JSONRPCRequest decodes an incoming JSON-RPC 2.0 request tunneled in `mcp_message`.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse encodes an outgoing JSON-RPC 2.0 response inside [MCPResponseEnvelope].
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError describes a JSON-RPC 2.0 error payload.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCPToolsCallParams decodes `tools/call` parameters inside an MCP JSON-RPC request.
type MCPToolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// MCPToolDef encodes a tool entry returned from MCP `tools/list`.
type MCPToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"inputSchema"`
}

// Frame represents a single NDJSON line emitted by the CLI on stdout.
type Frame struct {
	Type                 string                  `json:"type"`
	Subtype              string                  `json:"subtype,omitempty"`
	Status               string                  `json:"status,omitempty"`
	SessionID            string                  `json:"session_id,omitempty"`
	Model                string                  `json:"model,omitempty"`
	RequestID            string                  `json:"request_id,omitempty"`
	Request              *IncomingControlRequest `json:"request,omitempty"`
	Response             *ControlResponseBody    `json:"response,omitempty"`
	Event                *StreamEvent            `json:"event,omitempty"`
	Message              *AssistantMessage       `json:"message,omitempty"`
	CompactMetadata      *CompactMetadata        `json:"compactMetadata,omitempty"`
	CompactMetadataSnake *CompactMetadata        `json:"compact_metadata,omitempty"`
	IsError              bool                    `json:"is_error,omitempty"`
	Result               string                  `json:"result,omitempty"`
	Errors               []string                `json:"errors,omitempty"`
	DurationMS           int64                   `json:"duration_ms,omitempty"`
	NumTurns             int                     `json:"num_turns,omitempty"`
	TotalCostUSD         float64                 `json:"total_cost_usd,omitempty"`
	Usage                *Usage                  `json:"usage,omitempty"`
}

// CompactMetadata holds pre-compaction stats from a `compact_boundary` system frame.
type CompactMetadata struct {
	Trigger        string `json:"trigger,omitempty"`
	PreTokens      int    `json:"preTokens,omitempty"`
	PreTokensSnake int    `json:"pre_tokens,omitempty"`
}

// CompactInfo extracts normalized trigger and pre-compaction token count if f
// is a `compact_boundary` system frame.
func (f Frame) CompactInfo() (trigger string, preTokens int, ok bool) {
	if f.Type != "system" || f.Subtype != "compact_boundary" {
		return "", 0, false
	}
	meta := f.CompactMetadata
	if meta == nil {
		meta = f.CompactMetadataSnake
	}
	if meta != nil {
		trigger = meta.Trigger
		preTokens = meta.PreTokens
		if preTokens == 0 {
			preTokens = meta.PreTokensSnake
		}
	}
	return trigger, preTokens, true
}

// IncomingControlRequest holds the body of a CLI-initiated `control_request` or
// `sdk_control_request` frame.
type IncomingControlRequest struct {
	Subtype    string          `json:"subtype"`
	RequestID  string          `json:"request_id,omitempty"`
	ServerName string          `json:"server_name,omitempty"`
	Message    json.RawMessage `json:"message,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	ToolUseID  string          `json:"tool_use_id,omitempty"`
}

// StreamEvent wraps Anthropic SSE-style streaming events forwarded by the CLI.
type StreamEvent struct {
	Type         string            `json:"type"`
	Index        int               `json:"index"`
	ContentBlock *ContentBlock     `json:"content_block,omitempty"`
	Delta        *ContentDelta     `json:"delta,omitempty"`
	Message      *AssistantMessage `json:"message,omitempty"`
	Usage        *Usage            `json:"usage,omitempty"`
}

// AssistantMessage represents an `assistant` message frame or `message_start` body.
type AssistantMessage struct {
	ID         string         `json:"id,omitempty"`
	Model      string         `json:"model,omitempty"`
	Role       string         `json:"role,omitempty"`
	Content    []ContentBlock `json:"content,omitempty"`
	StopReason string         `json:"stop_reason,omitempty"`
	Usage      *Usage         `json:"usage,omitempty"`
}

// ContentBlock represents a text, thinking, or tool_use content block.
type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

// ContentDelta represents an incremental content or stop-reason delta.
type ContentDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

// Usage reports token counters emitted by Anthropic/Claude Code result frames.
type Usage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	ThinkingTokens           int `json:"thinking_tokens,omitempty"`
}

// BuildInitializeControlRequest constructs the initial `control_request` handshake frame.
func BuildInitializeControlRequest(reqID string, sdkMCPServers []string) (ControlRequest, error) {
	initReq := InitializeRequest{
		Subtype:       "initialize",
		Hooks:         map[string]any{},
		SDKMCPServers: sdkMCPServers,
	}
	raw, err := json.Marshal(initReq)
	if err != nil {
		return ControlRequest{}, err
	}
	return ControlRequest{
		Type:      "control_request",
		RequestID: reqID,
		Request:   raw,
	}, nil
}

// NewSuccessControlResponse builds a successful `control_response` envelope.
func NewSuccessControlResponse(reqID string, body any) *ControlResponse {
	return &ControlResponse{
		Type: "control_response",
		Response: ControlResponseBody{
			Subtype:   "success",
			RequestID: reqID,
			Response:  body,
		},
	}
}

// NewErrorControlResponse builds an error `control_response` envelope.
func NewErrorControlResponse(reqID, errMsg string) *ControlResponse {
	return &ControlResponse{
		Type: "control_response",
		Response: ControlResponseBody{
			Subtype:   "error",
			RequestID: reqID,
			Error:     errMsg,
		},
	}
}

// NewMCPSuccessControlResponse wraps a JSON-RPC success result in `mcp_response`.
func NewMCPSuccessControlResponse(reqID string, rpcID json.RawMessage, result any) *ControlResponse {
	return NewSuccessControlResponse(reqID, MCPResponseEnvelope{
		MCPResponse: JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      rpcID,
			Result:  result,
		},
	})
}

// NewMCPErrorControlResponse wraps a JSON-RPC error in `mcp_response`.
func NewMCPErrorControlResponse(reqID string, rpcID json.RawMessage, code int, msg string) *ControlResponse {
	return NewSuccessControlResponse(reqID, MCPResponseEnvelope{
		MCPResponse: JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      rpcID,
			Error: &JSONRPCError{
				Code:    code,
				Message: msg,
			},
		},
	})
}
