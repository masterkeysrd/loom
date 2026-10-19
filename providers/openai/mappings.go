package loomopenai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
)

func toUserMessageParam(m *message.User) (openai.ChatCompletionMessageParamUnion, error) {
	var parts []openai.ChatCompletionContentPartUnionParam
	for _, block := range m.Content {
		switch b := block.(type) {
		case *message.TextBlock:
			parts = append(parts, openai.TextContentPart(b.Text))
		case *message.ImageBlock:
			url := b.URL
			if url == "" && len(b.Data) > 0 {
				mimeType := b.MIMEType
				if mimeType == "" {
					mimeType = "image/jpeg"
				}
				url = fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(b.Data))
			}
			if url != "" {
				parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
					URL: url,
				}))
			}
		case *message.AudioBlock:
			if len(b.Data) > 0 {
				format := "wav" // default
				if strings.Contains(b.MIMEType, "mp3") {
					format = "mp3"
				}
				parts = append(parts, openai.InputAudioContentPart(openai.ChatCompletionContentPartInputAudioInputAudioParam{
					Data:   base64.StdEncoding.EncodeToString(b.Data),
					Format: format,
				}))
			}
		case *message.DocumentBlock:
			fileParam := openai.ChatCompletionContentPartFileFileParam{}
			if b.URL != "" {
				fileParam.FileID = openai.String(b.URL)
			}
			if len(b.Data) > 0 {
				fileParam.FileData = openai.String(base64.StdEncoding.EncodeToString(b.Data))
			}
			if !param.IsOmitted(fileParam.FileID) || !param.IsOmitted(fileParam.FileData) {
				parts = append(parts, openai.FileContentPart(fileParam))
			}
		}
	}

	if len(parts) == 0 {
		return openai.UserMessage(""), nil
	}

	// If it's just one text part, use the simple string version for better compatibility
	if len(parts) == 1 {
		if text := parts[0].GetText(); text != nil {
			return openai.UserMessage(*text), nil
		}
	}

	return openai.UserMessage(parts), nil
}

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

	if request.ResponseSchema != nil {
		name := request.ResponseSchema.Title
		if name == "" {
			name = "response"
		}
		// OpenAI requires name to be a-z, A-Z, 0-9, or contain underscores and dashes.
		// For now we'll just use the name as is or a safe default.
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:        name,
					Strict:      openai.Bool(true),
					Description: openai.String(request.ResponseSchema.Description),
					Schema:      request.ResponseSchema,
				},
			},
		}
	}

	params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
		IncludeUsage: openai.Bool(true),
	}

	if request.Thinking != nil && request.Thinking.Effort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(request.Thinking.Effort)
	}

	if ext, ok := request.Extensions[PromptCache{}.ExtensionID()]; ok {
		if pc, ok := ext.(PromptCache); ok {
			if pc.Key != "" {
				params.PromptCacheKey = openai.String(pc.Key)
			}
			if pc.Retention != "" {
				params.PromptCacheRetention = openai.ChatCompletionNewParamsPromptCacheRetention(pc.Retention)
			}
		}
	}

	for _, msg := range request.Messages {
		switch m := msg.(type) {
		case *message.System:
			flushPending()
			messages = append(messages, openai.SystemMessage(m.Content.Text()))
		case *message.User:
			flushPending()
			userMsg, err := toUserMessageParam(m)
			if err != nil {
				return openai.ChatCompletionNewParams{}, err
			}
			messages = append(messages, userMsg)
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

			messages = append(messages, openai.ToolMessage(ensureValidContent(toToolString(m)), m.ToolCallID))
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
	content := m.Content.Text()
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

func toToolString(m *message.Tool) string {
	if len(m.Content) > 0 {
		var sb strings.Builder
		if m.IsError {
			sb.WriteString("Error: ")
		}

		sb.WriteString(m.Content.Text())

		for _, block := range m.Content {
			switch b := block.(type) {
			case *message.ImageBlock:
				if b.URL != "" {
					fmt.Fprintf(&sb, "\n[Image URL: %s]", b.URL)
				} else {
					sb.WriteString("\n[Image attached]")
				}
			case *message.AudioBlock:
				if b.URL != "" {
					fmt.Fprintf(&sb, "\n[Audio URL: %s]", b.URL)
				} else {
					sb.WriteString("\n[Audio attached]")
				}
			case *message.VideoBlock:
				if b.URL != "" {
					fmt.Fprintf(&sb, "\n[Video URL: %s]", b.URL)
				} else {
					sb.WriteString("\n[Video attached]")
				}
			case *message.DocumentBlock:
				if b.URL != "" {
					fmt.Fprintf(&sb, "\n[Document URL: %s]", b.URL)
				} else {
					sb.WriteString("\n[Document attached]")
				}
			}
		}
		return sb.String()
	}

	if m.StructuredContent != nil {
		data, _ := json.Marshal(m.StructuredContent)
		return string(data)
	}

	return ""
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
			TotalTokens: int(chunk.Usage.TotalTokens),
			Tokens: message.TokenDetails{
				Input:     int(chunk.Usage.PromptTokens),
				Output:    int(chunk.Usage.CompletionTokens),
				CacheRead: int(chunk.Usage.PromptTokensDetails.CachedTokens),
				Reasoning: int(chunk.Usage.CompletionTokensDetails.ReasoningTokens),
			},
		}
	}

	return res, nil
}
