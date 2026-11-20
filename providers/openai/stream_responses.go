package loomopenai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/stream"
	"github.com/masterkeysrd/loom/tool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func (p *Provider) streamResponses(ctx context.Context, request *llm.Request, opts ResponsesOptions) (llm.StreamResponse, error) {
	var client *responses.ResponseService
	if p.client != nil {
		client = &p.client.Responses
	}
	return streamResponses(ctx, client, request, opts, p.Name(), false)
}

// StreamChatGPTPlanResponses adapts the Responses protocol for direct ChatGPT plan
// usage, including caller-managed history and client-side function tools.
func StreamChatGPTPlanResponses(ctx context.Context, client *responses.ResponseService, request *llm.Request, provider string, opts ResponsesOptions) (llm.StreamResponse, error) {
	opts.Store = new(bool)
	req := *request
	req.MaxTokens = 0
	req.Temperature = nil
	req.TopP = nil
	return streamResponses(ctx, client, &req, opts, provider, true)
}

func streamResponses(ctx context.Context, client *responses.ResponseService, request *llm.Request, opts ResponsesOptions, provider string, chatGPTPlan bool) (llm.StreamResponse, error) {
	params, err := toResponseNewParams(request, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to convert responses request: %w", err)
	}
	if chatGPTPlan {
		params.Include = append(params.Include, responses.ResponseIncludableReasoningEncryptedContent)
		var functions, hosted []responses.ToolUnionParam
		for _, tool := range params.Tools {
			if tool.OfFunction != nil {
				functions = append(functions, tool)
			} else {
				hosted = append(hosted, tool)
			}
		}
		params.Tools = hosted
		if len(functions) > 0 {
			params.Input.OfInputItemList = append([]responses.ResponseInputItemUnionParam{
				responses.ResponseInputItemParamOfAdditionalTools(functions),
			}, params.Input.OfInputItemList...)
		}
		for _, item := range params.Input.OfInputItemList {
			if item.OfMessage != nil && item.OfMessage.Role == responses.EasyInputMessageRoleSystem {
				item.OfMessage.Role = responses.EasyInputMessageRoleDeveloper
			}
		}
		if params.Input.OfInputItemList == nil {
			params.Input.OfInputItemList = []responses.ResponseInputItemUnionParam{}
		}
	}

	if sw, ok := stream.WriterFromContext(ctx); ok {
		_ = sw.Write(ctx, stream.Event{
			Name: "on_llm_request",
			Data: map[string]any{
				"provider": provider,
				"payload":  params,
			},
		})
	}

	// OpenAI Specific Span Decoration
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("openai.api.type", "responses"),
	)
	if params.ServiceTier != "" {
		span.SetAttributes(attribute.String("openai.request.service_tier", string(params.ServiceTier)))
	}

	return func(yield func(message.AssistantChunk, error) bool) {
		respStream := client.NewStreaming(ctx, params)
		defer respStream.Close()
		var currentResponseID string

		type toolInfo struct {
			index    int
			callID   string
			name     string
			hasDelta bool
		}
		toolCallsMap := make(map[string]*toolInfo)
		toolCallsByIndex := make(map[int64]*toolInfo)

		for respStream.Next() {
			event := respStream.Current()
			switch event.Type {
			case "response.created":
				if event.Response.ID != "" {
					currentResponseID = event.Response.ID
				}

			case "response.output_item.added":
				if event.Item.Type == "function_call" {
					info := &toolInfo{
						index:  int(event.OutputIndex),
						callID: event.Item.CallID,
						name:   event.Item.Name,
					}
					toolCallsMap[event.Item.ID] = info
					toolCallsByIndex[event.OutputIndex] = info
				}

			case "response.output_text.delta":
				if event.Delta != "" {
					chunk := message.AssistantChunk{
						BaseChunk: message.BaseChunk{ID: currentResponseID},
						Content: []message.Block{
							&message.TextBlock{
								Text: event.Delta,
								Extras: map[string]any{
									ResponseIDKey: currentResponseID,
								},
							},
						},
						Metadata: map[string]any{
							ResponseIDKey: currentResponseID,
						},
					}
					if !yield(chunk, nil) {
						return
					}
				}

			case "response.reasoning_text.delta":
				if event.Delta != "" {
					chunk := message.AssistantChunk{
						BaseChunk: message.BaseChunk{ID: currentResponseID},
						Content: []message.Block{
							&message.ThinkingBlock{
								ID:       event.ItemID,
								Thinking: event.Delta,
								Extras: map[string]any{
									ResponseIDKey: currentResponseID,
								},
							},
						},
						Metadata: map[string]any{
							ResponseIDKey: currentResponseID,
						},
					}
					if !yield(chunk, nil) {
						return
					}
				}

			case "response.function_call_arguments.delta":
				if event.Delta != "" {
					var callID, name string
					idx := int(event.OutputIndex)
					if info, ok := toolCallsMap[event.ItemID]; ok {
						info.hasDelta = true
						callID = info.callID
						name = info.name
						idx = info.index
					} else if info, ok := toolCallsByIndex[event.OutputIndex]; ok {
						info.hasDelta = true
						callID = info.callID
						name = info.name
					}

					chunk := message.AssistantChunk{
						BaseChunk: message.BaseChunk{ID: currentResponseID},
						Content: []message.Block{
							&message.ToolCallChunk{
								Index:     idx,
								ID:        callID,
								Name:      name,
								ArgsChunk: event.Delta,
							},
						},
						Metadata: map[string]any{
							ResponseIDKey: currentResponseID,
						},
					}
					if !yield(chunk, nil) {
						return
					}
				}

			case "response.output_item.done":
				if chatGPTPlan && event.Item.Type == "function_call" {
					info := toolCallsByIndex[event.OutputIndex]
					if info == nil || !info.hasDelta {
						args := event.Item.AsFunctionCall().Arguments
						if args == "" {
							args = "{}"
						}
						chunk := message.AssistantChunk{
							BaseChunk: message.BaseChunk{ID: currentResponseID},
							Content: []message.Block{&message.ToolCallChunk{
								Index: int(event.OutputIndex), ID: event.Item.CallID, Name: event.Item.Name, ArgsChunk: args,
							}},
						}
						if !yield(chunk, nil) {
							return
						}
					}
				}
				// For reasoning items, capture encrypted_content from output_item.done
				if event.Item.Type == "reasoning" && event.Item.EncryptedContent != "" {
					extras := map[string]any{
						EncryptedContentKey: event.Item.EncryptedContent,
						ReasoningIDKey:      event.Item.ID,
						ResponseIDKey:       currentResponseID,
					}
					chunk := message.AssistantChunk{
						BaseChunk: message.BaseChunk{ID: currentResponseID},
						Content: []message.Block{
							&message.ThinkingBlock{
								ID:     event.Item.ID,
								Extras: extras,
							},
						},
						Metadata: map[string]any{
							ResponseIDKey:       currentResponseID,
							EncryptedContentKey: event.Item.EncryptedContent,
							ReasoningIDKey:      event.Item.ID,
						},
					}
					if !yield(chunk, nil) {
						return
					}
				} else if event.Item.Type == "tool_search_call" {
					tsc := event.Item.AsToolSearchCall()
					callID := tsc.CallID
					if callID == "" {
						callID = tsc.ID
					}
					var args map[string]any
					switch a := tsc.Arguments.(type) {
					case map[string]any:
						args = a
					case string:
						_ = json.Unmarshal([]byte(a), &args)
					}
					if args == nil {
						args = make(map[string]any)
					}
					chunk := message.AssistantChunk{
						BaseChunk: message.BaseChunk{ID: currentResponseID},
						Content: []message.Block{
							&message.ServerToolCall{
								ID:   callID,
								Name: "tool_search",
								Args: args,
								Extras: map[string]any{
									ResponseIDKey: currentResponseID,
								},
							},
						},
						Metadata: map[string]any{
							ResponseIDKey: currentResponseID,
						},
					}
					if !yield(chunk, nil) {
						return
					}
				} else if event.Item.Type == "tool_search_output" {
					tso := event.Item.AsToolSearchOutput()
					callID := tso.CallID
					if callID == "" {
						callID = tso.ID
					}
					var refs []message.Block
					for _, t := range tso.Tools {
						if t.Name != "" {
							refs = append(refs, &message.ToolReferenceBlock{
								ToolName: t.Name,
							})
						}
					}
					chunk := message.AssistantChunk{
						BaseChunk: message.BaseChunk{ID: currentResponseID},
						Content: []message.Block{
							&message.ServerToolResult{
								ToolCallID: callID,
								Name:       "tool_search",
								Content:    refs,
								Extras: map[string]any{
									ResponseIDKey: currentResponseID,
								},
							},
						},
						Metadata: map[string]any{
							ResponseIDKey: currentResponseID,
						},
					}
					if !yield(chunk, nil) {
						return
					}
				}

			case "response.completed":
				resp := event.Response
				if resp.ID != "" {
					currentResponseID = resp.ID
				}
				var metrics *message.TokenMetrics
				if resp.Usage.TotalTokens > 0 {
					metrics = &message.TokenMetrics{
						TotalTokens: int(resp.Usage.TotalTokens),
						Tokens: message.TokenDetails{
							Input:     int(resp.Usage.InputTokens),
							Output:    int(resp.Usage.OutputTokens),
							CacheRead: int(resp.Usage.InputTokensDetails.CachedTokens),
							Reasoning: int(resp.Usage.OutputTokensDetails.ReasoningTokens),
						},
					}
				}
				chunk := message.AssistantChunk{
					BaseChunk: message.BaseChunk{ID: currentResponseID},
					Done:      true,
					Metrics:   metrics,
					Metadata: map[string]any{
						ResponseIDKey: currentResponseID,
					},
				}
				if !yield(chunk, nil) || chatGPTPlan {
					return
				}
			case "response.failed":
				if chatGPTPlan {
					yield(message.AssistantChunk{}, fmt.Errorf("%s: response failed (%s): %s", provider, event.Response.Error.Code, event.Response.Error.Message))
					return
				}
			case "response.incomplete":
				if chatGPTPlan {
					yield(message.AssistantChunk{}, fmt.Errorf("%s: response incomplete (%s)", provider, event.Response.IncompleteDetails.Reason))
					return
				}
			case "error":
				if chatGPTPlan {
					yield(message.AssistantChunk{}, fmt.Errorf("%s: response error (%s): %s", provider, event.Code, event.Message))
					return
				}
			}
		}

		if err := respStream.Err(); err != nil {
			yield(message.AssistantChunk{}, fmt.Errorf("stream error: %w", err))
		} else if chatGPTPlan {
			yield(message.AssistantChunk{}, fmt.Errorf("%s: stream ended without response.completed", provider))
		}
	}, nil
}

func toResponseNewParams(request *llm.Request, opts ResponsesOptions) (responses.ResponseNewParams, error) {
	if !modelSupportsToolSearch(request.Model) {
		if opts.ToolSearch {
			return responses.ResponseNewParams{}, fmt.Errorf("model %q does not support tool_search; tool search requires gpt-5.4 or later", request.Model)
		}
		for _, t := range request.Tools {
			if t.DeferLoading {
				return responses.ResponseNewParams{}, fmt.Errorf("model %q does not support deferred tool loading (tool %q); deferred tools require gpt-5.4 or later", request.Model, t.Name)
			}
		}
		if hasToolReferenceBlock(request) {
			return responses.ResponseNewParams{}, fmt.Errorf("model %q does not support tool reference blocks; tool reference blocks require gpt-5.4 or later", request.Model)
		}
		if hasToolDefinitionBlock(request) {
			return responses.ResponseNewParams{}, fmt.Errorf("model %q does not support inline tool definition blocks; inline tool definitions require gpt-5.4 or later", request.Model)
		}
	}

	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(request.Model),
	}

	if opts.Store != nil {
		params.Store = openai.Bool(*opts.Store)
	}

	if request.MaxTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(request.MaxTokens))
	}

	if request.Temperature != nil {
		params.Temperature = openai.Float(float64(*request.Temperature))
	}

	if request.TopP != nil {
		params.TopP = openai.Float(float64(*request.TopP))
	}

	if request.Thinking != nil && request.Thinking.Effort != "" {
		params.Reasoning = shared.ReasoningParam{
			Effort: shared.ReasoningEffort(request.Thinking.Effort),
		}
	}

	if ext, ok := request.Extensions[PromptCache{}.ExtensionID()]; ok {
		if pc, ok := ext.(PromptCache); ok && pc.Key != "" {
			params.PromptCacheKey = openai.String(pc.Key)
		}
	}

	if request.ResponseFormat == "json_object" {
		rf := shared.NewResponseFormatJSONObjectParam()
		params.Text = responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONObject: &rf,
			},
		}
	} else if request.ResponseSchema != nil {
		name := request.ResponseSchema.Title
		if name == "" {
			name = "response"
		}
		var schemaMap map[string]any
		data, _ := json.Marshal(request.ResponseSchema)
		_ = json.Unmarshal(data, &schemaMap)

		params.Text = responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
					Name:   name,
					Schema: schemaMap,
					Strict: openai.Bool(true),
				},
			},
		}
	}

	// 1. Separate instructions (System messages) and conversation messages
	var instructions []string
	var convMessages []message.Message
	seenNonSystem := false
	for _, msg := range request.Messages {
		if sys, ok := msg.(*message.System); ok {
			if err := validateNoMediaBlocks(request.Model, "system messages", sys.Content); err != nil {
				return responses.ResponseNewParams{}, err
			}
			refNames := extractToolReferences(sys.Content)
			inlineDefs := extractToolDefinitions(sys.Content)
			if !seenNonSystem && len(refNames) == 0 && len(inlineDefs) == 0 {
				if text := sys.Content.Text(); text != "" {
					instructions = append(instructions, text)
				}
			} else {
				convMessages = append(convMessages, msg)
			}
		} else {
			seenNonSystem = true
			convMessages = append(convMessages, msg)
		}
	}

	if len(instructions) > 0 {
		params.Instructions = openai.String(strings.Join(instructions, "\n\n"))
	}

	// 2. Handle PreviousResponseID & Message Pruning (only when Store is not explicitly false)
	isStoreFalse := opts.Store != nil && !*opts.Store
	if !isStoreFalse {
		prevID := opts.PreviousResponseID
		if prevID == "" && opts.UsePreviousResponseID {
			for i, msg := range slices.Backward(convMessages) {

				if msg.Role() == message.RoleAssistant {
					if id := extractResponseID(msg); id != "" {
						prevID = id
						convMessages = convMessages[i+1:]
						break
					}
				}
			}
		}
		if prevID != "" {
			params.PreviousResponseID = openai.String(prevID)
		}
	}

	// 3. Convert remaining messages to input items
	var inputItems []responses.ResponseInputItemUnionParam
	for _, msg := range convMessages {
		switch m := msg.(type) {
		case *message.System:
			if text := m.Content.Text(); text != "" {
				inputItems = append(inputItems, responses.ResponseInputItemParamOfMessage(
					text,
					responses.EasyInputMessageRoleSystem,
				))
			}
			refNames := extractToolReferences(m.Content)
			inlineDefs := extractToolDefinitions(m.Content)
			if len(refNames) > 0 || len(inlineDefs) > 0 {
				var additionTools []responses.ToolUnionParam
				for _, name := range refNames {
					additionTools = append(additionTools, resolveToolParam(name, request.Tools, opts.StrictTools))
				}
				for _, def := range inlineDefs {
					additionTools = append(additionTools, toolDefinitionToParam(def, opts.StrictTools))
				}
				inputItems = append(inputItems, responses.ResponseInputItemParamOfAdditionalTools(additionTools))
			}

		case *message.User:
			refNames := extractToolReferences(m.Content)
			inlineDefs := extractToolDefinitions(m.Content)
			hasUserContent := false
			for _, b := range m.Content {
				switch b.(type) {
				case *message.TextBlock, *message.ImageBlock, *message.AudioBlock, *message.VideoBlock, *message.DocumentBlock:
					hasUserContent = true
				}
			}
			if hasUserContent || (len(refNames) == 0 && len(inlineDefs) == 0) {
				userItem, err := toResponseUserInputItemWithModel(request.Model, m)
				if err != nil {
					return responses.ResponseNewParams{}, err
				}
				inputItems = append(inputItems, userItem)
			}
			if len(refNames) > 0 || len(inlineDefs) > 0 {
				var additionTools []responses.ToolUnionParam
				for _, name := range refNames {
					additionTools = append(additionTools, resolveToolParam(name, request.Tools, opts.StrictTools))
				}
				for _, def := range inlineDefs {
					additionTools = append(additionTools, toolDefinitionToParam(def, opts.StrictTools))
				}
				inputItems = append(inputItems, responses.ResponseInputItemParamOfAdditionalTools(additionTools))
			}

		case *message.Assistant:
			if err := validateNoMediaBlocks(request.Model, "assistant messages", m.Content); err != nil {
				return responses.ResponseNewParams{}, err
			}

			// Include reasoning encrypted_content if present (critical when store: false)
			for _, block := range m.Content {
				if tb, ok := block.(*message.ThinkingBlock); ok {
					if enc, ok := tb.Extras[EncryptedContentKey].(string); ok && enc != "" {
						rParam := responses.ResponseReasoningItemParam{
							EncryptedContent: openai.String(enc),
							Summary:          []responses.ResponseReasoningItemSummaryParam{},
						}
						if rID, ok := tb.Extras[ReasoningIDKey].(string); ok && rID != "" {
							rParam.ID = rID
						}
						inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
							OfReasoning: &rParam,
						})
					}
				}
			}

			// Include assistant text
			if text := m.Content.Text(); text != "" {
				inputItems = append(inputItems, responses.ResponseInputItemParamOfMessage(
					text,
					responses.EasyInputMessageRoleAssistant,
				))
			}

			// Include tool calls / server tool calls / server tool results
			for _, block := range m.Content {
				if tc, ok := block.(*message.ToolCall); ok {
					args := "{}"
					if len(tc.Args) > 0 {
						if b, err := json.Marshal(tc.Args); err == nil {
							args = string(b)
						}
					}
					inputItems = append(inputItems, responses.ResponseInputItemParamOfFunctionCall(
						args,
						tc.ID,
						tc.Name,
					))
				} else if stc, ok := block.(*message.ServerToolCall); ok {
					args := stc.Args
					if args == nil {
						args = make(map[string]any)
					}
					stcParam := responses.ResponseInputItemToolSearchCallParam{
						Arguments: args,
						Status:    "completed",
						Execution: "server",
					}
					if stc.ID != "" {
						stcParam.CallID = openai.String(stc.ID)
					}
					inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
						OfToolSearchCall: &stcParam,
					})
				} else if str, ok := block.(*message.ServerToolResult); ok {
					tsoTools := make([]responses.ResponseToolSearchOutputItemParamToolUnion, 0)
					for _, b := range str.Content {
						if ref, ok := b.(*message.ToolReferenceBlock); ok {
							resolved := resolveToolParam(ref.ToolName, request.Tools, opts.StrictTools)
							if resolved.OfFunction != nil {
								tsoTools = append(tsoTools, responses.ResponseToolSearchOutputItemParamToolUnion{
									OfFunction: resolved.OfFunction,
								})
							}
						} else if tdb, ok := b.(*message.ToolDefinitionBlock); ok {
							funcParam := toolDefinitionToParam(tdb, opts.StrictTools)
							if funcParam.OfFunction != nil {
								tsoTools = append(tsoTools, responses.ResponseToolSearchOutputItemParamToolUnion{
									OfFunction: funcParam.OfFunction,
								})
							}
						}
					}
					tsoParam := responses.ResponseToolSearchOutputItemParam{
						Tools:     tsoTools,
						Status:    responses.ResponseToolSearchOutputItemParamStatusCompleted,
						Execution: responses.ResponseToolSearchOutputItemParamExecutionServer,
					}
					if str.ToolCallID != "" {
						tsoParam.CallID = openai.String(str.ToolCallID)
					}
					inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
						OfToolSearchOutput: &tsoParam,
					})
				}
			}

		case *message.Tool:
			toolStr, err := toToolStringWithModel(request.Model, m)
			if err != nil {
				return responses.ResponseNewParams{}, err
			}
			outputStr := ensureValidContent(toolStr)
			inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: openai.String(m.ToolCallID),
					Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
						OfString: openai.String(outputStr),
					},
				},
			})
			refNames := extractToolReferences(m.Content)
			inlineDefs := extractToolDefinitions(m.Content)
			if len(refNames) > 0 || len(inlineDefs) > 0 {
				var additionTools []responses.ToolUnionParam
				for _, name := range refNames {
					additionTools = append(additionTools, resolveToolParam(name, request.Tools, opts.StrictTools))
				}
				for _, def := range inlineDefs {
					additionTools = append(additionTools, toolDefinitionToParam(def, opts.StrictTools))
				}
				inputItems = append(inputItems, responses.ResponseInputItemParamOfAdditionalTools(additionTools))
			}
		}
	}

	if len(inputItems) > 0 {
		params.Input = responses.ResponseNewParamsInputUnion{
			OfInputItemList: inputItems,
		}
	}

	// 4. Tools (Built-in + Custom Function Tools)
	var tools []responses.ToolUnionParam
	if opts.WebSearch {
		tools = append(tools, responses.ToolParamOfWebSearch(responses.WebSearchToolTypeWebSearch))
	}
	if opts.FileSearch != nil && len(opts.FileSearch.VectorStoreIDs) > 0 {
		tools = append(tools, responses.ToolParamOfFileSearch(opts.FileSearch.VectorStoreIDs))
	}
	if opts.CodeInterpreter {
		tools = append(tools, responses.ToolParamOfCodeInterpreter("auto"))
	}
	if opts.ToolSearch {
		tools = append(tools, responses.ToolUnionParam{
			OfToolSearch: &responses.ToolSearchToolParam{
				Execution: responses.ToolSearchToolExecutionServer,
			},
		})
	}

	inlineToolNames := make(map[string]bool)
	for _, msg := range request.Messages {
		for _, b := range msg.GetContent() {
			if tdb, ok := b.(*message.ToolDefinitionBlock); ok {
				inlineToolNames[tdb.Name] = true
			}
		}
	}

	for _, t := range request.Tools {
		if inlineToolNames[t.Name] {
			continue
		}
		var paramsMap map[string]any
		if t.InputSchema != nil {
			data, _ := json.Marshal(t.InputSchema)
			_ = json.Unmarshal(data, &paramsMap)
		}
		if paramsMap == nil {
			paramsMap = make(map[string]any)
		}
		if paramsMap["type"] == "object" {
			if paramsMap["properties"] == nil {
				paramsMap["properties"] = make(map[string]any)
			}
			if paramsMap["additionalProperties"] == nil {
				paramsMap["additionalProperties"] = false
			}
		}
		funcTool := responses.ToolParamOfFunction(t.Name, paramsMap, opts.StrictTools)
		if t.Description != "" {
			funcTool.OfFunction.Description = openai.String(t.Description)
		}
		if t.DeferLoading {
			funcTool.OfFunction.DeferLoading = openai.Bool(true)
		}
		tools = append(tools, funcTool)
	}

	if len(tools) > 0 {
		params.Tools = tools
	}

	return params, nil
}

func extractToolReferences(content []message.Block) []string {
	var names []string
	for _, block := range content {
		if ref, ok := block.(*message.ToolReferenceBlock); ok {
			names = append(names, ref.ToolName)
		} else if str, ok := block.(*message.ServerToolResult); ok {
			for _, b := range str.Content {
				if ref, ok := b.(*message.ToolReferenceBlock); ok {
					names = append(names, ref.ToolName)
				}
			}
		}
	}
	return names
}

func resolveToolParam(name string, tools []tool.Definition, strict bool) responses.ToolUnionParam {
	for _, t := range tools {
		if t.Name == name {
			var paramsMap map[string]any
			if t.InputSchema != nil {
				data, _ := json.Marshal(t.InputSchema)
				_ = json.Unmarshal(data, &paramsMap)
			}
			if paramsMap == nil {
				paramsMap = make(map[string]any)
			}
			if paramsMap["type"] == "object" {
				if paramsMap["properties"] == nil {
					paramsMap["properties"] = make(map[string]any)
				}
				if paramsMap["additionalProperties"] == nil {
					paramsMap["additionalProperties"] = false
				}
			}
			funcTool := responses.ToolParamOfFunction(t.Name, paramsMap, strict)
			if t.Description != "" {
				funcTool.OfFunction.Description = openai.String(t.Description)
			}
			return funcTool
		}
	}
	return responses.ToolParamOfFunction(name, map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}, strict)
}

func extractToolDefinitions(content []message.Block) []*message.ToolDefinitionBlock {
	var defs []*message.ToolDefinitionBlock
	for _, block := range content {
		if tdb, ok := block.(*message.ToolDefinitionBlock); ok {
			defs = append(defs, tdb)
		} else if str, ok := block.(*message.ServerToolResult); ok {
			for _, b := range str.Content {
				if tdb, ok := b.(*message.ToolDefinitionBlock); ok {
					defs = append(defs, tdb)
				}
			}
		}
	}
	return defs
}

func toolDefinitionToParam(b *message.ToolDefinitionBlock, strict bool) responses.ToolUnionParam {
	var paramsMap map[string]any
	if b.InputSchema != nil {
		data, _ := json.Marshal(b.InputSchema)
		_ = json.Unmarshal(data, &paramsMap)
	}
	if paramsMap == nil {
		paramsMap = make(map[string]any)
	}
	if paramsMap["type"] == "object" {
		if paramsMap["properties"] == nil {
			paramsMap["properties"] = make(map[string]any)
		}
		if paramsMap["additionalProperties"] == nil {
			paramsMap["additionalProperties"] = false
		}
	}
	funcTool := responses.ToolParamOfFunction(b.Name, paramsMap, strict)
	if b.Description != "" {
		funcTool.OfFunction.Description = openai.String(b.Description)
	}
	return funcTool
}

func extractResponseID(msg message.Message) string {
	if meta := msg.GetMetadata(); meta != nil {
		if id, ok := meta[ResponseIDKey].(string); ok && id != "" {
			return id
		}
	}
	for _, block := range msg.GetContent() {
		switch b := block.(type) {
		case *message.TextBlock:
			if id, ok := b.Extras[ResponseIDKey].(string); ok && id != "" {
				return id
			}
		case *message.ThinkingBlock:
			if id, ok := b.Extras[ResponseIDKey].(string); ok && id != "" {
				return id
			}
		case *message.ToolCall:
			if id, ok := b.Extras[ResponseIDKey].(string); ok && id != "" {
				return id
			}
		}
	}
	return msg.GetID()
}

func toResponseUserInputItem(m *message.User) (responses.ResponseInputItemUnionParam, error) {
	return toResponseUserInputItemWithModel("", m)
}

func toResponseUserInputItemWithModel(model string, m *message.User) (responses.ResponseInputItemUnionParam, error) {
	var contentList []responses.ResponseInputContentUnionParam
	for _, block := range m.Content {
		switch b := block.(type) {
		case *message.TextBlock:
			if b != nil {
				contentList = append(contentList, responses.ResponseInputContentParamOfInputText(b.Text))
			}
		case *message.ImageBlock:
			if b == nil || (b.URL == "" && len(b.Data) == 0) {
				return responses.ResponseInputItemUnionParam{}, profile.NewUnsupportedModalityError("openai", model, profile.ModalityImage, "image block is empty: missing both data and url")
			}
			url := b.URL
			if url == "" {
				mime := b.MIMEType
				if mime == "" {
					mime = "image/jpeg"
				}
				url = fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(b.Data))
			}
			contentList = append(contentList, responses.ResponseInputContentUnionParam{
				OfInputImage: &responses.ResponseInputImageParam{
					ImageURL: openai.String(url),
				},
			})
		case *message.AudioBlock:
			return responses.ResponseInputItemUnionParam{}, profile.NewUnsupportedModalityError("openai", model, profile.ModalityAudio, "openai responses API user messages do not support audio inputs")
		case *message.VideoBlock:
			return responses.ResponseInputItemUnionParam{}, profile.NewUnsupportedModalityError("openai", model, profile.ModalityVideo, "openai responses API does not support video inputs")
		case *message.DocumentBlock:
			if b == nil || (b.URL == "" && len(b.Data) == 0) {
				return responses.ResponseInputItemUnionParam{}, profile.NewUnsupportedModalityError("openai", model, profile.ModalityPDF, "document block is empty: missing both data and url")
			}
			fileParam := responses.ResponseInputFileParam{}
			if b.URL != "" {
				fileParam.FileURL = openai.String(b.URL)
			}
			if len(b.Data) > 0 {
				fileParam.FileData = openai.String(base64.StdEncoding.EncodeToString(b.Data))
			}
			contentList = append(contentList, responses.ResponseInputContentUnionParam{
				OfInputFile: &fileParam,
			})
		}
	}

	if len(contentList) == 1 && contentList[0].OfInputText != nil {
		return responses.ResponseInputItemParamOfMessage(
			contentList[0].OfInputText.Text,
			responses.EasyInputMessageRoleUser,
		), nil
	}

	if len(contentList) > 0 {
		return responses.ResponseInputItemParamOfInputMessage(
			contentList,
			"user",
		), nil
	}

	return responses.ResponseInputItemParamOfMessage(
		"",
		responses.EasyInputMessageRoleUser,
	), nil
}
