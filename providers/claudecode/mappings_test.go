package loomclaudecode

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/providers/claudecode/internal/protocol"
	"github.com/masterkeysrd/loom/tool"
)

func TestToTokenMetricsNormalization(t *testing.T) {
	u := &protocol.Usage{
		InputTokens:              120,
		OutputTokens:             45,
		CacheReadInputTokens:     800,
		CacheCreationInputTokens: 200,
		ThinkingTokens:           15,
	}
	m := toTokenMetrics(u, 350, 0.042)
	if m == nil {
		t.Fatal("expected non-nil TokenMetrics")
	}
	// Input must include uncached + cacheRead + cacheWrite per message/metrics.go.
	wantInput := 120 + 800 + 200
	if m.Tokens.Input != wantInput {
		t.Fatalf("Tokens.Input = %d, want %d", m.Tokens.Input, wantInput)
	}
	if m.Tokens.Output != 45 {
		t.Fatalf("Tokens.Output = %d, want 45", m.Tokens.Output)
	}
	if m.Tokens.CacheRead != 800 {
		t.Fatalf("Tokens.CacheRead = %d, want 800", m.Tokens.CacheRead)
	}
	if m.Tokens.CacheWrite != 200 {
		t.Fatalf("Tokens.CacheWrite = %d, want 200", m.Tokens.CacheWrite)
	}
	if m.Tokens.Reasoning != 15 {
		t.Fatalf("Tokens.Reasoning = %d, want 15", m.Tokens.Reasoning)
	}
	if m.TotalTokens != wantInput+45 {
		t.Fatalf("TotalTokens = %d, want %d", m.TotalTokens, wantInput+45)
	}
	if m.Timing.Total.Milliseconds() != 350 {
		t.Fatalf("Timing.Total = %v, want 350ms", m.Timing.Total)
	}
	if m.Credits.Amount != 0.042 || m.Credits.Unit != "usd" || !m.Credits.Estimated {
		t.Fatalf("Credits = %+v, want {Amount:0.042 Unit:usd Estimated:true}", m.Credits)
	}
}

func TestSplitSystemAndConversation(t *testing.T) {
	msgs := []message.Message{
		&message.System{Content: message.Content{&message.TextBlock{Text: "Sys 1"}}},
		&message.System{Content: message.Content{&message.TextBlock{Text: "Sys 2"}}},
		&message.User{Content: message.Content{&message.TextBlock{Text: "Hi"}}},
		&message.System{Content: message.Content{&message.TextBlock{Text: "Mid Sys"}}},
	}

	sys, conv, err := splitSystemAndConversation("sonnet", msgs)
	if err != nil {
		t.Fatalf("splitSystemAndConversation: %v", err)
	}
	if sys != "Sys 1\n\nSys 2" {
		t.Fatalf("system = %q, want %q", sys, "Sys 1\n\nSys 2")
	}
	if len(conv) != 2 {
		t.Fatalf("len(conv) = %d, want 2", len(conv))
	}
}

func TestPrefixDigestDetectsMutations(t *testing.T) {
	baseReq := &llm.Request{
		Model: "sonnet",
		Tools: []tool.Definition{
			{Name: "read_file", Description: "Read a file"},
		},
	}
	msgs := []message.Message{
		&message.User{Content: message.Content{&message.TextBlock{Text: "hello"}}},
		&message.Assistant{Content: message.Content{&message.TextBlock{Text: "world"}}},
	}

	d1 := computePrefixDigest(baseReq, "be concise", msgs, Session{}, nil)
	d2 := computePrefixDigest(baseReq, "be concise", msgs, Session{}, nil)
	if d1 != d2 {
		t.Fatalf("expected deterministic digest, got %q != %q", d1, d2)
	}

	// Mutating an earlier message must change digest (compaction / edit).
	editedMsgs := []message.Message{
		&message.User{Content: message.Content{&message.TextBlock{Text: "compacted summary"}}},
		&message.Assistant{Content: message.Content{&message.TextBlock{Text: "world"}}},
	}
	dEdited := computePrefixDigest(baseReq, "be concise", editedMsgs, Session{}, nil)
	if dEdited == d1 {
		t.Fatal("expected digest to change when earlier message is edited/compacted")
	}

	// Changing tools must change digest.
	reqMoreTools := &llm.Request{
		Model: "sonnet",
		Tools: []tool.Definition{
			{Name: "read_file", Description: "Read a file"},
			{Name: "bash", Description: "Run bash"},
		},
	}
	dTools := computePrefixDigest(reqMoreTools, "be concise", msgs, Session{}, nil)
	if dTools == d1 {
		t.Fatal("expected digest to change when tools change")
	}

	// Changing schema must change digest.
	reqSchema := &llm.Request{
		Model:          "sonnet",
		Tools:          baseReq.Tools,
		ResponseSchema: &jsonschema.Schema{Type: "object"},
	}
	dSchema := computePrefixDigest(reqSchema, "be concise", msgs, Session{}, nil)
	if dSchema == d1 {
		t.Fatal("expected digest to change when ResponseSchema changes")
	}
}

func TestFindLastSessionCheckpoint(t *testing.T) {
	msgs := []message.Message{
		&message.User{Content: message.Content{&message.TextBlock{Text: "q1"}}},
		&message.Assistant{
			Base: message.Base{
				Metadata: map[string]any{
					SessionIDKey:    "sess_1",
					TurnTokenKey:    "tok_1",
					PrefixDigestKey: "dig_1",
				},
			},
			Content: message.Content{&message.TextBlock{Text: "a1"}},
		},
		&message.User{Content: message.Content{&message.TextBlock{Text: "q2"}}},
		&message.Assistant{
			Content: message.Content{
				&message.TextBlock{
					Text: "a2",
					Extras: map[string]any{
						SessionIDKey:    "sess_2",
						TurnTokenKey:    "tok_2",
						PrefixDigestKey: "dig_2",
					},
				},
			},
		},
		&message.User{Content: message.Content{&message.TextBlock{Text: "q3"}}},
	}

	meta, idx, ok := findLastSessionCheckpoint(msgs)
	if !ok {
		t.Fatal("expected to find session checkpoint")
	}
	if idx != 3 {
		t.Fatalf("idx = %d, want 3", idx)
	}
	if meta.SessionID != "sess_2" || meta.TurnToken != "tok_2" || meta.PrefixDigest != "dig_2" {
		t.Fatalf("unexpected meta: %+v", meta)
	}
}

func TestBuildCLIArgsAndNormalizeToolName(t *testing.T) {
	if got := normalizeToolName("mcp__loom__shell_command"); got != "shell_command" {
		t.Fatalf("normalizeToolName = %q, want shell_command", got)
	}
	if got := normalizeToolName("plain_tool"); got != "plain_tool" {
		t.Fatalf("normalizeToolName = %q, want plain_tool", got)
	}

	cfg := normalizeConfig(&Config{PermissionMode: "bypassPermissions"})
	req := &llm.Request{
		Model:          "sonnet",
		ResponseFormat: "json_object",
		Thinking:       &llm.ThinkingConfig{Effort: "xhigh"},
		Tools:          []tool.Definition{{Name: "shell_command"}},
	}
	args, err := buildCLIArgs(req, cfg, Session{}, "sys prompt", "sess_resume_1")
	if err != nil {
		t.Fatalf("buildCLIArgs: %v", err)
	}

	joined := " " + joinSpace(args) + " "
	for _, want := range []string{
		" --input-format stream-json ",
		" --output-format stream-json ",
		" --permission-prompt-tool stdio ",
		" --mcp-config ",
		" --model sonnet ",
		" --effort xhigh ",
		" --system-prompt sys prompt ",
		" --tools  ",
		" --resume sess_resume_1 ",
		` --json-schema {"type":"object"} `,
	} {
		if !containsSubstr(joined, want) {
			t.Fatalf("expected %q in args %v", want, args)
		}
	}

	if got := resolveEffortLevel(&llm.ThinkingConfig{Budget: 8192}); got != "" {
		t.Fatalf("resolveEffortLevel(budget-only) = %q, want empty", got)
	}
	if got := resolveEffortLevel(&llm.ThinkingConfig{Effort: "HIGH"}); got != "high" {
		t.Fatalf("resolveEffortLevel(HIGH) = %q, want high", got)
	}
}

func joinSpace(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}

func containsSubstr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestWireModalityValidationRejectsSilentDrops(t *testing.T) {
	mediaCases := []struct {
		name  string
		block message.Block
		want  profile.Modality
	}{
		{
			name:  "image bytes",
			block: &message.ImageBlock{Data: []byte{1, 2, 3}, MIMEType: "image/png"},
			want:  profile.ModalityImage,
		},
		{
			name:  "image url",
			block: &message.ImageBlock{URL: "https://example.com/img.png"},
			want:  profile.ModalityImage,
		},
		{
			name:  "empty image",
			block: &message.ImageBlock{},
			want:  profile.ModalityImage,
		},
		{
			name:  "audio",
			block: &message.AudioBlock{Data: []byte{1, 2}, MIMEType: "audio/mp3"},
			want:  profile.ModalityAudio,
		},
		{
			name:  "video",
			block: &message.VideoBlock{URL: "https://example.com/vid.mp4", MIMEType: "video/mp4"},
			want:  profile.ModalityVideo,
		},
		{
			name:  "document pdf",
			block: &message.DocumentBlock{Data: []byte("%PDF"), MIMEType: "application/pdf"},
			want:  profile.ModalityPDF,
		},
		{
			name:  "document text/plain",
			block: &message.DocumentBlock{Data: []byte("hello"), MIMEType: "text/plain"},
			want:  profile.ModalityPDF,
		},
		{
			name:  "empty document",
			block: &message.DocumentBlock{},
			want:  profile.ModalityPDF,
		},
	}

	assertModalityErr := func(t *testing.T, err error, want profile.Modality) {
		t.Helper()
		if err == nil {
			t.Fatalf("expected error for modality %s, got nil", want)
		}
		if !errors.Is(err, profile.ErrUnsupportedModality) {
			t.Fatalf("expected ErrUnsupportedModality, got %v", err)
		}
		var modErr *profile.UnsupportedModalityError
		if !errors.As(err, &modErr) || modErr.Modality != want || modErr.Provider != "claudecode" {
			t.Fatalf("unexpected UnsupportedModalityError: %+v (err=%v)", modErr, err)
		}
	}

	for _, tc := range mediaCases {
		t.Run("splitSystemAndConversation/"+tc.name, func(t *testing.T) {
			_, _, err := splitSystemAndConversation("sonnet", []message.Message{
				&message.System{Content: message.Content{&message.TextBlock{Text: "sys"}, tc.block}},
			})
			assertModalityErr(t, err, tc.want)
		})

		t.Run("formatUserDeltaPrompt/"+tc.name, func(t *testing.T) {
			_, err := formatUserDeltaPrompt("sonnet", []message.Message{
				&message.User{Content: message.Content{&message.TextBlock{Text: "hi"}, tc.block}},
			})
			assertModalityErr(t, err, tc.want)
		})

		t.Run("formatMessagesTranscript/user/"+tc.name, func(t *testing.T) {
			_, err := formatMessagesTranscript("sonnet", []message.Message{
				&message.User{Content: message.Content{tc.block}},
			})
			assertModalityErr(t, err, tc.want)
		})

		t.Run("formatMessagesTranscript/assistant/"+tc.name, func(t *testing.T) {
			_, err := formatMessagesTranscript("sonnet", []message.Message{
				&message.User{Content: message.Content{&message.TextBlock{Text: "hi"}}},
				&message.Assistant{Content: message.Content{&message.TextBlock{Text: "ok"}, tc.block}},
			})
			assertModalityErr(t, err, tc.want)
		})

		t.Run("formatMessagesTranscript/tool/"+tc.name, func(t *testing.T) {
			_, err := formatMessagesTranscript("sonnet", []message.Message{
				&message.User{Content: message.Content{&message.TextBlock{Text: "hi"}}},
				&message.Tool{
					ToolCallID: "call_1",
					Name:       "read",
					Content:    message.Content{&message.TextBlock{Text: "out"}, tc.block},
				},
			})
			assertModalityErr(t, err, tc.want)
		})

		t.Run("buildMCPToolResultControlResponse/"+tc.name, func(t *testing.T) {
			_, err := buildMCPToolResultControlResponse("sonnet", pendingMCPCall{
				ControlRequestID: "ctrl_1",
				JSONRPCID:        json.RawMessage(`1`),
				ToolCallID:       "call_1",
				ToolName:         "read",
			}, &message.Tool{
				ToolCallID: "call_1",
				Name:       "read",
				Content:    message.Content{&message.TextBlock{Text: "out"}, tc.block},
			})
			assertModalityErr(t, err, tc.want)
		})
	}
}

func TestThinkingBlockRemainsLenientOnTranscript(t *testing.T) {
	msgs := []message.Message{
		&message.User{Content: message.Content{&message.TextBlock{Text: "q1"}}},
		&message.Assistant{
			Content: message.Content{
				&message.ThinkingBlock{Thinking: "secret chain of thought"},
				&message.TextBlock{Text: "a1"},
				&message.ToolCall{ID: "tc_1", Name: "echo", Args: map[string]any{"x": 1}},
			},
		},
		&message.Tool{
			ToolCallID: "tc_1",
			Name:       "echo",
			Content:    message.Content{&message.TextBlock{Text: "ok"}},
		},
	}

	transcript, err := formatMessagesTranscript("sonnet", msgs)
	if err != nil {
		t.Fatalf("formatMessagesTranscript failed with ThinkingBlock: %v", err)
	}
	if containsSubstr(transcript, "secret chain of thought") {
		t.Fatalf("did not expect thinking block in transcript, got %q", transcript)
	}
	if !containsSubstr(transcript, "a1") || !containsSubstr(transcript, "[ToolCall name=echo id=tc_1") {
		t.Fatalf("expected assistant text and tool call in transcript, got %q", transcript)
	}
}
