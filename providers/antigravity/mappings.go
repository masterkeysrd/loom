package loomantigravity

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/providers/antigravity/sdk"
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
// sdk.GenerateContentRequest expected by Cloud Code / Antigravity.
func toGenerateContentRequest(req *llm.Request) (sdk.GenerateContentRequest, error) {
	out := sdk.GenerateContentRequest{}
	if req == nil {
		return out, fmt.Errorf("request cannot be nil")
	}
	if sess, ok := extractSession(req); ok && sess.ID != "" {
		out.SessionID = sess.ID
	}

	for _, d := range req.Tools {
		if d.DeferLoading {
			return out, fmt.Errorf("antigravity provider does not support deferred tool loading (tool %q)", d.Name)
		}
	}
	if slices.ContainsFunc(req.Messages, hasToolReferenceBlock) {
		return out, fmt.Errorf("antigravity provider does not support tool reference blocks")
	}
	if slices.ContainsFunc(req.Messages, hasToolDefinitionBlock) {
		return out, fmt.Errorf("antigravity provider does not support inline tool definition blocks")
	}

	// 1. Generation Config
	cfg := &sdk.GenerationConfig{}
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
		cfg.ThinkingConfig = &sdk.ThinkingConfig{
			IncludeThoughts: true,
		}
		if req.Thinking.Budget > 0 {
			b := int32(req.Thinking.Budget)
			cfg.ThinkingConfig.ThinkingBudget = &b
		}
		if req.Thinking.Effort != "" {
			cfg.ThinkingConfig.ThinkingLevel = strings.ToUpper(req.Thinking.Effort)
		}
		hasConfig = true
	}
	if hasConfig {
		out.GenerationConfig = cfg
	}

	// 2. Messages & System Instructions
	activeToolTurnIdx := -1
	for i, msg := range req.Messages {
		switch v := msg.(type) {
		case *message.System:
			activeToolTurnIdx = -1
			if v == nil {
				return out, fmt.Errorf("system message at index %d: nil message", i)
			}
			text, err := extractPlainText(v.GetContent(), req.Model, "system")
			if err != nil {
				return out, fmt.Errorf("system message at index %d: %w", i, err)
			}
			if out.SystemInstruction == nil {
				out.SystemInstruction = &sdk.Content{
					Parts: []sdk.Part{{Text: text}},
				}
			} else {
				out.SystemInstruction.Parts[0].Text += "\n" + text
			}

		case *message.User:
			activeToolTurnIdx = -1
			if v == nil {
				return out, fmt.Errorf("user message at index %d: nil message", i)
			}
			parts, err := toUserParts(v.GetContent(), req.Model)
			if err != nil {
				return out, fmt.Errorf("user message at index %d: %w", i, err)
			}
			out.Contents = append(out.Contents, sdk.Content{
				Role:  "user",
				Parts: parts,
			})

		case *message.Assistant:
			activeToolTurnIdx = -1
			if v == nil {
				return out, fmt.Errorf("assistant message at index %d: nil message", i)
			}
			parts, err := toAssistantPartsForModel(v.GetContent(), req.Model)
			if err != nil {
				return out, fmt.Errorf("assistant message at index %d: %w", i, err)
			}
			out.Contents = append(out.Contents, sdk.Content{
				Role:  "model",
				Parts: parts,
			})

		case *message.Tool:
			if v == nil {
				return out, fmt.Errorf("tool message at index %d: nil message", i)
			}
			part, err := toFunctionResponsePart(v, req.Model)
			if err != nil {
				return out, fmt.Errorf("tool message at index %d: %w", i, err)
			}
			// Tool results answering the preceding assistant turn are grouped into a single user turn.
			if activeToolTurnIdx != -1 {
				out.Contents[activeToolTurnIdx].Parts = append(out.Contents[activeToolTurnIdx].Parts, part)
			} else {
				out.Contents = append(out.Contents, sdk.Content{
					Role:  "user",
					Parts: []sdk.Part{part},
				})
				activeToolTurnIdx = len(out.Contents) - 1
			}

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
		out.Tools = []sdk.Tool{t}
	}

	return out, nil
}

func mediaBlockModality(block message.Block) (mod profile.Modality, label string, ok bool) {
	switch block.(type) {
	case *message.ImageBlock:
		return profile.ModalityImage, "image", true
	case *message.AudioBlock:
		return profile.ModalityAudio, "audio", true
	case *message.VideoBlock:
		return profile.ModalityVideo, "video", true
	case *message.DocumentBlock:
		return profile.ModalityPDF, "document", true
	default:
		return "", "", false
	}
}

func extractPlainText(content message.Content, model, role string) (string, error) {
	var sb strings.Builder
	first := true
	for _, block := range content {
		switch v := block.(type) {
		case *message.TextBlock:
			if v == nil {
				continue
			}
			if !first {
				sb.WriteString("\n")
			}
			sb.WriteString(v.Text)
			first = false
		default:
			if mod, label, ok := mediaBlockModality(block); ok {
				return "", profile.NewUnsupportedModalityError("antigravity", model, mod, fmt.Sprintf("%s messages do not support %s content", role, label))
			}
			return "", fmt.Errorf("unsupported block type in %s content: %T", role, block)
		}
	}
	return sb.String(), nil
}

func mediaPart(model string, modality profile.Modality, data []byte, url, mimeType string) (sdk.Part, error) {
	if len(data) > 0 {
		return sdk.Part{
			InlineData: &sdk.Blob{
				Data:     base64.StdEncoding.EncodeToString(data),
				MIMEType: mimeType,
			},
		}, nil
	}
	if url != "" {
		return sdk.Part{
			FileData: &sdk.FileData{
				FileURI:  url,
				MIMEType: mimeType,
			},
		}, nil
	}
	return sdk.Part{}, profile.NewUnsupportedModalityError("antigravity", model, modality, "media block has neither inline data nor URL")
}

func toUserParts(content message.Content, model string) ([]sdk.Part, error) {
	var parts []sdk.Part
	for _, block := range content {
		switch v := block.(type) {
		case *message.TextBlock:
			if v == nil {
				continue
			}
			parts = append(parts, sdk.Part{Text: v.Text})
		case *message.ImageBlock:
			if v == nil {
				return nil, profile.NewUnsupportedModalityError("antigravity", model, profile.ModalityImage, "nil image block")
			}
			part, err := mediaPart(model, profile.ModalityImage, v.Data, v.URL, v.MIMEType)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case *message.AudioBlock:
			if v == nil {
				return nil, profile.NewUnsupportedModalityError("antigravity", model, profile.ModalityAudio, "nil audio block")
			}
			part, err := mediaPart(model, profile.ModalityAudio, v.Data, v.URL, v.MIMEType)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case *message.VideoBlock:
			if v == nil {
				return nil, profile.NewUnsupportedModalityError("antigravity", model, profile.ModalityVideo, "nil video block")
			}
			part, err := mediaPart(model, profile.ModalityVideo, v.Data, v.URL, v.MIMEType)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case *message.DocumentBlock:
			if v == nil {
				return nil, profile.NewUnsupportedModalityError("antigravity", model, profile.ModalityPDF, "nil document block")
			}
			part, err := mediaPart(model, profile.ModalityPDF, v.Data, v.URL, v.MIMEType)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case *message.ThinkingBlock:
			// Do not send thinking blocks back in user content
		default:
			return nil, fmt.Errorf("unsupported block type in user content: %T", block)
		}
	}
	return parts, nil
}

func toAssistantParts(content message.Content) ([]sdk.Part, error) {
	return toAssistantPartsForModel(content, "")
}

func toAssistantPartsForModel(content message.Content, model string) ([]sdk.Part, error) {
	var parts []sdk.Part
	var firstFCSeen bool
	var pendingSig string

	attachOrHoldSig := func(sig string) {
		if sig == "" {
			return
		}
		if n := len(parts); n > 0 && parts[n-1].FunctionCall == nil && parts[n-1].ThoughtSignature == "" {
			parts[n-1].ThoughtSignature = sig
			return
		}
		pendingSig = sig
	}

	for _, block := range content {
		switch v := block.(type) {
		case *message.TextBlock:
			if v == nil {
				continue
			}
			sig, _ := v.Extras[ThoughtSignatureKey].(string)
			if v.Text == "" {
				attachOrHoldSig(sig)
				continue
			}
			p := sdk.Part{Text: v.Text}
			if sig != "" {
				p.ThoughtSignature = sig
			} else if pendingSig != "" {
				p.ThoughtSignature = pendingSig
				pendingSig = ""
			}
			parts = append(parts, p)

		case *message.ToolCall:
			if v == nil {
				return nil, fmt.Errorf("nil tool call block in assistant content")
			}
			p := sdk.Part{
				FunctionCall: &sdk.FunctionCall{
					ID:   v.ID,
					Name: v.Name,
					Args: v.Args,
				},
			}
			if sig, ok := v.Extras[ThoughtSignatureKey].(string); ok && sig != "" {
				p.ThoughtSignature = sig
			}
			if p.ThoughtSignature == "" && pendingSig != "" {
				p.ThoughtSignature = pendingSig
				pendingSig = ""
			}
			// For parallel function calls, Gemini only places thoughtSignature on the first
			// functionCall part; calls #2..N intentionally carry no signature.
			if !firstFCSeen && p.ThoughtSignature == "" {
				p.ThoughtSignature = DummyThoughtSignature
			}
			firstFCSeen = true
			parts = append(parts, p)

		case *message.ThinkingBlock:
			if v == nil {
				continue
			}
			sig, _ := v.Extras[ThoughtSignatureKey].(string)
			if v.Thinking == "" {
				attachOrHoldSig(sig)
				continue
			}
			p := sdk.Part{
				Text:    v.Thinking,
				Thought: true,
			}
			if sig != "" {
				p.ThoughtSignature = sig
			}
			parts = append(parts, p)

		default:
			if mod, label, ok := mediaBlockModality(block); ok {
				return nil, profile.NewUnsupportedModalityError("antigravity", model, mod, fmt.Sprintf("assistant messages do not support %s content", label))
			}
			return nil, fmt.Errorf("unsupported block type in assistant content: %T", block)
		}
	}
	return parts, nil
}

func toFunctionResponsePart(msg *message.Tool, model string) (sdk.Part, error) {
	response := make(map[string]any)

	if len(msg.Content) > 0 {
		text, err := extractPlainText(msg.Content, model, "tool")
		if err != nil {
			return sdk.Part{}, err
		}
		if msg.IsError {
			response["error"] = text
		} else {
			response["output"] = text
		}
	} else if msg.StructuredContent != nil {
		data, _ := msg.CanonicalStructuredContent()
		_ = json.Unmarshal(data, &response)
	}

	if msg.IsError && response["error"] == nil {
		response["error"] = "unknown error"
	}

	return sdk.Part{
		FunctionResponse: &sdk.FunctionResponse{
			ID:       msg.ToolCallID,
			Name:     msg.Name,
			Response: response,
		},
	}, nil
}

func toTools(defs []tool.Definition) (sdk.Tool, error) {
	decls := make([]sdk.FunctionDeclaration, 0, len(defs))
	for _, d := range defs {
		if d.DeferLoading {
			return sdk.Tool{}, fmt.Errorf("antigravity provider does not support deferred tool loading (tool %q)", d.Name)
		}
		var params any
		if d.InputSchema != nil {
			sanitized, err := sanitizeSchema(d.InputSchema)
			if err != nil {
				return sdk.Tool{}, fmt.Errorf("tool %q: sanitize input schema: %w", d.Name, err)
			}
			params = sanitized
		}
		decls = append(decls, sdk.FunctionDeclaration{
			Name:        d.Name,
			Description: d.Description,
			Parameters:  params,
		})
	}
	return sdk.Tool{FunctionDeclarations: decls}, nil
}

// toAssistantChunk converts a stream chunk response into an AssistantChunk.
func toAssistantChunk(resp *sdk.GenerateContentResponse, model string) (message.AssistantChunk, error) {
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
				case part.Text != "" || part.ThoughtSignature != "":
					txt := &message.TextBlock{
						Text: part.Text,
					}
					if part.ThoughtSignature != "" {
						txt.Extras = map[string]any{
							ThoughtSignatureKey: part.ThoughtSignature,
						}
					}
					chunk.Content = append(chunk.Content, txt)
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
func ToCountTokensRequest(messages message.MessageList) (sdk.CountTokensRequest, error) {
	var contents []sdk.Content
	for i, msg := range messages {
		switch v := msg.(type) {
		case *message.System:
			if v == nil {
				return sdk.CountTokensRequest{}, fmt.Errorf("system message at index %d: nil message", i)
			}
			text, err := extractPlainText(v.GetContent(), "", "system")
			if err != nil {
				return sdk.CountTokensRequest{}, fmt.Errorf("system message at index %d: %w", i, err)
			}
			if text != "" {
				contents = append(contents, sdk.Content{
					Role:  "system",
					Parts: []sdk.Part{{Text: text}},
				})
			}
		case *message.User:
			if v == nil {
				return sdk.CountTokensRequest{}, fmt.Errorf("user message at index %d: nil message", i)
			}
			parts, err := toUserParts(v.GetContent(), "")
			if err != nil {
				return sdk.CountTokensRequest{}, fmt.Errorf("user message at index %d: %w", i, err)
			}
			contents = append(contents, sdk.Content{
				Role:  "user",
				Parts: parts,
			})
		case *message.Assistant:
			if v == nil {
				return sdk.CountTokensRequest{}, fmt.Errorf("assistant message at index %d: nil message", i)
			}
			parts, err := toAssistantPartsForModel(v.GetContent(), "")
			if err != nil {
				return sdk.CountTokensRequest{}, fmt.Errorf("assistant message at index %d: %w", i, err)
			}
			contents = append(contents, sdk.Content{
				Role:  "model",
				Parts: parts,
			})
		case *message.Tool:
			if v == nil {
				return sdk.CountTokensRequest{}, fmt.Errorf("tool message at index %d: nil message", i)
			}
			part, err := toFunctionResponsePart(v, "")
			if err != nil {
				return sdk.CountTokensRequest{}, fmt.Errorf("tool message at index %d: %w", i, err)
			}
			contents = append(contents, sdk.Content{
				Role:  "user",
				Parts: []sdk.Part{part},
			})
		default:
			return sdk.CountTokensRequest{}, fmt.Errorf("unsupported message type at index %d: %T", i, msg)
		}
	}
	return sdk.CountTokensRequest{Contents: contents}, nil
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

func hasToolReferenceBlock(msg message.Message) bool {
	if msg == nil {
		return false
	}
	return slices.ContainsFunc(msg.GetContent(), blockHasToolReference)
}

func blockHasToolReference(b message.Block) bool {
	switch v := b.(type) {
	case *message.ToolReferenceBlock:
		return true
	case *message.ServerToolResult:
		if slices.ContainsFunc(v.Content, blockHasToolReference) {
			return true
		}
	}
	return false
}

func hasToolDefinitionBlock(msg message.Message) bool {
	if msg == nil {
		return false
	}
	return slices.ContainsFunc(msg.GetContent(), blockHasToolDefinition)
}

func blockHasToolDefinition(b message.Block) bool {
	switch v := b.(type) {
	case *message.ToolDefinitionBlock:
		return true
	case *message.ServerToolResult:
		if slices.ContainsFunc(v.Content, blockHasToolDefinition) {
			return true
		}
	}
	return false
}
