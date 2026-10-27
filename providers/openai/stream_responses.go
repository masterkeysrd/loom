package loomopenai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/stream"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func (p *Provider) streamResponses(ctx context.Context, request *llm.Request, opts ResponsesOptions) (llm.StreamResponse, error) {
	params, err := toResponseNewParams(request, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to convert responses request: %w", err)
	}

	if sw, ok := stream.WriterFromContext(ctx); ok {
		_ = sw.Write(ctx, stream.Event{
			Name: "on_llm_request",
			Data: map[string]any{
				"provider": "openai",
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
		respStream := p.client.Responses.NewStreaming(ctx, params)
		var currentResponseID string

		type toolInfo struct {
			index  int
			callID string
			name   string
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
						callID = info.callID
						name = info.name
						idx = info.index
					} else if info, ok := toolCallsByIndex[event.OutputIndex]; ok {
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
				if !yield(chunk, nil) {
					return
				}
			}
		}

		if err := respStream.Err(); err != nil {
			yield(message.AssistantChunk{}, fmt.Errorf("stream error: %w", err))
		}
	}, nil
}

func toResponseNewParams(request *llm.Request, opts ResponsesOptions) (responses.ResponseNewParams, error) {
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
	for _, msg := range request.Messages {
		if sys, ok := msg.(*message.System); ok {
			if text := sys.Content.Text(); text != "" {
				instructions = append(instructions, text)
			}
		} else {
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
			for i := len(convMessages) - 1; i >= 0; i-- {
				msg := convMessages[i]
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
		case *message.User:
			userItem, err := toResponseUserInputItem(m)
			if err != nil {
				return responses.ResponseNewParams{}, err
			}
			inputItems = append(inputItems, userItem)

		case *message.Assistant:
			// Include reasoning encrypted_content if present (critical when store: false)
			for _, block := range m.Content {
				if tb, ok := block.(*message.ThinkingBlock); ok {
					if enc, ok := tb.Extras[EncryptedContentKey].(string); ok && enc != "" {
						rParam := responses.ResponseReasoningItemParam{
							EncryptedContent: openai.String(enc),
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

			// Include tool calls
			for _, block := range m.Content {
				if tc, ok := block.(*message.ToolCall); ok {
					argsBytes, _ := json.Marshal(tc.Args)
					inputItems = append(inputItems, responses.ResponseInputItemParamOfFunctionCall(
						string(argsBytes),
						tc.ID,
						tc.Name,
					))
				}
			}

		case *message.Tool:
			outputStr := ensureValidContent(toToolString(m))
			inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: openai.String(m.ToolCallID),
					Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
						OfString: openai.String(outputStr),
					},
				},
			})
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

	for _, t := range request.Tools {
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
		tools = append(tools, funcTool)
	}

	if len(tools) > 0 {
		params.Tools = tools
	}

	return params, nil
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
	var contentList []responses.ResponseInputContentUnionParam
	for _, block := range m.Content {
		switch b := block.(type) {
		case *message.TextBlock:
			contentList = append(contentList, responses.ResponseInputContentParamOfInputText(b.Text))
		case *message.ImageBlock:
			url := b.URL
			if url == "" && len(b.Data) > 0 {
				mime := b.MIMEType
				if mime == "" {
					mime = "image/jpeg"
				}
				url = fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(b.Data))
			}
			if url != "" {
				contentList = append(contentList, responses.ResponseInputContentUnionParam{
					OfInputImage: &responses.ResponseInputImageParam{
						ImageURL: openai.String(url),
					},
				})
			}
		case *message.DocumentBlock:
			if b.URL != "" {
				contentList = append(contentList, responses.ResponseInputContentUnionParam{
					OfInputFile: &responses.ResponseInputFileParam{
						FileURL: openai.String(b.URL),
					},
				})
			}
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
