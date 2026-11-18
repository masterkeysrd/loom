package loomclaudecode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/providers/claudecode/internal/protocol"
)

type sessionMeta struct {
	SessionID    string
	TurnToken    string
	PrefixDigest string
	SyncToken    string
}

// extractSessionMeta reads Claude Code session tracking keys from an Assistant
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
// Assistant message tagged with Claude Code session metadata, returning the
// metadata, the index of that message, and whether one was found.
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

// validateWireContent ensures content contains no media modalities that would be
// silently dropped by Claude Code CLI text/control-protocol serialization.
// ThinkingBlock and tool-related blocks remain lenient.
func validateWireContent(model, role string, content message.Content) error {
	for _, block := range content {
		if block == nil {
			continue
		}
		switch block.(type) {
		case *message.TextBlock, *message.ThinkingBlock,
			*message.ToolCall, *message.ToolCallChunk,
			*message.ServerToolCall, *message.ServerToolCallChunk, *message.ServerToolResult,
			*message.ToolReferenceBlock, *message.ToolDefinitionBlock:
			continue
		case *message.ImageBlock:
			return profile.NewUnsupportedModalityError("claudecode", model, profile.ModalityImage, fmt.Sprintf("%s message image blocks are not supported by claudecode CLI wire format", role))
		case *message.AudioBlock:
			return profile.NewUnsupportedModalityError("claudecode", model, profile.ModalityAudio, fmt.Sprintf("%s message audio blocks are not supported by claudecode CLI wire format", role))
		case *message.VideoBlock:
			return profile.NewUnsupportedModalityError("claudecode", model, profile.ModalityVideo, fmt.Sprintf("%s message video blocks are not supported by claudecode CLI wire format", role))
		case *message.DocumentBlock:
			return profile.NewUnsupportedModalityError("claudecode", model, profile.ModalityPDF, fmt.Sprintf("%s message document blocks are not supported by claudecode CLI wire format", role))
		default:
			return fmt.Errorf("claudecode: unsupported content block kind %q in %s message", block.Kind(), role)
		}
	}
	return nil
}

func validateWireMessages(model string, msgs []message.Message) error {
	for i, msg := range msgs {
		if msg == nil {
			continue
		}
		if err := validateWireContent(model, string(msg.Role()), msg.GetContent()); err != nil {
			return fmt.Errorf("message[%d] (%s): %w", i, msg.Role(), err)
		}
	}
	return nil
}

// splitSystemAndConversation extracts leading System instructions (for
// `--system-prompt`) while preserving any mid-conversation messages in order.
func splitSystemAndConversation(model string, msgs []message.Message) (string, []message.Message, error) {
	var systemParts []string
	var conv []message.Message
	seenNonSystem := false

	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		if sys, ok := msg.(*message.System); ok && !seenNonSystem {
			if err := validateWireContent(model, string(message.RoleSystem), sys.Content); err != nil {
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

// computePrefixDigest hashes the canonical conversation prefix plus request
// structural config (model, system prompt, tools, schema, thinking) so any
// Loom-side compaction, history edit, or tool change invalidates stale sessions.
func computePrefixDigest(
	req *llm.Request,
	systemPrompt string,
	prefixMsgs []message.Message,
	ext Session,
	allowedNativeTools []string,
) string {
	h := sha256.New()
	writeStr := func(s string) {
		_, _ = fmt.Fprintf(h, "%d:%s\n", len(s), s)
	}

	writeStr("sys:" + systemPrompt)
	writeStr("cwd:" + ext.WorkingDir)

	if len(ext.NativeTools) > 0 {
		writeStr("native:" + strings.Join(ext.NativeTools, ","))
	} else if len(allowedNativeTools) > 0 {
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
	}

	if req.Thinking != nil {
		b, _ := json.Marshal(req.Thinking)
		writeStr("thinking:" + string(b))
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

// formatUserDeltaPrompt renders one or more trailing non-tool messages into a
// prompt string for the `user` NDJSON message when resuming/continuing a session.
func formatUserDeltaPrompt(model string, msgs []message.Message) (string, error) {
	if len(msgs) == 1 {
		if u, ok := msgs[0].(*message.User); ok && u != nil {
			if err := validateWireContent(model, string(message.RoleUser), u.Content); err != nil {
				return "", err
			}
			return strings.TrimSpace(u.Content.Text()), nil
		}
	}
	return formatMessagesTranscript(model, msgs)
}

// formatMessagesTranscript renders a slice of Loom messages into a structured
// prompt for Path C fresh-session recovery (first turn, compaction, cold restore).
func formatMessagesTranscript(model string, msgs []message.Message) (string, error) {
	if len(msgs) == 1 {
		if u, ok := msgs[0].(*message.User); ok && u != nil {
			if err := validateWireContent(model, string(message.RoleUser), u.Content); err != nil {
				return "", err
			}
			return u.Content.Text(), nil
		}
	}

	var sb strings.Builder
	for i, m := range msgs {
		if m == nil {
			continue
		}
		if err := validateWireContent(model, string(m.Role()), m.GetContent()); err != nil {
			return "", err
		}
		if i > 0 {
			sb.WriteString("\n\n")
		}
		switch msg := m.(type) {
		case *message.System:
			sb.WriteString("[System]\n")
			sb.WriteString(msg.Content.Text())
		case *message.User:
			if len(msgs) > 1 {
				sb.WriteString("[User]\n")
			}
			sb.WriteString(msg.Content.Text())
		case *message.Assistant:
			sb.WriteString("[Assistant]\n")
			if txt := msg.Content.Text(); txt != "" {
				sb.WriteString(txt)
			}
			for _, tc := range msg.ToolCalls() {
				argsJSON, _ := json.Marshal(tc.Args)
				fmt.Fprintf(&sb, "\n[ToolCall name=%s id=%s args=%s]", tc.Name, tc.ID, string(argsJSON))
			}
		case *message.Tool:
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
			fmt.Fprintf(&sb, "[%s]\n%s", m.Role(), m.GetContent().Text())
		}
	}
	return sb.String(), nil
}

// areAllToolResults reports whether msgs is non-empty and every message is *message.Tool.
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

func toTokenMetrics(u *protocol.Usage, durationMS int64, totalCostUSD float64) *message.TokenMetrics {
	if u == nil && durationMS <= 0 && totalCostUSD <= 0 {
		return nil
	}
	var inputTokens, outputTokens, cacheRead, cacheWrite, reasoning int
	if u != nil {
		inputTokens = u.InputTokens
		outputTokens = u.OutputTokens
		cacheRead = u.CacheReadInputTokens
		cacheWrite = u.CacheCreationInputTokens
		reasoning = u.ThinkingTokens
	}
	// Follow Loom's normalization rule (message/metrics.go):
	// Input MUST include CacheRead and CacheWrite.
	totalInput := inputTokens + cacheRead + cacheWrite
	m := &message.TokenMetrics{
		TotalTokens: totalInput + outputTokens,
		Tokens: message.TokenDetails{
			Input:      totalInput,
			Output:     outputTokens,
			CacheRead:  cacheRead,
			CacheWrite: cacheWrite,
			Reasoning:  reasoning,
		},
	}
	if durationMS > 0 {
		m.Timing.Total = time.Duration(durationMS) * time.Millisecond
	}
	if totalCostUSD > 0 {
		m.Credits = message.CreditMetrics{
			Amount:    totalCostUSD,
			Unit:      "usd",
			Estimated: true,
		}
	}
	return m
}

func buildCLIArgs(
	req *llm.Request,
	cfg Config,
	ext Session,
	systemPrompt string,
	resumeSessionID string,
) ([]string, error) {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-prompt-tool", "stdio",
	}

	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	if effort := resolveEffortLevel(req.Thinking); effort != "" {
		args = append(args, "--effort", effort)
	}

	if len(req.Tools) > 0 {
		mcpCfg, err := json.Marshal(map[string]any{
			"mcpServers": map[string]any{
				DefaultMCPServerName: map[string]any{
					"type": "sdk",
					"name": DefaultMCPServerName,
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("claudecode: marshal mcp config: %w", err)
		}
		args = append(args, "--mcp-config", string(mcpCfg))
	}

	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}

	permMode := cfg.PermissionMode
	if ext.PermissionMode != "" {
		permMode = ext.PermissionMode
	}
	if permMode != "" {
		args = append(args, "--permission-mode", permMode)
	}

	nativeTools := cfg.AllowedNativeTools
	if len(ext.NativeTools) > 0 {
		nativeTools = ext.NativeTools
	}
	if len(nativeTools) == 0 {
		args = append(args, "--tools", "")
	} else {
		args = append(args, "--tools", strings.Join(nativeTools, ","))
	}

	if req.ResponseSchema != nil {
		schemaBytes, err := json.Marshal(req.ResponseSchema)
		if err != nil {
			return nil, fmt.Errorf("claudecode: marshal response schema: %w", err)
		}
		args = append(args, "--json-schema", string(schemaBytes))
	} else if req.ResponseFormat == "json_object" {
		args = append(args, "--json-schema", `{"type":"object"}`)
	}

	if resumeSessionID != "" {
		args = append(args, "--resume", resumeSessionID)
	}

	if len(cfg.ExtraArgs) > 0 {
		args = append(args, cfg.ExtraArgs...)
	}

	return args, nil
}

func resolveAllowedNativeTools(cfg Config, ext Session) []string {
	if len(ext.NativeTools) > 0 {
		return ext.NativeTools
	}
	return cfg.AllowedNativeTools
}

func resolveEffortLevel(tc *llm.ThinkingConfig) string {
	if tc == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(tc.Effort))
}
