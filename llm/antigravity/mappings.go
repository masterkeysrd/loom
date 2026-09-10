package loomantigravity

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/tool"
)

const (
	// ThoughtSignatureKey is the key in message block Extras storing Google's thought signature.
	ThoughtSignatureKey = "thought_signature"

	// DummyThoughtSignature is Google's documented fallback signature for functionCall parts
	// in conversational history when an explicit signature was not preserved.
	// See: https://ai.google.dev/gemini-api/docs/thought-signatures
	DummyThoughtSignature = "ErQCCrECAdHtim8MtxgeMCRCiNiyoyImxtYAEDzz4NXOr/HSL3rA7rPPvHWZCm+T9VSDYh/mt9lESoH4wQh/ca1zDtWTN6XOL1+S3krYLQeqp47RV/b1eSq5jdZF28S4Lb7w4A3/EFdybc4SFb2/YhMm+CulYLmLA4Tr4VSu0eMWgxM3HVt6u0jECf5BbXzj0qjJ32tEQYJvKvV8H1tCHvB6J+RZhsDr+TcyOCaqxDoR4WKxXYxNRZb3hYTuCnBEDPhn1lROumVaghi9nEIgc17z002zLoyqIptlLfIVw70FXkCLsPUSL1SjPQYtGL8PVncVajeqGogRD/eZSVZ1Zr5tshxh3DQ+JAYNcrHaRHWC4Hg0H6oftYx+JdJD9B/81NYV9jyGxP7zHKFHOELl0IUP5GEXP9I="
)

// toGenerateContentRequest converts a generic [llm.Request] into the wire-format
// GenerateContentRequest expected by Cloud Code / Antigravity.
func toGenerateContentRequest(req *llm.Request) (GenerateContentRequest, error) {
	out := GenerateContentRequest{}

	// 1. Generation Config
	cfg := &GenerationConfig{}
	hasConfig := false

	if req.MaxTokens > 0 {
		cfg.MaxOutputTokens = int32(req.MaxTokens)
		hasConfig = true
	}
	if req.Temperature != nil {
		cfg.Temperature = req.Temperature
		hasConfig = true
	}
	if req.TopP != nil {
		cfg.TopP = req.TopP
		hasConfig = true
	}
	if req.TopK != nil {
		topK := float32(*req.TopK)
		cfg.TopK = &topK
		hasConfig = true
	}
	if len(req.Stop) > 0 {
		cfg.StopSequences = req.Stop
		hasConfig = true
	}
	if req.ResponseFormat == "json_object" {
		cfg.ResponseMIMEType = "application/json"
		hasConfig = true
	}
	if req.ResponseSchema != nil {
		cfg.ResponseMIMEType = "application/json"
		sanitized, err := sanitizeSchema(req.ResponseSchema)
		if err != nil {
			return out, fmt.Errorf("response schema: %w", err)
		}
		cfg.ResponseSchema = sanitized
		hasConfig = true
	}
	if req.Thinking != nil {
		cfg.ThinkingConfig = &ThinkingConfig{
			IncludeThoughts: true,
		}
		if req.Thinking.Budget > 0 {
			b := int32(req.Thinking.Budget)
			cfg.ThinkingConfig.ThinkingBudget = &b
		}
		if req.Thinking.Effort != "" {
			cfg.ThinkingConfig.ThinkingLevel = string(req.Thinking.Effort)
		}
		hasConfig = true
	}
	if hasConfig {
		out.GenerationConfig = cfg
	}

	// 2. Messages & System Instructions
	for i, msg := range req.Messages {
		switch v := msg.(type) {
		case *message.System:
			text := v.Content.Text()
			if out.SystemInstruction == nil {
				out.SystemInstruction = &Content{
					Parts: []Part{{Text: text}},
				}
			} else {
				out.SystemInstruction.Parts[0].Text += "\n" + text
			}

		case *message.User:
			parts, err := toUserParts(v.GetContent())
			if err != nil {
				return out, fmt.Errorf("user message at index %d: %w", i, err)
			}
			out.Contents = append(out.Contents, Content{
				Role:  "user",
				Parts: parts,
			})

		case *message.Assistant:
			parts, err := toAssistantParts(v.GetContent())
			if err != nil {
				return out, fmt.Errorf("assistant message at index %d: %w", i, err)
			}
			out.Contents = append(out.Contents, Content{
				Role:  "model",
				Parts: parts,
			})

		case *message.Tool:
			part, err := toFunctionResponsePart(v)
			if err != nil {
				return out, fmt.Errorf("tool message at index %d: %w", i, err)
			}
			out.Contents = append(out.Contents, Content{
				Role:  "user",
				Parts: []Part{part},
			})

		default:
			return out, fmt.Errorf("unsupported message type at index %d: %T", i, msg)
		}
	}

	// 3. Tools
	if len(req.Tools) > 0 {
		t, err := toTools(req.Tools)
		if err != nil {
			return out, fmt.Errorf("convert tools: %w", err)
		}
		out.Tools = []Tool{t}
	}

	return out, nil
}

func toUserParts(content message.Content) ([]Part, error) {
	var parts []Part
	for _, block := range content {
		switch v := block.(type) {
		case *message.TextBlock:
			parts = append(parts, Part{Text: v.Text})
		case *message.ImageBlock:
			if len(v.Data) > 0 {
				parts = append(parts, Part{
					InlineData: &Blob{
						Data:     base64.StdEncoding.EncodeToString(v.Data),
						MIMEType: v.MIMEType,
					},
				})
			} else if v.URL != "" {
				parts = append(parts, Part{
					FileData: &FileData{
						FileURI:  v.URL,
						MIMEType: v.MIMEType,
					},
				})
			}
		case *message.AudioBlock:
			if len(v.Data) > 0 {
				parts = append(parts, Part{
					InlineData: &Blob{
						Data:     base64.StdEncoding.EncodeToString(v.Data),
						MIMEType: v.MIMEType,
					},
				})
			} else if v.URL != "" {
				parts = append(parts, Part{
					FileData: &FileData{
						FileURI:  v.URL,
						MIMEType: v.MIMEType,
					},
				})
			}
		case *message.VideoBlock:
			if len(v.Data) > 0 {
				parts = append(parts, Part{
					InlineData: &Blob{
						Data:     base64.StdEncoding.EncodeToString(v.Data),
						MIMEType: v.MIMEType,
					},
				})
			} else if v.URL != "" {
				parts = append(parts, Part{
					FileData: &FileData{
						FileURI:  v.URL,
						MIMEType: v.MIMEType,
					},
				})
			}
		case *message.DocumentBlock:
			if len(v.Data) > 0 {
				parts = append(parts, Part{
					InlineData: &Blob{
						Data:     base64.StdEncoding.EncodeToString(v.Data),
						MIMEType: v.MIMEType,
					},
				})
			} else if v.URL != "" {
				parts = append(parts, Part{
					FileData: &FileData{
						FileURI:  v.URL,
						MIMEType: v.MIMEType,
					},
				})
			}
		case *message.ThinkingBlock:
			// Do not send thinking blocks back in user content
		default:
			return nil, fmt.Errorf("unsupported block type in user content: %T", block)
		}
	}
	return parts, nil
}

func toAssistantParts(content message.Content) ([]Part, error) {
	var parts []Part
	for _, block := range content {
		switch v := block.(type) {
		case *message.TextBlock:
			if v.Text != "" {
				p := Part{Text: v.Text}
				if sig, ok := v.Extras[ThoughtSignatureKey].(string); ok && sig != "" {
					p.ThoughtSignature = sig
				}
				parts = append(parts, p)
			}
		case *message.ToolCall:
			p := Part{
				FunctionCall: &FunctionCall{
					ID:   v.ID,
					Name: v.Name,
					Args: v.Args,
				},
			}
			if sig, ok := v.Extras[ThoughtSignatureKey].(string); ok && sig != "" {
				p.ThoughtSignature = sig
			}
			// Google requires thought_signature on every functionCall part in thinking models.
			// If missing from the tool call (e.g. from history or cross-model turns), supply DummyThoughtSignature.
			if p.ThoughtSignature == "" {
				p.ThoughtSignature = DummyThoughtSignature
			}
			parts = append(parts, p)
		case *message.ThinkingBlock:
			if v.Thinking != "" {
				p := Part{
					Text:    v.Thinking,
					Thought: true,
				}
				if sig, ok := v.Extras[ThoughtSignatureKey].(string); ok && sig != "" {
					p.ThoughtSignature = sig
				}
				parts = append(parts, p)
			}
		default:
			return nil, fmt.Errorf("unsupported block type in assistant content: %T", block)
		}
	}
	return parts, nil
}

func toFunctionResponsePart(msg *message.Tool) (Part, error) {
	response := make(map[string]any)

	if len(msg.Content) > 0 {
		text := msg.Content.Text()
		if msg.IsError {
			response["error"] = text
		} else {
			response["output"] = text
		}
	} else if msg.StructuredContent != nil {
		if m, ok := msg.StructuredContent.(map[string]any); ok {
			maps.Copy(response, m)
		} else {
			data, _ := json.Marshal(msg.StructuredContent)
			_ = json.Unmarshal(data, &response)
		}
	}

	if msg.IsError && response["error"] == nil {
		if text := msg.Content.Text(); text != "" {
			response["error"] = text
		} else {
			response["error"] = "unknown error"
		}
	}

	return Part{
		FunctionResponse: &FunctionResponse{
			ID:       msg.ToolCallID,
			Name:     msg.Name,
			Response: response,
		},
	}, nil
}

func toTools(defs []tool.Definition) (Tool, error) {
	decls := make([]FunctionDeclaration, 0, len(defs))
	for _, d := range defs {
		var params any
		if d.InputSchema != nil {
			sanitized, err := sanitizeSchema(d.InputSchema)
			if err != nil {
				return Tool{}, fmt.Errorf("tool %q: sanitize input schema: %w", d.Name, err)
			}
			params = sanitized
		}
		decls = append(decls, FunctionDeclaration{
			Name:        d.Name,
			Description: d.Description,
			Parameters:  params,
		})
	}
	return Tool{FunctionDeclarations: decls}, nil
}

// toAssistantChunk converts a stream chunk response into an AssistantChunk.
func toAssistantChunk(resp *GenerateContentResponse, model string) (message.AssistantChunk, error) {
	chunk := message.AssistantChunk{
		Model: model,
	}

	for _, cand := range resp.Candidates {
		if cand.FinishReason != "" {
			chunk.Done = true
			chunk.DoneReason = cand.FinishReason
		}

		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				switch {
				case part.Thought:
					tb := &message.ThinkingBlock{
						Thinking: part.Text,
					}
					if part.ThoughtSignature != "" {
						tb.Extras = map[string]any{
							ThoughtSignatureKey: part.ThoughtSignature,
						}
					}
					chunk.Content = append(chunk.Content, tb)
				case part.Text != "":
					txt := &message.TextBlock{
						Text: part.Text,
					}
					if part.ThoughtSignature != "" {
						txt.Extras = map[string]any{
							ThoughtSignatureKey: part.ThoughtSignature,
						}
					}
					chunk.Content = append(chunk.Content, txt)
				case part.FunctionCall != nil:
					tc := &message.ToolCall{
						ID:   part.FunctionCall.ID,
						Name: part.FunctionCall.Name,
						Args: part.FunctionCall.Args,
					}
					if part.ThoughtSignature != "" {
						tc.Extras = map[string]any{
							ThoughtSignatureKey: part.ThoughtSignature,
						}
					}
					chunk.Content = append(chunk.Content, tc)
				}
			}
		}
	}

	if resp.UsageMetadata != nil {
		chunk.Metrics = &message.TokenMetrics{
			TotalTokens: resp.UsageMetadata.TotalTokenCount,
			Tokens: message.TokenDetails{
				Input:     resp.UsageMetadata.PromptTokenCount,
				Output:    resp.UsageMetadata.CandidatesTokenCount,
				CacheRead: resp.UsageMetadata.CachedContentTokenCount,
				Reasoning: resp.UsageMetadata.ThoughtsTokenCount,
			},
		}
	}

	return chunk, nil
}

// ToCountTokensRequest converts a message.MessageList into a CountTokensRequest.
func ToCountTokensRequest(messages message.MessageList) (CountTokensRequest, error) {
	var contents []Content
	for i, msg := range messages {
		switch v := msg.(type) {
		case *message.System:
			text := v.Content.Text()
			if text != "" {
				contents = append(contents, Content{
					Role:  "system",
					Parts: []Part{{Text: text}},
				})
			}
		case *message.User:
			parts, err := toUserParts(v.GetContent())
			if err != nil {
				return CountTokensRequest{}, fmt.Errorf("user message at index %d: %w", i, err)
			}
			contents = append(contents, Content{
				Role:  "user",
				Parts: parts,
			})
		case *message.Assistant:
			parts, err := toAssistantParts(v.GetContent())
			if err != nil {
				return CountTokensRequest{}, fmt.Errorf("assistant message at index %d: %w", i, err)
			}
			contents = append(contents, Content{
				Role:  "model",
				Parts: parts,
			})
		case *message.Tool:
			part, err := toFunctionResponsePart(v)
			if err != nil {
				return CountTokensRequest{}, fmt.Errorf("tool message at index %d: %w", i, err)
			}
			contents = append(contents, Content{
				Role:  "user",
				Parts: []Part{part},
			})
		default:
			return CountTokensRequest{}, fmt.Errorf("unsupported message type at index %d: %T", i, msg)
		}
	}
	return CountTokensRequest{Contents: contents}, nil
}


// sanitizeSchema converts a standard JSON Schema (such as from jsonschema.For or an MCP server)
// into Google's strict Schema proto format accepted by Cloud Code and Gemini endpoints.
// Specifically:
//   - Disallowed proto metadata fields ($schema, $defs, definitions, $id, etc.) are stripped.
//   - Multi-type arrays (e.g. ["null", "string"] or ["string", "null"]) are converted into a
//     single type string (e.g. "STRING") with nullable: true.
//   - anyOf branches with {"type": "null"} are collapsed and marked as nullable: true.
func sanitizeSchema(raw any) (map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var m any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	result, ok := sanitizeNode(m).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema must be an object")
	}
	return result, nil
}

func sanitizeNode(v any) any {
	switch node := v.(type) {
	case []any:
		out := make([]any, len(node))
		for i, item := range node {
			out[i] = sanitizeNode(item)
		}
		return out

	case map[string]any:
		out := make(map[string]any, len(node))

		// 1. Remove disallowed schema root/metadata keys that Google's proto rejects
		for k, val := range node {
			switch k {
			case "$schema", "$id", "$defs", "definitions", "$comment", "$anchor", "$dynamicAnchor", "$dynamicRef", "$vocabulary":
				continue
			default:
				out[k] = sanitizeNode(val)
			}
		}

		// 2. Handle `type` if it's a list (e.g. ["null", "string"]) or string
		if rawType, ok := out["type"]; ok {
			switch t := rawType.(type) {
			case []any:
				var nonNullTypes []string
				hasNull := false
				for _, item := range t {
					s := strings.ToLower(fmt.Sprint(item))
					if s == "null" {
						hasNull = true
					} else if s != "" {
						nonNullTypes = append(nonNullTypes, s)
					}
				}
				if hasNull {
					out["nullable"] = true
				}
				if len(nonNullTypes) > 0 {
					out["type"] = toGoogleType(nonNullTypes[0])
				} else {
					out["type"] = "STRING"
					out["nullable"] = true
				}
			case string:
				if strings.ToLower(t) == "null" {
					delete(out, "type")
					out["nullable"] = true
				} else {
					out["type"] = toGoogleType(t)
				}
			}
		}

		// 3. Handle `anyOf`: if any branch is a null branch, extract nullability
		if anyOf, ok := out["anyOf"].([]any); ok {
			var filtered []any
			for _, item := range anyOf {
				if itemMap, ok := item.(map[string]any); ok {
					t, _ := itemMap["type"].(string)
					isOnlyNull := strings.ToLower(t) == "null" || (itemMap["nullable"] == true && len(itemMap) == 1)
					if isOnlyNull {
						out["nullable"] = true
						continue
					}
				}
				filtered = append(filtered, item)
			}
			if len(filtered) == 1 {
				if singleMap, ok := filtered[0].(map[string]any); ok {
					delete(out, "anyOf")
					for k, v := range singleMap {
						if _, exists := out[k]; !exists {
							out[k] = v
						}
					}
				} else {
					out["anyOf"] = filtered
				}
			} else if len(filtered) > 0 {
				out["anyOf"] = filtered
			} else {
				delete(out, "anyOf")
			}
		}

		return out

	default:
		return v
	}
}

func toGoogleType(t string) string {
	switch strings.ToLower(t) {
	case "string":
		return "STRING"
	case "number":
		return "NUMBER"
	case "integer":
		return "INTEGER"
	case "boolean":
		return "BOOLEAN"
	case "array":
		return "ARRAY"
	case "object":
		return "OBJECT"
	default:
		return strings.ToUpper(t)
	}
}
