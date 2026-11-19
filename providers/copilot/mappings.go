package loomcopilot

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/tool"
)

type sessionMeta struct {
	SessionID    string
	TurnToken    string
	PrefixDigest string
	SyncToken    string
	EventID      string
}

// supportedImageMIMETypes lists the image formats the Copilot SDK forwards to
// models (PNG, JPEG, WebP, GIF per the copilot-sdk Go README). The runtime
// silently drops other formats, so loom rejects them up front instead.
var supportedImageMIMETypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
	"image/gif":  true,
}

// resolveImageMIME resolves the effective MIME type for inline image bytes and
// rejects formats the Copilot runtime will not forward to the model.
func resolveImageMIME(model, declared string, data []byte) (string, error) {
	mimeType := strings.ToLower(strings.TrimSpace(declared))
	if mimeType == "" {
		mimeType = strings.ToLower(http.DetectContentType(data))
	}
	if mimeType == "image/jpg" {
		mimeType = "image/jpeg"
	}
	if !supportedImageMIMETypes[mimeType] {
		return "", profile.NewUnsupportedModalityError("copilot", model, profile.ModalityImage, fmt.Sprintf("copilot only supports PNG, JPEG, WebP, and GIF images; got %s", mimeType))
	}
	return mimeType, nil
}

// extractSessionMeta reads Copilot session tracking keys from an Assistant
// message's Extensions, Metadata, or block Extras.
func extractSessionMeta(msg message.Message) (sessionMeta, bool) {
	if msg == nil {
		return sessionMeta{}, false
	}
	var out sessionMeta
	if exts := msg.GetExtensions(); exts != nil {
		if ext, ok := exts[SyncCheckpointExtensionID]; ok && ext != nil {
			if v, ok := ext.(*SyncCheckpointExtension); ok && v != nil {
				out.SessionID = v.SessionID
				out.TurnToken = v.TurnToken
				out.SyncToken = v.SyncToken
			}
		}
	}
	if meta := msg.GetMetadata(); meta != nil {
		if out.SessionID == "" {
			if v, ok := meta[SessionIDKey].(string); ok {
				out.SessionID = v
			}
		}
		if out.TurnToken == "" {
			if v, ok := meta[TurnTokenKey].(string); ok {
				out.TurnToken = v
			}
		}
		if out.PrefixDigest == "" {
			if v, ok := meta[PrefixDigestKey].(string); ok {
				out.PrefixDigest = v
			}
		}
		if out.EventID == "" {
			if v, ok := meta[EventIDKey].(string); ok {
				out.EventID = v
			}
		}
	}
	if out.SessionID != "" && out.TurnToken != "" {
		return out, true
	}

	for _, block := range msg.GetContent() {
		var extras map[string]any
		switch b := block.(type) {
		case *message.TextBlock:
			extras = b.Extras
		case *message.ThinkingBlock:
			extras = b.Extras
		case *message.ToolCall:
			extras = b.Extras
		}
		if extras == nil {
			continue
		}
		if out.SessionID == "" {
			if v, ok := extras[SessionIDKey].(string); ok {
				out.SessionID = v
			}
		}
		if out.TurnToken == "" {
			if v, ok := extras[TurnTokenKey].(string); ok {
				out.TurnToken = v
			}
		}
		if out.PrefixDigest == "" {
			if v, ok := extras[PrefixDigestKey].(string); ok {
				out.PrefixDigest = v
			}
		}
	}
	return out, out.SessionID != "" && out.TurnToken != ""
}

// findLastSessionCheckpoint scans convMessages backwards for the most recent
// Assistant message tagged with Copilot session metadata.
func findLastSessionCheckpoint(convMessages []message.Message) (sessionMeta, int, bool) {
	for i, msg := range slices.Backward(convMessages) {
		if msg.Role() != message.RoleAssistant {
			continue
		}
		if meta, ok := extractSessionMeta(msg); ok {
			return meta, i, true
		}
	}
	return sessionMeta{}, -1, false
}

// splitSystemAndConversation extracts leading System instructions while
// preserving any mid-conversation messages in order.
func splitSystemAndConversation(msgs []message.Message) (string, []message.Message) {
	sys, conv, _ := splitSystemAndConversationChecked("", msgs)
	return sys, conv
}

func splitSystemAndConversationChecked(model string, msgs []message.Message) (string, []message.Message, error) {
	var systemParts []string
	var conv []message.Message
	seenNonSystem := false

	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		if sys, ok := msg.(*message.System); ok && !seenNonSystem {
			if _, err := extractAttachmentsOrRejectMedia(model, message.RoleSystem, sys.Content, false); err != nil {
				return "", nil, err
			}
			if txt := strings.TrimSpace(sys.Content.Text()); txt != "" {
				systemParts = append(systemParts, txt)
			}
			continue
		}
		seenNonSystem = true
		conv = append(conv, msg)
	}
	return strings.Join(systemParts, "\n\n"), conv, nil
}

// computePrefixDigest hashes the canonical conversation prefix plus structural
// request configuration (model, system prompt, tools, schema, thinking, cwd,
// native tools) so any Loom-side compaction, edit, or config change invalidates
// stale live sessions cleanly.
func computePrefixDigest(
	req *llm.Request,
	systemPrompt string,
	prefixMsgs []message.Message,
	ext Session,
	allowedNativeTools []string,
	workingDir string,
) string {
	h := sha256.New()
	writeStr := func(s string) {
		_, _ = fmt.Fprintf(h, "%d:%s\n", len(s), s)
	}

	writeStr("model:" + req.Model)
	writeStr("sys:" + systemPrompt)
	writeStr("cwd:" + workingDir)

	if len(allowedNativeTools) > 0 {
		writeStr("native:" + strings.Join(allowedNativeTools, ","))
	}

	for _, t := range req.Tools {
		b, _ := json.Marshal(struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Schema      any    `json:"schema"`
		}{t.Name, t.Description, t.InputSchema})
		writeStr("tool:" + string(b))
	}

	if req.ResponseSchema != nil {
		b, _ := json.Marshal(req.ResponseSchema)
		writeStr("schema:" + string(b))
	} else if req.ResponseFormat != "" {
		writeStr("format:" + req.ResponseFormat)
	}

	if req.Thinking != nil {
		b, _ := json.Marshal(req.Thinking)
		writeStr("thinking:" + string(b))
	}
	if ext.ReasoningSummary != "" {
		writeStr("rsummary:" + string(ext.ReasoningSummary))
	}

	for _, m := range prefixMsgs {
		writeStr("role:" + string(m.Role()))
		switch msg := m.(type) {
		case *message.Tool:
			writeStr("tool_id:" + msg.ToolCallID)
			writeStr("tool_name:" + msg.Name)
			writeStr(fmt.Sprintf("is_err:%v", msg.IsError))
			if msg.StructuredContent != nil {
				if data, err := msg.CanonicalStructuredContent(); err == nil {
					writeStr("structured:" + string(data))
				}
			}
		}
		for _, block := range m.GetContent() {
			switch b := block.(type) {
			case *message.TextBlock:
				writeStr("text:" + b.Text)
			case *message.ThinkingBlock:
				writeStr("think:" + b.Thinking)
			case *message.ToolCall:
				argsBytes, _ := json.Marshal(b.Args)
				writeStr("tc:" + b.ID + ":" + b.Name + ":" + string(argsBytes))
			default:
				raw, _ := json.Marshal(b)
				writeStr("blk:" + string(b.Kind()) + ":" + string(raw))
			}
		}
	}

	return hex.EncodeToString(h.Sum(nil))[:24]
}

func computeTurnToken(sessionID, turnNonce, prefixDigest string) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s|%s|%s", sessionID, turnNonce, prefixDigest)
	return hex.EncodeToString(h.Sum(nil))[:24]
}

func extractAttachmentsOrRejectMedia(model string, role message.Role, content message.Content, allowInlineImage bool) ([]copilot.Attachment, error) {
	var attachments []copilot.Attachment
	for _, block := range content {
		if block == nil {
			continue
		}
		switch b := block.(type) {
		case *message.ImageBlock:
			if b == nil || (len(b.Data) == 0 && b.URL == "") {
				return nil, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityImage, fmt.Sprintf("empty image block in %s message", role))
			}
			if !allowInlineImage {
				return nil, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityImage, fmt.Sprintf("copilot does not support image blocks in %s messages", role))
			}
			if len(b.Data) == 0 {
				return nil, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityImage, "copilot requires inline image bytes; URL-only images are not supported")
			}
			mimeType, err := resolveImageMIME(model, b.MIMEType, b.Data)
			if err != nil {
				return nil, err
			}
			encoded := base64.StdEncoding.EncodeToString(b.Data)
			attachments = append(attachments, &copilot.AttachmentBlob{
				Data:     &encoded,
				MIMEType: mimeType,
			})
		case *message.AudioBlock:
			return nil, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityAudio, fmt.Sprintf("copilot does not support audio blocks in %s messages", role))
		case *message.VideoBlock:
			return nil, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityVideo, fmt.Sprintf("copilot does not support video blocks in %s messages", role))
		case *message.DocumentBlock:
			if b == nil || (len(b.Data) == 0 && b.URL == "") {
				return nil, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityPDF, fmt.Sprintf("empty document block in %s message", role))
			}
			if !allowInlineImage {
				return nil, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityPDF, fmt.Sprintf("copilot does not support document blocks in %s messages", role))
			}
			if len(b.Data) == 0 {
				return nil, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityPDF, "copilot requires inline document bytes; URL-only documents are not supported")
			}
			mimeType := strings.ToLower(strings.TrimSpace(b.MIMEType))
			if mimeType == "" {
				mimeType = strings.ToLower(http.DetectContentType(b.Data))
			}
			if mimeType != "application/pdf" {
				return nil, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityPDF, fmt.Sprintf("copilot document attachments must be application/pdf; got %s", mimeType))
			}
			encoded := base64.StdEncoding.EncodeToString(b.Data)
			attachments = append(attachments, &copilot.AttachmentBlob{
				Data:     &encoded,
				MIMEType: mimeType,
			})
		}
	}
	return attachments, nil
}

func formatUserDeltaPrompt(model string, msgs []message.Message) (string, []copilot.Attachment, error) {
	if len(msgs) == 1 {
		if u, ok := msgs[0].(*message.User); ok {
			attachments, err := extractAttachmentsOrRejectMedia(model, message.RoleUser, u.Content, true)
			if err != nil {
				return "", nil, err
			}
			return strings.TrimSpace(u.Content.Text()), attachments, nil
		}
	}
	return formatMessagesTranscript(model, msgs)
}

func formatMessagesTranscript(model string, msgs []message.Message) (string, []copilot.Attachment, error) {
	if len(msgs) == 1 {
		if u, ok := msgs[0].(*message.User); ok {
			attachments, err := extractAttachmentsOrRejectMedia(model, message.RoleUser, u.Content, true)
			if err != nil {
				return "", nil, err
			}
			return u.Content.Text(), attachments, nil
		}
	}

	var sb strings.Builder
	var attachments []copilot.Attachment
	for i, m := range msgs {
		if m == nil {
			continue
		}
		if i > 0 {
			sb.WriteString("\n\n")
		}
		switch msg := m.(type) {
		case *message.System:
			if _, err := extractAttachmentsOrRejectMedia(model, message.RoleSystem, msg.Content, false); err != nil {
				return "", nil, err
			}
			sb.WriteString("[System]\n")
			sb.WriteString(msg.Content.Text())
		case *message.User:
			isFinalMsg := i == len(msgs)-1
			atts, err := extractAttachmentsOrRejectMedia(model, message.RoleUser, msg.Content, isFinalMsg)
			if err != nil {
				return "", nil, err
			}
			attachments = append(attachments, atts...)
			if len(msgs) > 1 {
				sb.WriteString("[User]\n")
			}
			sb.WriteString(msg.Content.Text())
		case *message.Assistant:
			if _, err := extractAttachmentsOrRejectMedia(model, message.RoleAssistant, msg.Content, false); err != nil {
				return "", nil, err
			}
			sb.WriteString("[Assistant]\n")
			if txt := msg.Content.Text(); txt != "" {
				sb.WriteString(txt)
			}
			for _, tc := range msg.ToolCalls() {
				argsJSON, _ := json.Marshal(tc.Args)
				fmt.Fprintf(&sb, "\n[ToolCall name=%s id=%s args=%s]", tc.Name, tc.ID, string(argsJSON))
			}
		case *message.Tool:
			if _, err := extractAttachmentsOrRejectMedia(model, message.RoleTool, msg.Content, false); err != nil {
				return "", nil, err
			}
			status := "ok"
			if msg.IsError {
				status = "error"
			}
			body := msg.Content.Text()
			if body == "" && msg.StructuredContent != nil {
				if data, err := msg.CanonicalStructuredContent(); err == nil {
					body = string(data)
				}
			}
			fmt.Fprintf(&sb, "[ToolResult name=%s id=%s status=%s]\n%s",
				msg.Name, msg.ToolCallID, status, body)
		default:
			if _, err := extractAttachmentsOrRejectMedia(model, m.Role(), m.GetContent(), false); err != nil {
				return "", nil, err
			}
			fmt.Fprintf(&sb, "[%s]\n%s", m.Role(), m.GetContent().Text())
		}
	}
	return sb.String(), attachments, nil
}

func areAllToolResults(msgs []message.Message) ([]*message.Tool, bool) {
	if len(msgs) == 0 {
		return nil, false
	}
	out := make([]*message.Tool, 0, len(msgs))
	for _, m := range msgs {
		tm, ok := m.(*message.Tool)
		if !ok {
			return nil, false
		}
		out = append(out, tm)
	}
	return out, true
}

func resolveAllowedNativeTools(cfg Config, ext Session) []string {
	if len(ext.NativeTools) > 0 {
		return ext.NativeTools
	}
	return cfg.AllowedNativeTools
}

func buildToolFilterLists(loomTools []tool.Definition, allowedNative []string) (available []string, excluded []string) {
	ts := copilot.NewToolSet()
	hasAny := false
	for _, nt := range allowedNative {
		nt = strings.TrimSpace(nt)
		if nt != "" {
			ts.AddBuiltIn(nt)
			hasAny = true
		}
	}
	if len(loomTools) > 0 {
		ts.AddCustom("*")
		hasAny = true
	}
	if !hasAny {
		// Explicit non-nil empty slice blocks all built-in tools when no tools are bound.
		return []string{}, copilot.NewToolSet().AddBuiltIn("*").ToSlice()
	}
	if len(allowedNative) == 0 {
		return ts.ToSlice(), copilot.NewToolSet().AddBuiltIn("*").ToSlice()
	}
	return ts.ToSlice(), nil
}

func mapSchemaToMap(schema any) (map[string]any, error) {
	if schema == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}, nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	return out, nil
}

func buildCopilotTools(defs []tool.Definition, handler copilot.ToolHandler) ([]copilot.Tool, error) {
	if len(defs) == 0 {
		return nil, nil
	}
	out := make([]copilot.Tool, 0, len(defs))
	for _, t := range defs {
		params, err := mapSchemaToMap(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("copilot: marshal tool schema for %q: %w", t.Name, err)
		}
		out = append(out, copilot.Tool{
			Name:           t.Name,
			Description:    t.Description,
			Parameters:     params,
			SkipPermission: true,
			Handler:        handler,
		})
	}
	return out, nil
}

func buildMessageOptions(req *llm.Request, prompt string, attachments []copilot.Attachment) (copilot.MessageOptions, error) {
	opts := copilot.MessageOptions{
		Prompt:      prompt,
		Attachments: attachments,
	}
	if req == nil {
		return opts, nil
	}
	if req.ResponseSchema != nil {
		schemaMap, err := mapSchemaToMap(req.ResponseSchema)
		if err != nil {
			return opts, fmt.Errorf("copilot: marshal response schema: %w", err)
		}
		opts.ResponseSchema = schemaMap
	} else if req.ResponseFormat == "json_object" {
		opts.ResponseSchema = map[string]any{"type": "object"}
	}
	return opts, nil
}

func mapThinkingToReasoningEffort(t *llm.ThinkingConfig) string {
	if t == nil {
		return ""
	}
	if effort := strings.ToLower(strings.TrimSpace(t.Effort)); effort != "" {
		switch effort {
		case "low", "medium", "high", "xhigh", "max":
			return effort
		}
	}
	if t.Budget > 0 {
		switch {
		case t.Budget <= 4096:
			return "low"
		case t.Budget <= 16384:
			return "medium"
		default:
			return "high"
		}
	}
	if t.Enabled {
		return "medium"
	}
	return ""
}

func prepareToolResults(model string, toolMsgs []*message.Tool) ([]copilot.ToolResult, error) {
	out := make([]copilot.ToolResult, len(toolMsgs))
	for i, tm := range toolMsgs {
		res, err := toToolResult(tm, model)
		if err != nil {
			return nil, err
		}
		out[i] = res
	}
	return out, nil
}

func toToolResult(tm *message.Tool, model string) (copilot.ToolResult, error) {
	if tm == nil {
		return copilot.ToolResult{
			TextResultForLLM: "missing tool result from caller",
			ResultType:       "failure",
			Error:            "missing tool result from caller",
		}, nil
	}
	var sb strings.Builder
	var binaryResults []copilot.ToolBinaryResult
	for _, block := range tm.Content {
		if block == nil {
			continue
		}
		switch b := block.(type) {
		case *message.TextBlock:
			if b != nil && b.Text != "" {
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(b.Text)
			}
		case *message.ImageBlock:
			if b == nil || (len(b.Data) == 0 && b.URL == "") {
				return copilot.ToolResult{}, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityImage, "empty image block in tool result")
			}
			if len(b.Data) == 0 {
				return copilot.ToolResult{}, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityImage, "copilot tool results require inline image bytes; URL-only images are not supported")
			}
			mimeType, err := resolveImageMIME(model, b.MIMEType, b.Data)
			if err != nil {
				return copilot.ToolResult{}, err
			}
			binaryResults = append(binaryResults, copilot.ToolBinaryResult{
				Data:     base64.StdEncoding.EncodeToString(b.Data),
				MIMEType: mimeType,
				Type:     "image",
			})
		case *message.AudioBlock:
			return copilot.ToolResult{}, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityAudio, "copilot does not support audio blocks in tool results")
		case *message.VideoBlock:
			return copilot.ToolResult{}, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityVideo, "copilot does not support video blocks in tool results")
		case *message.DocumentBlock:
			if b == nil || (len(b.Data) == 0 && b.URL == "") {
				return copilot.ToolResult{}, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityPDF, "empty document block in tool result")
			}
			if len(b.Data) == 0 {
				return copilot.ToolResult{}, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityPDF, "copilot tool results require inline document bytes; URL-only documents are not supported")
			}
			docMIME := strings.ToLower(strings.TrimSpace(b.MIMEType))
			if docMIME == "" {
				docMIME = strings.ToLower(http.DetectContentType(b.Data))
			}
			if docMIME != "application/pdf" {
				return copilot.ToolResult{}, profile.NewUnsupportedModalityError("copilot", model, profile.ModalityPDF, fmt.Sprintf("copilot tool result documents must be application/pdf; got %s", docMIME))
			}
			binaryResults = append(binaryResults, copilot.ToolBinaryResult{
				Data:     base64.StdEncoding.EncodeToString(b.Data),
				MIMEType: docMIME,
				Type:     "resource",
			})
		}
	}
	text := sb.String()
	if text == "" {
		text = tm.Content.Text()
	}
	if text == "" && tm.StructuredContent != nil {
		if data, err := tm.CanonicalStructuredContent(); err == nil && len(data) > 0 {
			text = string(data)
		}
	}
	if tm.IsError {
		errMsg := text
		if errMsg == "" {
			errMsg = "tool execution failed"
		}
		return copilot.ToolResult{
			TextResultForLLM:    text,
			BinaryResultsForLLM: binaryResults,
			ResultType:          "failure",
			Error:               errMsg,
		}, nil
	}
	return copilot.ToolResult{
		TextResultForLLM:    text,
		BinaryResultsForLLM: binaryResults,
		ResultType:          "success",
	}, nil
}

func toArgsMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"raw": v}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{"raw": v}
	}
	return out
}

// toTokenMetrics converts Copilot AssistantUsageData to Loom TokenMetrics,
// preserving Loom's normalization rule (message/metrics.go): Input MUST include
// CacheRead and CacheWrite tokens. It also captures server-reported AI credit
// charges, model multipliers, and credit reporting status when present.
func toTokenMetrics(u *copilot.AssistantUsageData) *message.TokenMetrics {
	if u == nil {
		return nil
	}
	var inputTokens, outputTokens, cacheRead, cacheWrite, reasoning int
	if u.InputTokens != nil {
		inputTokens = int(*u.InputTokens)
	}
	if u.OutputTokens != nil {
		outputTokens = int(*u.OutputTokens)
	}
	if u.CacheReadTokens != nil {
		cacheRead = int(*u.CacheReadTokens)
	}
	if u.CacheWriteTokens != nil {
		cacheWrite = int(*u.CacheWriteTokens)
	}
	if u.ReasoningTokens != nil {
		reasoning = int(*u.ReasoningTokens)
	}

	totalInput := inputTokens
	isAnthropic := u.APIEndpoint != nil && *u.APIEndpoint == copilot.AssistantUsageAPIEndpointV1Messages
	if isAnthropic || totalInput < cacheRead+cacheWrite {
		totalInput = inputTokens + cacheRead + cacheWrite
	}

	m := &message.TokenMetrics{
		TotalTokens: totalInput + outputTokens,
		Tokens: message.TokenDetails{
			Input:      totalInput,
			Output:     outputTokens,
			CacheRead:  cacheRead,
			CacheWrite: cacheWrite,
			Reasoning:  reasoning,
		},
		Credits: toCreditMetrics(u),
	}
	if u.Duration != nil && *u.Duration > 0 {
		m.Timing.Total = time.Duration(*u.Duration) * time.Millisecond
	}
	return m
}

const nanoAiuPerCredit = 1e9

func toCreditMetrics(u *copilot.AssistantUsageData) message.CreditMetrics {
	if u == nil {
		return message.CreditMetrics{}
	}
	var credits message.CreditMetrics
	if u.Cost != nil && *u.Cost > 0 {
		credits.Multiplier = *u.Cost
	}
	if u.CopilotUsage != nil {
		credits.Amount = u.CopilotUsage.TotalNanoAiu / nanoAiuPerCredit
		credits.Unit = "ai_credits"
	} else if credits.Multiplier > 0 {
		credits.Amount = credits.Multiplier
		credits.Unit = "premium_requests"
	}
	if u.AiCreditsStatus != nil {
		credits.Status = string(*u.AiCreditsStatus)
	}
	return credits
}

func toQuotaBucket(id string, snap rpc.AccountQuotaSnapshot) llm.QuotaBucket {
	var frac float64
	if snap.IsUnlimitedEntitlement {
		frac = 1.0
	} else {
		frac = snap.RemainingPercentage / 100.0
		if frac < 0 {
			frac = 0
		} else if frac > 1 {
			frac = 1
		}
	}
	var limit, used float64
	if !snap.IsUnlimitedEntitlement && snap.EntitlementRequests > 0 {
		limit = float64(snap.EntitlementRequests)
	}
	if snap.UsedRequests > 0 {
		used = float64(snap.UsedRequests)
	}
	var resetAt *time.Time
	if snap.ResetDate != nil && !snap.ResetDate.IsZero() {
		t := snap.ResetDate.UTC()
		resetAt = &t
	}
	return llm.QuotaBucket{
		ID:                id,
		Scope:             "account",
		Unit:              llm.QuotaUnitRequests,
		Limit:             limit,
		Used:              used,
		RemainingFraction: frac,
		Unlimited:         snap.IsUnlimitedEntitlement,
		Overage:           snap.Overage,
		ResetAt:           resetAt,
	}
}
