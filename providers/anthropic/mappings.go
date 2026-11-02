package loomanthropic

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
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
	var cacheAuto bool
	if ext, ok := request.Extensions[PromptCaching{}.ExtensionID()]; ok {
		if pc, ok := ext.(PromptCaching); ok {
			cacheHeader = pc.CacheHeader
			cacheAuto = pc.Auto
		}
	}

	var toolSearch *ToolSearch
	if ext, ok := request.Extensions[ToolSearch{}.ExtensionID()]; ok {
		if ts, ok := ext.(ToolSearch); ok {
			toolSearch = &ts
		} else if ts, ok := ext.(*ToolSearch); ok {
			toolSearch = ts
		}
	}

	supportsToolSearch := modelSupportsToolSearch(request.Model)
	if !supportsToolSearch {
		if toolSearch != nil {
			return anthropic.MessageNewParams{}, fmt.Errorf("model %q does not support server-side tool search", request.Model)
		}
		for _, tool := range request.Tools {
			if tool.DeferLoading {
				return anthropic.MessageNewParams{}, fmt.Errorf("model %q does not support deferred tool loading (tool %q)", request.Model, tool.Name)
			}
		}
		if slices.ContainsFunc(request.Messages, hasToolReferenceBlock) {
			return anthropic.MessageNewParams{}, fmt.Errorf("model %q does not support tool reference blocks", request.Model)
		}
	}

	var pendingToolCallIDs []string
	flushPending := func() {
		for _, id := range pendingToolCallIDs {
			anthropicReq.Messages = append(anthropicReq.Messages, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(id, "", false),
			))
		}
		pendingToolCallIDs = nil
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
			if len(anthropicReq.Messages) == 0 {
				anthropicReq.System = append(anthropicReq.System, toTextBlocksParams(msg.Content, false)...)
			} else {
				flushPending()
				anthropicReq.Messages = append(anthropicReq.Messages, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleSystem,
					Content: toContentBlocksParams(msg.Content, cache),
				})
			}
		case *message.Assistant:
			flushPending()
			anthropicReq.Messages = append(anthropicReq.Messages, anthropic.NewAssistantMessage(
				toContentBlocksParams(msg.Content, cache)...,
			))

			for _, block := range msg.ToolCalls() {
				pendingToolCallIDs = append(pendingToolCallIDs, block.ID)
			}
		case *message.User:
			flushPending()
			anthropicReq.Messages = append(anthropicReq.Messages, anthropic.NewUserMessage(
				toContentBlocksParams(msg.Content, cache)...,
			))
		case *message.Tool:
			foundIdx := -1
			for i, id := range pendingToolCallIDs {
				if id == msg.ToolCallID {
					foundIdx = i
					break
				}
			}
			if foundIdx != -1 {
				pendingToolCallIDs = append(pendingToolCallIDs[:foundIdx], pendingToolCallIDs[foundIdx+1:]...)
			}

			// Consistently use ToolResultBlockParam for all tool outputs.
			var contentBlocks []anthropic.ToolResultBlockParamContentUnion
			if len(msg.Content) > 0 {
				contentBlocks = toToolResultContent(msg.Content)
			} else if msg.StructuredContent != nil {
				data, _ := msg.CanonicalStructuredContent()
				contentBlocks = []anthropic.ToolResultBlockParamContentUnion{
					{
						OfText: &anthropic.TextBlockParam{
							Text: string(data),
						},
					},
				}
			}
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
		}
	}

	flushPending()

	if cacheHeader && len(anthropicReq.System) > 0 {
		anthropicReq.System[len(anthropicReq.System)-1].CacheControl = anthropic.NewCacheControlEphemeralParam()
	}

	if cacheAuto {
		anthropicReq.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}

	lastNonDeferredIdx := -1
	if toolSearch == nil && cacheHeader {
		for i, v := range slices.Backward(request.Tools) {
			if !v.DeferLoading {
				lastNonDeferredIdx = i
				break
			}
		}
	}

	if len(request.Tools) > 0 {
		anthropicReq.Tools = make([]anthropic.ToolUnionParam, 0, len(request.Tools))
		for i, tool := range request.Tools {
			cache := i == lastNonDeferredIdx
			toolParam, err := toToolParam(tool, cache)
			if err != nil {
				return anthropic.MessageNewParams{}, fmt.Errorf("failed to convert tool definition: %w", err)
			}

			anthropicReq.Tools = append(anthropicReq.Tools, anthropic.ToolUnionParam{
				OfTool: &toolParam,
			})
		}
	}

	if toolSearch != nil {
		if anthropicReq.Tools == nil {
			anthropicReq.Tools = make([]anthropic.ToolUnionParam, 0, 1)
		}
		if toolSearch.Strategy == ToolSearchRegex {
			tsTool := anthropic.ToolUnionParamOfToolSearchToolRegex20251119(anthropic.ToolSearchToolRegex20251119TypeToolSearchToolRegex20251119)
			if cacheHeader {
				tsTool.OfToolSearchToolRegex20251119.CacheControl = anthropic.NewCacheControlEphemeralParam()
			}
			anthropicReq.Tools = append(anthropicReq.Tools, tsTool)
		} else {
			tsTool := anthropic.ToolUnionParamOfToolSearchToolBm25_20251119(anthropic.ToolSearchToolBm25_20251119TypeToolSearchToolBm25_20251119)
			if cacheHeader {
				tsTool.OfToolSearchToolBm25_20251119.CacheControl = anthropic.NewCacheControlEphemeralParam()
			}
			anthropicReq.Tools = append(anthropicReq.Tools, tsTool)
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
		var set bool
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
			set = true
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
				set = true
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
				set = true
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
			set = true
		case *message.ToolReferenceBlock:
			addition := anthropic.BetaRequestToolAdditionBlockParam{
				Tool: anthropic.BetaRequestToolAdditionBlockToolUnionParam{
					OfToolReference: &anthropic.BetaToolChangeToolReferenceParam{
						Name: block.ToolName,
					},
				},
			}
			if cache && i == len(content)-1 {
				addition.CacheControl = anthropic.NewBetaCacheControlEphemeralParam()
			}
			b = param.Override[anthropic.ContentBlockParamUnion](addition)
			set = true
		case *message.ServerToolCall:
			args := block.Args
			if args == nil {
				args = make(map[string]any)
			}
			stub := anthropic.ServerToolUseBlockParam{
				ID:    block.ID,
				Input: args,
				Name:  anthropic.ServerToolUseBlockParamName(block.Name),
			}
			if cache && i == len(content)-1 {
				stub.CacheControl = anthropic.NewCacheControlEphemeralParam()
			}
			b = anthropic.ContentBlockParamUnion{
				OfServerToolUse: &stub,
			}
			set = true
		case *message.ServerToolResult:
			if block.IsError {
				errorCode := anthropic.ToolSearchToolResultErrorCodeUnavailable
				if block.Extras != nil {
					if ec, ok := block.Extras["error_code"].(string); ok && ec != "" {
						errorCode = anthropic.ToolSearchToolResultErrorCode(ec)
					}
				}
				errText := block.Content.Text()
				if errText == "" {
					if s, ok := block.Output.(string); ok {
						errText = s
					}
				}
				errParam := anthropic.ToolSearchToolResultErrorParam{
					ErrorCode: errorCode,
				}
				if errText != "" {
					errParam.ErrorMessage = anthropic.String(errText)
				}
				trbp := anthropic.ToolSearchToolResultBlockParam{
					ToolUseID: block.ToolCallID,
					Content: anthropic.ToolSearchToolResultBlockParamContentUnion{
						OfRequestToolSearchToolResultError: &errParam,
					},
				}
				if cache && i == len(content)-1 {
					trbp.CacheControl = anthropic.NewCacheControlEphemeralParam()
				}
				b = anthropic.ContentBlockParamUnion{
					OfToolSearchToolResult: &trbp,
				}
			} else {
				var refs []anthropic.ToolReferenceBlockParam
				for _, c := range block.Content {
					if tr, ok := c.(*message.ToolReferenceBlock); ok {
						refs = append(refs, anthropic.ToolReferenceBlockParam{
							ToolName: tr.ToolName,
						})
					}
				}
				resParam := anthropic.ToolSearchToolSearchResultBlockParam{
					ToolReferences: refs,
				}
				trbp := anthropic.ToolSearchToolResultBlockParam{
					ToolUseID: block.ToolCallID,
					Content: anthropic.ToolSearchToolResultBlockParamContentUnion{
						OfRequestToolSearchToolSearchResultBlock: &resParam,
					},
				}
				if cache && i == len(content)-1 {
					trbp.CacheControl = anthropic.NewCacheControlEphemeralParam()
				}
				b = anthropic.ContentBlockParamUnion{
					OfToolSearchToolResult: &trbp,
				}
			}
			set = true
		}

		if set {
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

	if def.DeferLoading {
		param.DeferLoading = anthropic.Bool(true)
	}

	if cache && !def.DeferLoading {
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
				// Anthropic input_tokens excludes cache tokens; add them back so
				// Input is the total prompt size (cache tokens are a subset).
				Input:      inputTokens + cacheRead + cacheWrite,
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
		case *message.ToolReferenceBlock:
			blocks = append(blocks, anthropic.ToolResultBlockParamContentUnion{
				OfToolReference: &anthropic.ToolReferenceBlockParam{
					ToolName: block.ToolName,
				},
			})
		}
	}
	return blocks
}

// fromToolSearchToolResultBlock converts an Anthropic ToolSearchToolResultBlock into a *message.ServerToolResult.
func fromToolSearchToolResultBlock(cb anthropic.ToolSearchToolResultBlock) *message.ServerToolResult {
	if cb.Content.Type == "tool_search_tool_result_error" {
		errBlock := cb.Content.AsResponseToolSearchToolResultError()
		return &message.ServerToolResult{
			ToolCallID: cb.ToolUseID,
			Name:       "tool_search_tool",
			IsError:    true,
			Content: []message.Block{
				&message.TextBlock{Text: errBlock.ErrorMessage},
			},
			Extras: map[string]any{
				"error_code": string(errBlock.ErrorCode),
			},
		}
	}

	resBlock := cb.Content.AsResponseToolSearchToolSearchResultBlock()
	refs := make([]message.Block, 0, len(resBlock.ToolReferences))
	for _, ref := range resBlock.ToolReferences {
		refs = append(refs, &message.ToolReferenceBlock{
			ToolName: ref.ToolName,
		})
	}
	return &message.ServerToolResult{
		ToolCallID: cb.ToolUseID,
		Name:       "tool_search_tool",
		Content:    refs,
	}
}

// hasMidConversationToolAddition checks if any mid-conversation message in request.Messages
// contains a *message.ToolReferenceBlock (outside *message.Tool), which requires the
// inline-tools-2026-09-15 beta header.
func hasMidConversationToolAddition(request *llm.Request) bool {
	if request == nil {
		return false
	}
	startedMessages := false
	for _, msg := range request.Messages {
		switch m := msg.(type) {
		case *message.System:
			if startedMessages {
				for _, b := range m.Content {
					if _, ok := b.(*message.ToolReferenceBlock); ok {
						return true
					}
				}
			}
		case *message.Tool:
			// Tool results contain ToolReferenceBlock as result content, not tool addition.
			startedMessages = true
		case *message.User:
			startedMessages = true
			for _, b := range m.Content {
				if _, ok := b.(*message.ToolReferenceBlock); ok {
					return true
				}
			}
		case *message.Assistant:
			startedMessages = true
			for _, b := range m.Content {
				if _, ok := b.(*message.ToolReferenceBlock); ok {
					return true
				}
			}
		}
	}
	return false
}

// requestOptions returns the Anthropic request options required for the given request.
func requestOptions(request *llm.Request) []option.RequestOption {
	var opts []option.RequestOption
	if hasMidConversationToolAddition(request) {
		opts = append(opts, option.WithHeaderAdd("anthropic-beta", string(anthropic.AnthropicBetaInlineTools2026_09_15)))
	}
	return opts
}

func hasToolReferenceBlock(msg message.Message) bool {
	if msg == nil {
		return false
	}
	for _, block := range msg.GetContent() {
		if _, ok := block.(*message.ToolReferenceBlock); ok {
			return true
		}
		if str, ok := block.(*message.ServerToolResult); ok {
			for _, subBlock := range str.Content {
				if _, ok := subBlock.(*message.ToolReferenceBlock); ok {
					return true
				}
			}
		}
	}
	return false
}

var supportedToolSearchPrefixes = []string{
	"claude-fable-5",
	"claude-mythos-5",
	"claude-opus-5",
	"claude-sonnet-5",
	"claude-haiku-5",
	"claude-opus-4-5",
	"claude-opus-4.5",
	"claude-opus-4-6",
	"claude-opus-4.6",
	"claude-opus-4-7",
	"claude-opus-4.7",
	"claude-opus-4-8",
	"claude-opus-4.8",
	"claude-sonnet-4-5",
	"claude-sonnet-4.5",
	"claude-sonnet-4-6",
	"claude-sonnet-4.6",
	"claude-haiku-4-5",
	"claude-haiku-4.5",
}

// modelSupportsToolSearch reports whether the model supports native Anthropic tool search
// and deferred tool loading.
func modelSupportsToolSearch(modelName string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	for _, prefix := range supportedToolSearchPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}
