package loomopenai

import (
	"encoding/json"
	"strings"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
)

func toChatCompletionNewParams(request *llm.Request) (openai.ChatCompletionNewParams, error) {
	params := openai.ChatCompletionNewParams{
		Model: openai.ChatModel(request.Model),
	}

	var messages []openai.ChatCompletionMessageParamUnion
	var pendingIDs []string

	// flushPending ensures all tool calls emitted by the assistant have a response.
	// OpenAI requires that every tool_call_id in an assistant message is followed by a tool message.
	flushPending := func() {
		for _, id := range pendingIDs {
			messages = append(messages, openai.ToolMessage("Tool not found or execution skipped.", id))
		}
		pendingIDs = nil
	}

	if request.MaxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(request.MaxTokens))
	}

	if request.Temperature != nil {
		params.Temperature = openai.Float(float64(*request.Temperature))
	}

	if request.TopP != nil {
		params.TopP = openai.Float(float64(*request.TopP))
	}

	if request.PresencePenalty != nil {
		params.PresencePenalty = openai.Float(float64(*request.PresencePenalty))
	}

	if request.FrequencyPenalty != nil {
		params.FrequencyPenalty = openai.Float(float64(*request.FrequencyPenalty))
	}

	if len(request.Stop) > 0 {
		params.Stop = openai.ChatCompletionNewParamsStopUnion{
			OfStringArray: request.Stop,
		}
	}

	if request.ResponseFormat == "json_object" {
		rf := shared.NewResponseFormatJSONObjectParam()
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &rf,
		}
	}

	for _, msg := range request.Messages {
		switch m := msg.(type) {
		case *message.System:
			flushPending()
			messages = append(messages, openai.SystemMessage(extractText(m.Content)))
		case *message.User:
			flushPending()
			messages = append(messages, openai.UserMessage(extractText(m.Content)))
		case *message.Assistant:
			flushPending()
			assistantMsg := toAssistantMessageParam(m)
			// Track tool calls to ensure they are answered
			for _, tc := range assistantMsg.ToolCalls {
				if tc.OfFunction != nil {
					pendingIDs = append(pendingIDs, tc.OfFunction.ID)
				}
			}
			messages = append(messages, openai.ChatCompletionMessageParamUnion{
				OfAssistant: &assistantMsg,
			})
		case *message.Tool:
			// Match and remove from pending IDs
			foundIdx := -1
			for i, id := range pendingIDs {
				if id == m.ToolCallID {
					foundIdx = i
					break
				}
			}
			if foundIdx != -1 {
				pendingIDs = append(pendingIDs[:foundIdx], pendingIDs[foundIdx+1:]...)
			}
			messages = append(messages, openai.ToolMessage(ensureValidContent(extractText(m.Content)), m.ToolCallID))
		}
	}
	flushPending()

	params.Messages = messages

	if len(request.Tools) > 0 {
		tools := make([]openai.ChatCompletionToolUnionParam, 0, len(request.Tools))
		for _, t := range request.Tools {
			var paramsMap map[string]any
			if t.InputSchema != nil {
				// Convert jsonschema.Schema to map[string]any
				data, _ := json.Marshal(t.InputSchema)
				_ = json.Unmarshal(data, &paramsMap)
			}

			if paramsMap == nil {
				paramsMap = make(map[string]any)
			}

			// OpenAI requires 'properties' if 'type' is 'object'.
			// If it's an empty object, we ensure 'properties' is an empty map instead of missing.
			if paramsMap["type"] == "object" {
				if paramsMap["properties"] == nil {
					paramsMap["properties"] = make(map[string]any)
				}
				if paramsMap["additionalProperties"] == nil {
					paramsMap["additionalProperties"] = false
				}
			}

			tools = append(tools, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  shared.FunctionParameters(paramsMap),
			}))
		}
		params.Tools = tools
	}

	return params, nil
}

func toAssistantMessageParam(m *message.Assistant) openai.ChatCompletionAssistantMessageParam {
	content := extractText(m.Content)
	assistantMsg := openai.ChatCompletionAssistantMessageParam{}
	if content != "" {
		assistantMsg.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
			OfString: openai.String(content),
		}
	}

	var toolCalls []openai.ChatCompletionMessageToolCallUnionParam
	for _, block := range m.Content {
		if tc, ok := block.(*message.ToolCall); ok {
			args, _ := json.Marshal(tc.Args)
			toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID: tc.ID,
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      tc.Name,
						Arguments: string(args),
					},
				},
			})
		}
	}
	if len(toolCalls) > 0 {
		assistantMsg.ToolCalls = toolCalls
		if content == "" {
			assistantMsg.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
				OfString: param.Null[string](),
			}
		}
	}

	return assistantMsg
}

func extractText(content message.Content) string {
	var sb strings.Builder
	for _, block := range content {
		if tb, ok := block.(*message.TextBlock); ok {
			sb.WriteString(tb.Text)
		}
	}
	return sb.String()
}

func ensureValidContent(content string) string {
	if content == "" {
		return "Tool executed successfully."
	}
	return content
}

func toAssistantChunk(chunk *openai.ChatCompletionChunk) (message.AssistantChunk, error) {
	res := message.AssistantChunk{}

	if len(chunk.Choices) > 0 {
		choice := chunk.Choices[0]
		if choice.Delta.Content != "" {
			res.Content = append(res.Content, &message.TextBlock{
				Text: choice.Delta.Content,
			})
		}

		for _, tc := range choice.Delta.ToolCalls {
			res.Content = append(res.Content, &message.ToolCallChunk{
				Index:     int(tc.Index),
				ID:        tc.ID,
				Name:      tc.Function.Name,
				ArgsChunk: tc.Function.Arguments,
			})
		}
	}

	if chunk.Usage.TotalTokens > 0 {
		res.Metrics = &message.TokenMetrics{
			PromptTokens:     int(chunk.Usage.PromptTokens),
			CompletionTokens: int(chunk.Usage.CompletionTokens),
			TotalTokens:      int(chunk.Usage.TotalTokens),
		}
	}

	return res, nil
}
