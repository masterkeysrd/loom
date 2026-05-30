package loomgenai

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/tool"
	"google.golang.org/genai"
)

const (
	ThoughtSignatureKey   = "thought_signature"
	DummyThoughtSignature = "ErQCCrECAdHtim8MtxgeMCRCiNiyoyImxtYAEDzz4NXOr/HSL3rA7rPPvHWZCm+T9VSDYh/mt9lESoH4wQh/ca1zDtWTN6XOL1+S3krYLQeqp47RV/b1eSq5jdZF28S4Lb7w4A3/EFdybc4SFb2/YhMm+CulYLmLA4Tr4VSu0eMWgxM3HVt6u0jECf5BbXzj0qjJ32tEQYJvKvV8H1tCHvB6J+RZhsDr+TcyOCaqxDoR4WKxXYxNRZb3hYTuCnBEDPhn1lROumVaghi9nEIgc17z002zLoyqIptlLfIVw70FXkCLsPUSL1SjPQYtGL8PVncVajeqGogRD/eZSVZ1Zr5tshxh3DQ+JAYNcrHaRHWC4Hg0H6oftYx+JdJD9B/81NYV9jyGxP7zHKFHOELl0IUP5GEXP9I="
)

// toGenerateContentArgs converts a generic [llm.Request] into the arguments
// expected by the Google GenAI GenerateContentStream call: a content slice and a
// config struct. System messages are extracted and placed in SystemInstruction.
func toGenerateContentArgs(req *llm.Request) ([]*genai.Content, *genai.GenerateContentConfig, error) {
	config := &genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: true,
		},
	}

	if req.MaxTokens > 0 {
		config.MaxOutputTokens = int32(req.MaxTokens)
	}

	if req.Temperature != nil {
		config.Temperature = genai.Ptr(*req.Temperature)
	}

	if req.TopP != nil {
		config.TopP = genai.Ptr(*req.TopP)
	}

	if req.TopK != nil {
		config.TopK = genai.Ptr(float32(*req.TopK))
	}

	if req.PresencePenalty != nil {
		config.PresencePenalty = genai.Ptr(*req.PresencePenalty)
	}

	if req.FrequencyPenalty != nil {
		config.FrequencyPenalty = genai.Ptr(*req.FrequencyPenalty)
	}

	if len(req.Stop) > 0 {
		config.StopSequences = req.Stop
	}

	if req.ResponseFormat == "json_object" {
		config.ResponseMIMEType = "application/json"
	}

	var contents []*genai.Content

	for i, msg := range req.Messages {
		switch v := msg.(type) {
		case *message.System:
			// GenAI accepts a single system instruction at the config level.
			// When multiple system messages are present, their text is concatenated.
			text := extractText(v.GetContent())
			if config.SystemInstruction == nil {
				config.SystemInstruction = &genai.Content{
					Parts: []*genai.Part{{Text: text}},
				}
			} else {
				existing := config.SystemInstruction.Parts[0].Text
				config.SystemInstruction.Parts[0].Text = existing + "\n" + text
			}

		case *message.User:
			parts, err := toUserParts(v.GetContent())
			if err != nil {
				return nil, nil, fmt.Errorf("user message at index %d: %w", i, err)
			}
			contents = append(contents, &genai.Content{
				Role:  "user",
				Parts: parts,
			})

		case *message.Assistant:
			parts, err := toModelParts(v.GetContent())
			if err != nil {
				return nil, nil, fmt.Errorf("assistant message at index %d: %w", i, err)
			}
			contents = append(contents, &genai.Content{
				Role:  "model",
				Parts: parts,
			})

		case *message.Tool:
			part, err := toFunctionResponsePart(v)
			if err != nil {
				return nil, nil, fmt.Errorf("tool message at index %d: %w", i, err)
			}
			// Tool results must be sent as user-role content with a FunctionResponse part.
			contents = append(contents, &genai.Content{
				Role:  "user",
				Parts: []*genai.Part{part},
			})

		default:
			return nil, nil, fmt.Errorf("unsupported message type at index %d: %T", i, msg)
		}
	}

	if len(req.Tools) > 0 {
		t, err := toGenaiTools(req.Tools)
		if err != nil {
			return nil, nil, fmt.Errorf("convert tools: %w", err)
		}
		config.Tools = []*genai.Tool{t}
	}

	return contents, config, nil
}

// toUserParts converts content blocks from a user message into GenAI Part values.
func toUserParts(content message.Content) ([]*genai.Part, error) {
	var parts []*genai.Part
	for _, block := range content {
		switch v := block.(type) {
		case *message.TextBlock:
			parts = append(parts, &genai.Part{Text: v.Text})
		case *message.ThinkingBlock:
			// Thinking blocks are not sent to GenAI.
		default:
			return nil, fmt.Errorf("unsupported content block type in user message: %T", block)
		}
	}
	return parts, nil
}

// toModelParts converts content blocks from an assistant message into GenAI
// Part values, mapping ToolCall blocks to FunctionCall parts.
func toModelParts(content message.Content) ([]*genai.Part, error) {
	var parts []*genai.Part
	var firstFCSeen bool
	for _, block := range content {
		switch v := block.(type) {
		case *message.TextBlock:
			if v.Text != "" {
				part := &genai.Part{Text: v.Text}

				if sig, ok := v.Extras[ThoughtSignatureKey].(string); ok {
					decodedSig, err := decodeThoughtSignature(sig)
					if err != nil {
						return nil, fmt.Errorf("decode thought signature for text block: %w", err)
					}
					part.ThoughtSignature = decodedSig
				}

				parts = append(parts, part)
			}
		case *message.ThinkingBlock:
			part := &genai.Part{
				Text:    v.Thinking,
				Thought: true,
			}

			if sig, ok := v.Extras[ThoughtSignatureKey].(string); ok {
				decodedSig, err := decodeThoughtSignature(sig)
				if err != nil {
					return nil, fmt.Errorf("decode thought signature for thinking block: %w", err)
				}
				part.ThoughtSignature = decodedSig
			}

			parts = append(parts, part)
		case *message.ToolCall:
			part := &genai.Part{
				FunctionCall: &genai.FunctionCall{
					ID:   v.ID,
					Name: v.Name,
					Args: v.Args,
				},
			}

			if sig, ok := v.Extras[ThoughtSignatureKey].(string); ok {
				decodedSig, err := decodeThoughtSignature(sig)
				if err != nil {
					return nil, fmt.Errorf("decode thought signature for tool call %q: %w", v.ID, err)
				}
				part.ThoughtSignature = decodedSig
			}

			if !firstFCSeen && len(part.ThoughtSignature) == 0 {
				decodedDummy, err := decodeThoughtSignature(DummyThoughtSignature)
				if err != nil {
					return nil, fmt.Errorf("decode dummy thought signature: %w", err)
				}
				part.ThoughtSignature = decodedDummy
			}
			firstFCSeen = true

			parts = append(parts, part)
		default:
			return nil, fmt.Errorf("unsupported content block type in assistant message: %T", block)
		}
	}
	return parts, nil
}

// toFunctionResponsePart converts a tool result message into a GenAI
// FunctionResponse Part.
func toFunctionResponsePart(msg *message.Tool) (*genai.Part, error) {
	text := extractText(msg.GetContent())
	return &genai.Part{
		FunctionResponse: &genai.FunctionResponse{
			ID:   msg.ToolCallID,
			Name: msg.Name,
			Response: map[string]any{
				"result": text,
			},
		},
	}, nil
}

// extractText returns the concatenated text of all TextBlock entries in content.
func extractText(content message.Content) string {
	var sb strings.Builder
	for _, block := range content {
		if t, ok := block.(*message.TextBlock); ok {
			sb.WriteString(t.Text)
		}
	}
	return sb.String()
}

// toGenaiTools converts a slice of [tool.Definition] values into a single
// [genai.Tool] containing one FunctionDeclaration per tool.
func toGenaiTools(defs []tool.Definition) (*genai.Tool, error) {
	decls := make([]*genai.FunctionDeclaration, 0, len(defs))
	for _, d := range defs {
		schema, err := toGenaiSchema(d.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %q: convert schema: %w", d.Name, err)
		}
		decls = append(decls, &genai.FunctionDeclaration{
			Name:        d.Name,
			Description: d.Description,
			Parameters:  schema,
		})
	}
	return &genai.Tool{FunctionDeclarations: decls}, nil
}

func toGenaiSchema(schema *jsonschema.Schema) (*genai.Schema, error) {
	if schema == nil {
		return nil, nil
	}

	return &genai.Schema{
		Type:        genai.Type(schema.Type),
		Properties:  toGenaiSchemaMap(schema.Properties),
		Required:    schema.Required,
		Description: schema.Description,
	}, nil
}

func toGenaiSchemaMap(props map[string]*jsonschema.Schema) map[string]*genai.Schema {
	if props == nil {
		return nil
	}

	result := make(map[string]*genai.Schema, len(props))
	for k, v := range props {
		schema, err := toGenaiSchema(v)
		if err != nil {
			return nil
		}
		result[k] = schema
	}
	return result
}

// toAssistantChunk converts a [genai.GenerateContentResponse] into an
// [message.AssistantChunk], mapping text and function call parts and populating
// token metrics when usage metadata is available.
func toAssistantChunk(resp *genai.GenerateContentResponse) (message.AssistantChunk, error) {
	var content message.Content

	finishReasons := []string{}
	for _, candidate := range resp.Candidates {
		if candidate.FinishReason != "" {
			finishReasons = append(finishReasons, string(candidate.FinishReason))
		}
		if candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			switch {
			case part.Text != "":
				if part.Thought {
					tb := &message.ThinkingBlock{Thinking: part.Text}
					if len(part.ThoughtSignature) > 0 {
						tb.Extras = map[string]any{
							ThoughtSignatureKey: encodeThoughtSignature(part.ThoughtSignature),
						}
					}
					content = append(content, tb)
				} else {
					txt := &message.TextBlock{Text: part.Text}
					if len(part.ThoughtSignature) > 0 {
						txt.Extras = map[string]any{
							ThoughtSignatureKey: encodeThoughtSignature(part.ThoughtSignature),
						}
					}
					content = append(content, txt)
				}
			case part.FunctionCall != nil:
				tc := &message.ToolCall{
					ID:   part.FunctionCall.ID,
					Name: part.FunctionCall.Name,
					Args: part.FunctionCall.Args,
				}

				if len(part.ThoughtSignature) > 0 {
					tc.Extras = map[string]any{
						ThoughtSignatureKey: encodeThoughtSignature(part.ThoughtSignature),
					}
				}

				content = append(content, tc)
			}
		}
	}

	chunk := message.AssistantChunk{
		Content:    content,
		Done:       len(finishReasons) > 0,
		DoneReason: strings.Join(finishReasons, ","),
	}

	if resp.UsageMetadata != nil {
		chunk.Metrics = &message.TokenMetrics{
			PromptTokens:     int(resp.UsageMetadata.PromptTokenCount),
			CompletionTokens: int(resp.UsageMetadata.CandidatesTokenCount),
			TotalTokens:      int(resp.UsageMetadata.TotalTokenCount),
		}
	}

	return chunk, nil
}

// Convert the thought signature into a base64-encoded string that can be
// safely transmitted in the extras map of a ToolCall.
func encodeThoughtSignature(signature []byte) string {
	return base64.StdEncoding.EncodeToString(signature)
}

func decodeThoughtSignature(encoded string) ([]byte, error) {
	decodedBytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode thought signature: %w", err)
	}
	return decodedBytes, nil
}
