package loomanthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/tool"
)

// toMessageNewParams translates a generic [llm.Request] into an Anthropic-specific request.
func toMessageNewParams(request *llm.Request) (anthropic.MessageNewParams, error) {
	anthropicReq := anthropic.MessageNewParams{
		Model:     anthropic.Model(request.Model),
		Messages:  make([]anthropic.MessageParam, 0, len(request.Messages)),
		MaxTokens: int64(request.MaxTokens),
	}

	if request.Temperature != nil {
		anthropicReq.Temperature = anthropic.Float(float64(*request.Temperature))
	}

	if request.TopP != nil {
		anthropicReq.TopP = anthropic.Float(float64(*request.TopP))
	}

	if request.TopK != nil {
		anthropicReq.TopK = anthropic.Int(int64(*request.TopK))
	}

	if len(request.Stop) > 0 {
		anthropicReq.StopSequences = request.Stop
	}

	if request.Thinking != nil {
		if request.Thinking.Budget > 0 || request.Thinking.Adaptive {
			if request.Thinking.Adaptive {
				anthropicReq.Thinking = anthropic.ThinkingConfigParamUnion{
					OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{},
				}
			} else {
				anthropicReq.Thinking = anthropic.ThinkingConfigParamOfEnabled(int64(request.Thinking.Budget))
			}
		}

		if request.Thinking.Effort != "" {
			anthropicReq.OutputConfig = anthropic.OutputConfigParam{
				Effort: anthropic.OutputConfigEffort(request.Thinking.Effort),
			}
		}
	}

	// mark if the tool was completed.
	toolUsage := make(map[string]bool)
	registerToolUsage := func(toolCallID string, used bool) {
		toolUsage[toolCallID] = toolUsage[toolCallID] || used
	}

	for _, msg := range request.Messages {
		switch msg := msg.(type) {
		case *message.System:
			anthropicReq.System = toTextBlocksParams(msg.Content)
		case *message.Assistant:
			blocks := toContentBlocksParams(msg.Content)
			anthropicReq.Messages = append(anthropicReq.Messages, anthropic.NewAssistantMessage(
				blocks...,
			))

			// Mark any tool calls in the assistant message as used, so that we know to expect results for them later.
			for _, block := range msg.ToolCalls() {
				registerToolUsage(block.ID, true)
			}
		case *message.User:
			blocks := toContentBlocksParams(msg.Content)
			anthropicReq.Messages = append(anthropicReq.Messages, anthropic.NewUserMessage(
				blocks...,
			))
		case *message.Tool:
			var text strings.Builder
			for _, block := range msg.Content {
				if textBlock, ok := block.(*message.TextBlock); ok {
					text.WriteString(textBlock.Text + "\n")
				}
			}
			anthropicReq.Messages = append(anthropicReq.Messages, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(msg.ToolCallID, text.String(), false),
			))
			registerToolUsage(msg.ToolCallID, false)
		}
	}

	for toolCallID, used := range toolUsage {
		if !used {
			// If a tool was called but not marked as used, we need to add an empty tool result block for it so that the response includes results for this tool call.
			anthropicReq.Messages = append(anthropicReq.Messages, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(toolCallID, "", false),
			))
		}
	}

	if len(request.Tools) > 0 {
		anthropicReq.Tools = make([]anthropic.ToolUnionParam, 0, len(request.Tools))
		for _, tool := range request.Tools {
			toolParam, err := toToolParam(tool)
			if err != nil {
				return anthropic.MessageNewParams{}, fmt.Errorf("failed to convert tool definition: %w", err)
			}

			anthropicReq.Tools = append(anthropicReq.Tools, anthropic.ToolUnionParam{
				OfTool: &toolParam,
			})
		}
	}

	return anthropicReq, nil
}

func toTextBlocksParams(content message.Content) []anthropic.TextBlockParam {
	blocks := make([]anthropic.TextBlockParam, 0, len(content))
	for _, block := range content {
		switch block := block.(type) {
		case *message.TextBlock:
			blocks = append(blocks, anthropic.TextBlockParam{
				Text: block.Text,
			})
		}
	}

	return blocks
}

func toContentBlocksParams(content message.Content) []anthropic.ContentBlockParamUnion {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(content))
	for _, block := range content {
		switch block := block.(type) {
		case *message.TextBlock:
			blocks = append(blocks, anthropic.NewTextBlock(block.Text))
		case *message.ToolCall:
			if block.Args == nil {
				block.Args = make(map[string]any)
			}
			blocks = append(blocks, anthropic.NewToolUseBlock(block.ID, block.Args, block.Name))
		}
	}

	return blocks
}

func toToolParam(def tool.Definition) (anthropic.ToolParam, error) {
	jsonSchema, err := json.Marshal(def.InputSchema)
	if err != nil {
		return anthropic.ToolParam{}, err
	}

	var anthropicSchema anthropic.ToolInputSchemaParam
	if err := json.Unmarshal(jsonSchema, &anthropicSchema); err != nil {
		return anthropic.ToolParam{}, err
	}

	return anthropic.ToolParam{
		Name:        def.Name,
		Description: anthropic.String(def.Description),
		InputSchema: anthropicSchema,
	}, nil
}

func toAssistantChunk(event anthropic.MessageStreamEventUnion) (message.AssistantChunk, error) {
	chunk := message.AssistantChunk{}
	switch e := event.AsAny().(type) {
	case anthropic.MessageStartEvent:
		chunk.ID = e.Message.ID
	case anthropic.ContentBlockDeltaEvent:
		switch delta := e.Delta.AsAny().(type) {
		case anthropic.TextDelta:
			chunk.Content = append(chunk.Content, &message.TextBlock{
				Text: delta.Text,
			})
		case anthropic.ThinkingDelta:
			chunk.Content = append(chunk.Content, &message.ThinkingBlock{
				Thinking: delta.Thinking,
			})
		case anthropic.InputJSONDelta:
			// Not action needed
		default:
			deltaJSON, _ := json.Marshal(delta)
			fmt.Printf("Received unsupported content block delta type: %T, content: %s\n", delta, string(deltaJSON))
			return message.AssistantChunk{}, fmt.Errorf("unsupported content block delta type: %T", delta)
		}
	case anthropic.MessageDeltaEvent:
		inputTokens := int(e.Usage.InputTokens)
		outputTokens := int(e.Usage.OutputTokens)
		chunk.Metrics = &message.TokenMetrics{
			PromptTokens:       inputTokens,
			CompletionTokens:   outputTokens,
			TotalTokens:        inputTokens + outputTokens,
			CachedPromptTokens: int(e.Usage.CacheReadInputTokens),
			CacheWriteTokens:   int(e.Usage.CacheCreationInputTokens),
			ReasoningTokens:    int(e.Usage.OutputTokensDetails.ThinkingTokens),
		}
	case anthropic.MessageStopEvent:
	case anthropic.ContentBlockStartEvent:
	case anthropic.ContentBlockStopEvent:
		// No action needed for block start events in this implementation, but we could use this to set up state if needed.
	default:
		return message.AssistantChunk{}, fmt.Errorf("unsupported event type: %T", e)
	}

	return chunk, nil
}
