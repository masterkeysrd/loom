package loomanthropic

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

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

	var cacheHeader bool
	if ext, ok := request.Extensions[PromptCaching{}.ExtensionID()]; ok {
		if pc, ok := ext.(PromptCaching); ok {
			cacheHeader = pc.CacheHeader
		}
	}

	// mark if the tool was completed.
	toolUsage := make(map[string]bool)
	registerToolUsage := func(toolCallID string, used bool) {
		toolUsage[toolCallID] = toolUsage[toolCallID] || used
	}

	for _, msg := range request.Messages {
		var cache bool
		if ext, ok := msg.GetExtensions()[MetadataCache]; ok {
			if mc, ok := ext.(*MessageCache); ok {
				cache = mc.Enabled
			}
		}

		switch msg := msg.(type) {
		case *message.System:
			anthropicReq.System = toTextBlocksParams(msg.Content, cacheHeader)
		case *message.Assistant:
			anthropicReq.Messages = append(anthropicReq.Messages, anthropic.NewAssistantMessage(
				toContentBlocksParams(msg.Content, cache)...,
			))

			// Mark any tool calls in the assistant message as used, so that we know to expect results for them later.
			for _, block := range msg.ToolCalls() {
				registerToolUsage(block.ID, true)
			}
		case *message.User:
			anthropicReq.Messages = append(anthropicReq.Messages, anthropic.NewUserMessage(
				toContentBlocksParams(msg.Content, cache)...,
			))
		case *message.Tool:
			// Consistently use ToolResultBlockParam for all tool outputs.
			contentBlocks := toToolResultContent(msg.Content)
			trbp := &anthropic.ToolResultBlockParam{
				ToolUseID: msg.ToolCallID,
				Content:   contentBlocks,
				IsError:   anthropic.Bool(msg.IsError),
			}
			if cache {
				trbp.CacheControl = anthropic.NewCacheControlEphemeralParam()
			}

			anthropicReq.Messages = append(anthropicReq.Messages, anthropic.MessageParam{
				Role: anthropic.MessageParamRoleUser,
				Content: []anthropic.ContentBlockParamUnion{
					{
						OfToolResult: trbp,
					},
				},
			})

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
		for i, tool := range request.Tools {
			// Only cache the last tool in the list if cacheHeader is set
			cache := cacheHeader && i == len(request.Tools)-1
			toolParam, err := toToolParam(tool, cache)
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

func toTextBlocksParams(content message.Content, cache bool) []anthropic.TextBlockParam {
	blocks := make([]anthropic.TextBlockParam, 0, len(content))
	for i, block := range content {
		switch block := block.(type) {
		case *message.TextBlock:
			param := anthropic.TextBlockParam{
				Text: block.Text,
			}
			if cache && i == len(content)-1 {
				param.CacheControl = anthropic.NewCacheControlEphemeralParam()
			}
			blocks = append(blocks, param)
		}
	}

	return blocks
}

func toContentBlocksParams(content message.Content, cache bool) []anthropic.ContentBlockParamUnion {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(content))
	for i, block := range content {
		var b anthropic.ContentBlockParamUnion
		switch block := block.(type) {
		case *message.TextBlock:
			tb := anthropic.TextBlockParam{
				Text: block.Text,
			}
			if cache && i == len(content)-1 {
				tb.CacheControl = anthropic.NewCacheControlEphemeralParam()
			}
			b = anthropic.ContentBlockParamUnion{
				OfText: &tb,
			}
		case *message.ImageBlock:
			if len(block.Data) > 0 {
				mimeType := block.MIMEType
				if mimeType == "" {
					mimeType = "image/jpeg"
				}
				ib := anthropic.ImageBlockParam{
					Source: anthropic.ImageBlockParamSourceUnion{
						OfBase64: &anthropic.Base64ImageSourceParam{
							Data:      base64.StdEncoding.EncodeToString(block.Data),
							MediaType: anthropic.Base64ImageSourceMediaType(mimeType),
						},
					},
				}
				if cache && i == len(content)-1 {
					ib.CacheControl = anthropic.NewCacheControlEphemeralParam()
				}
				b = anthropic.ContentBlockParamUnion{
					OfImage: &ib,
				}
			}
		case *message.DocumentBlock:
			if len(block.Data) > 0 {
				db := anthropic.DocumentBlockParam{
					Source: anthropic.DocumentBlockParamSourceUnion{
						OfBase64: &anthropic.Base64PDFSourceParam{
							Data: base64.StdEncoding.EncodeToString(block.Data),
						},
					},
				}
				if cache && i == len(content)-1 {
					db.CacheControl = anthropic.NewCacheControlEphemeralParam()
				}
				b = anthropic.ContentBlockParamUnion{
					OfDocument: &db,
				}
			}
		case *message.ToolCall:
			if block.Args == nil {
				block.Args = make(map[string]any)
			}
			tub := anthropic.ToolUseBlockParam{
				ID:    block.ID,
				Input: block.Args,
				Name:  block.Name,
			}
			if cache && i == len(content)-1 {
				tub.CacheControl = anthropic.NewCacheControlEphemeralParam()
			}
			b = anthropic.ContentBlockParamUnion{
				OfToolUse: &tub,
			}
		}

		if b != (anthropic.ContentBlockParamUnion{}) {
			blocks = append(blocks, b)
		}
	}

	return blocks
}

func toToolParam(def tool.Definition, cache bool) (anthropic.ToolParam, error) {
	jsonSchema, err := json.Marshal(def.InputSchema)
	if err != nil {
		return anthropic.ToolParam{}, err
	}

	var anthropicSchema anthropic.ToolInputSchemaParam
	if err := json.Unmarshal(jsonSchema, &anthropicSchema); err != nil {
		return anthropic.ToolParam{}, err
	}

	param := anthropic.ToolParam{
		Name:        def.Name,
		Description: anthropic.String(def.Description),
		InputSchema: anthropicSchema,
	}

	if cache {
		param.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}

	return param, nil
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
		cacheRead := int(e.Usage.CacheReadInputTokens)
		cacheWrite := int(e.Usage.CacheCreationInputTokens)

		chunk.Metrics = &message.TokenMetrics{
			TotalTokens: inputTokens + outputTokens + cacheRead + cacheWrite,
			Tokens: message.TokenDetails{
				Input:      inputTokens + cacheRead + cacheWrite, // Summed for OTel compliance
				Output:     outputTokens,
				CacheRead:  cacheRead,
				CacheWrite: cacheWrite,
				Reasoning:  int(e.Usage.OutputTokensDetails.ThinkingTokens),
			},
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

func toToolResultContent(content message.Content) []anthropic.ToolResultBlockParamContentUnion {
	blocks := make([]anthropic.ToolResultBlockParamContentUnion, 0, len(content))
	for _, block := range content {
		switch block := block.(type) {
		case *message.TextBlock:
			blocks = append(blocks, anthropic.ToolResultBlockParamContentUnion{
				OfText: &anthropic.TextBlockParam{
					Text: block.Text,
				},
			})
		case *message.ImageBlock:
			if len(block.Data) > 0 {
				mimeType := block.MIMEType
				if mimeType == "" {
					mimeType = "image/jpeg"
				}
				blocks = append(blocks, anthropic.ToolResultBlockParamContentUnion{
					OfImage: &anthropic.ImageBlockParam{
						Source: anthropic.ImageBlockParamSourceUnion{
							OfBase64: &anthropic.Base64ImageSourceParam{
								Data:      base64.StdEncoding.EncodeToString(block.Data),
								MediaType: anthropic.Base64ImageSourceMediaType(mimeType),
							},
						},
					},
				})
			}
		case *message.DocumentBlock:
			if len(block.Data) > 0 {
				blocks = append(blocks, anthropic.ToolResultBlockParamContentUnion{
					OfDocument: &anthropic.DocumentBlockParam{
						Source: anthropic.DocumentBlockParamSourceUnion{
							OfBase64: &anthropic.Base64PDFSourceParam{
								Data: base64.StdEncoding.EncodeToString(block.Data),
							},
						},
					},
				})
			}
		}
	}
	return blocks
}
