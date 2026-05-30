package loomollama

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/tool"
	"github.com/ollama/ollama/api"
)

// toChatRequest translates a generic [llm.Request] into an Ollama-specific
// api.ChatRequest, mapping each message and setting sensible inference defaults
// (zero temperature, 32 k context window, streaming enabled).
func toChatRequest(request *llm.Request) (*api.ChatRequest, error) {
	options := map[string]any{
		"stream": true,
	}
	if request.MaxTokens > 0 {
		options["num_ctx"] = request.MaxTokens
	}

	if request.Temperature != nil {
		options["temperature"] = *request.Temperature
	}

	if request.TopP != nil {
		options["top_p"] = *request.TopP
	}

	if request.TopK != nil {
		options["top_k"] = *request.TopK
	}

	if request.PresencePenalty != nil {
		options["presence_penalty"] = *request.PresencePenalty
	}

	if request.FrequencyPenalty != nil {
		options["frequency_penalty"] = *request.FrequencyPenalty
	}

	if len(request.Stop) > 0 {
		options["stop"] = request.Stop
	}

	ollamaRequest := api.ChatRequest{
		Model:   request.Model,
		Options: options,
	}

	ollamaRequest.Messages = make([]api.Message, len(request.Messages))
	for i, msg := range request.Messages {
		ollamaMsg, err := toAPIMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("failed to convert message at index %d: %w", i, err)
		}
		ollamaRequest.Messages[i] = ollamaMsg
	}

	if len(request.Tools) > 0 {
		ollamaRequest.Tools = make([]api.Tool, len(request.Tools))
		for i, t := range request.Tools {
			var err error
			ollamaRequest.Tools[i], err = toAPITool(t)
			if err != nil {
				return nil, fmt.Errorf("failed to convert tool at index %d: %w", i, err)
			}
		}
	}

	return &ollamaRequest, nil
}

// toAPIMessage converts a [message.Message] to the Ollama api.Message format.
// Only text content blocks are supported; any other block type results in an error.
func toAPIMessage(msg message.Message) (api.Message, error) {
	ollamaMsg := api.Message{
		Role: toAPIRole(msg.Role()),
	}

	switch v := msg.(type) {
	case *message.Tool:
		ollamaMsg.ToolName = v.Name
		ollamaMsg.ToolCallID = v.ToolCallID
	}

	var content strings.Builder
	var thinking strings.Builder
	for _, c := range msg.GetContent() {
		switch v := c.(type) {
		case *message.TextBlock:
			content.WriteString(v.Text)
		case *message.ToolCall:
			ollamaMsg.ToolCalls = append(ollamaMsg.ToolCalls, toAPIToolCall(v))
		case *message.ThinkingBlock:
			thinking.WriteString(v.Thinking)
		default:
			return api.Message{}, fmt.Errorf("unsupported content type for message with role %s: %T", msg.Role(), c)
		}
	}
	ollamaMsg.Content = content.String()
	ollamaMsg.Thinking = thinking.String()

	return ollamaMsg, nil
}

// toAPIRole maps a [message.Role] to the role string understood by the Ollama API.
func toAPIRole(role message.Role) string {
	switch role {
	case message.RoleSystem:
		return "system"
	case message.RoleUser:
		return "user"
	case message.RoleAssistant:
		return "assistant"
	case message.RoleTool:
		return "tool"
	default:
		return role.String()
	}
}

// toAPIToolCall converts a [message.ToolCall] block into the Ollama api.ToolCall
// wire format, copying the ID, name, index, and argument map.
func toAPIToolCall(c *message.ToolCall) api.ToolCall {
	args := api.NewToolCallFunctionArguments()
	for k, v := range c.Args {
		args.Set(k, v)
	}

	return api.ToolCall{
		ID: c.ID,
		Function: api.ToolCallFunction{
			Name:      c.Name,
			Index:     c.Index,
			Arguments: args,
		},
	}
}

// toAssistantChunk converts an Ollama api.ChatResponse into an [message.AssistantChunk],
// mapping content blocks and populating token metrics when available.
func toAssistantChunk(resp api.ChatResponse) (message.AssistantChunk, error) {
	content, err := toModelContent(resp.Message)
	if err != nil {
		return message.AssistantChunk{}, fmt.Errorf("failed to convert response content: %w", err)
	}

	chunk := message.AssistantChunk{
		Content:    content,
		Done:       resp.Done,
		DoneReason: resp.DoneReason,
	}

	if resp.PromptEvalCount >= 0 || resp.EvalCount >= 0 {
		chunk.Metrics = &message.TokenMetrics{
			PromptTokens:     resp.PromptEvalCount,
			CompletionTokens: resp.EvalCount,
			TotalTokens:      resp.PromptEvalCount + resp.EvalCount,
		}
	}

	return chunk, nil
}

// toModelContent converts an Ollama api.Message into a [message.Content] slice.
// A non-empty Thinking field is surfaced as a [message.ThinkingBlock] placed
// before any text or tool-call blocks.
func toModelContent(msg api.Message) (message.Content, error) {
	var content message.Content
	if msg.Thinking != "" {
		content = append(content, &message.ThinkingBlock{Thinking: msg.Thinking})
	}
	if msg.Content != "" {
		content = append(content, &message.TextBlock{Text: msg.Content})
	}
	if msg.Images != nil {
		return nil, fmt.Errorf("received images in response, but image content is not supported in the current implementation")
	}
	for i, tc := range msg.ToolCalls {
		content = append(content, toToolCallBlock(i, tc))
	}
	return content, nil
}

// toAPITool converts a [tool.Definition] descriptor into the Ollama api.Tool wire
// format by encoding the InputSchema as JSON and decoding it into
// api.ToolFunctionParameters.
func toAPITool(t tool.Definition) (api.Tool, error) {
	schemaBytes, err := json.Marshal(t.InputSchema)
	if err != nil {
		return api.Tool{}, fmt.Errorf("tool %q: marshal input schema: %w", t.Name, err)
	}
	var params api.ToolFunctionParameters
	if err := json.Unmarshal(schemaBytes, &params); err != nil {
		return api.Tool{}, fmt.Errorf("tool %q: unmarshal schema as tool params: %w", t.Name, err)
	}
	return api.Tool{
		Type: "function",
		Function: api.ToolFunction{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		},
	}, nil
}

// toToolCallBlock converts an Ollama api.ToolCall into a [message.ToolCall] block.
func toToolCallBlock(index int, tc api.ToolCall) *message.ToolCall {
	return &message.ToolCall{
		ID:    tc.ID,
		Name:  tc.Function.Name,
		Index: index,
		Args:  tc.Function.Arguments.ToMap(),
	}
}
