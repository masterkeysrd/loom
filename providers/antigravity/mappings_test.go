package loomantigravity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/masterkeysrd/loom/auth"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/providers/antigravity/sdk"
	"github.com/masterkeysrd/loom/tool"
)

func TestToGenerateContentRequest_RejectsMediaInSystemAndToolAndAssistant(t *testing.T) {
	mediaBlocks := []struct {
		name  string
		block message.Block
	}{
		{"image", &message.ImageBlock{Data: []byte{1, 2, 3}, MIMEType: "image/png"}},
		{"audio", &message.AudioBlock{Data: []byte{1, 2, 3}, MIMEType: "audio/mp3"}},
		{"video", &message.VideoBlock{Data: []byte{1, 2, 3}, MIMEType: "video/mp4"}},
		{"document", &message.DocumentBlock{Data: []byte("%PDF"), MIMEType: "application/pdf"}},
	}

	for _, mb := range mediaBlocks {
		t.Run("system_"+mb.name, func(t *testing.T) {
			req := &llm.Request{
				Model: "gemini-2.5-pro",
				Messages: []message.Message{
					&message.System{Content: message.Content{
						&message.TextBlock{Text: "Be concise."},
						mb.block,
					}},
					message.NewUserText("hi"),
				},
			}
			_, err := toGenerateContentRequest(req)
			if !errors.Is(err, profile.ErrUnsupportedModality) {
				t.Fatalf("expected ErrUnsupportedModality for system %s, got: %v", mb.name, err)
			}
		})

		t.Run("assistant_"+mb.name, func(t *testing.T) {
			req := &llm.Request{
				Model: "gemini-2.5-pro",
				Messages: []message.Message{
					message.NewUserText("hi"),
					&message.Assistant{Content: message.Content{mb.block}},
				},
			}
			_, err := toGenerateContentRequest(req)
			if !errors.Is(err, profile.ErrUnsupportedModality) {
				t.Fatalf("expected ErrUnsupportedModality for assistant %s, got: %v", mb.name, err)
			}
		})

		t.Run("tool_"+mb.name, func(t *testing.T) {
			req := &llm.Request{
				Model: "gemini-2.5-pro",
				Messages: []message.Message{
					message.NewUserText("hi"),
					&message.Tool{
						ToolCallID: "call-1",
						Name:       "read_file",
						Content: message.Content{
							&message.TextBlock{Text: "ok"},
							mb.block,
						},
					},
				},
			}
			_, err := toGenerateContentRequest(req)
			if !errors.Is(err, profile.ErrUnsupportedModality) {
				t.Fatalf("expected ErrUnsupportedModality for tool %s, got: %v", mb.name, err)
			}
		})
	}

	// Non-media unsupported blocks in System and Tool must also error (not silently drop).
	if _, err := toGenerateContentRequest(&llm.Request{
		Model: "gemini-2.5-pro",
		Messages: []message.Message{
			&message.System{Content: message.Content{&message.ToolCall{ID: "1", Name: "fn"}}},
		},
	}); err == nil {
		t.Fatal("expected error for ToolCall in System message")
	}

	if _, err := toGenerateContentRequest(&llm.Request{
		Model: "gemini-2.5-pro",
		Messages: []message.Message{
			&message.Tool{
				ToolCallID: "call-1",
				Name:       "fn",
				Content:    message.Content{&message.ToolCall{ID: "2", Name: "fn2"}},
			},
		},
	}); err == nil {
		t.Fatal("expected error for ToolCall in Tool message")
	}
}

func TestToGenerateContentRequest_UserMediaEmptyAndValid(t *testing.T) {
	emptyCases := []struct {
		name  string
		block message.Block
	}{
		{"empty_image", &message.ImageBlock{MIMEType: "image/png"}},
		{"empty_audio", &message.AudioBlock{MIMEType: "audio/wav"}},
		{"empty_video", &message.VideoBlock{MIMEType: "video/mp4"}},
		{"empty_document", &message.DocumentBlock{MIMEType: "application/pdf"}},
		{"nil_image", (*message.ImageBlock)(nil)},
		{"nil_audio", (*message.AudioBlock)(nil)},
		{"nil_video", (*message.VideoBlock)(nil)},
		{"nil_document", (*message.DocumentBlock)(nil)},
	}

	for _, tc := range emptyCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &llm.Request{
				Model: "gemini-2.5-pro",
				Messages: []message.Message{
					message.NewUser(tc.block),
				},
			}
			_, err := toGenerateContentRequest(req)
			if !errors.Is(err, profile.ErrUnsupportedModality) {
				t.Fatalf("expected ErrUnsupportedModality for %s, got: %v", tc.name, err)
			}
		})
	}

	// Valid inline Data and URL FileData pass through.
	req := &llm.Request{
		Model: "gemini-2.5-pro",
		Messages: []message.Message{
			message.NewUser(
				&message.TextBlock{Text: "inspect these"},
				&message.ImageBlock{Data: []byte("png-bytes"), MIMEType: "image/png"},
				&message.AudioBlock{URL: "gs://bucket/audio.mp3", MIMEType: "audio/mp3"},
				&message.VideoBlock{URL: "https://example.com/clip.mp4", MIMEType: "video/mp4"},
				&message.DocumentBlock{Data: []byte("%PDF-1.4"), MIMEType: "application/pdf"},
			),
		},
	}
	wire, err := toGenerateContentRequest(req)
	if err != nil {
		t.Fatalf("expected valid multimodal request to succeed, got: %v", err)
	}
	if len(wire.Contents) != 1 || len(wire.Contents[0].Parts) != 5 {
		t.Fatalf("expected 5 parts in user turn, got %+v", wire.Contents)
	}
	if wire.Contents[0].Parts[1].InlineData == nil || wire.Contents[0].Parts[2].FileData == nil ||
		wire.Contents[0].Parts[3].FileData == nil || wire.Contents[0].Parts[4].InlineData == nil {
		t.Fatalf("unexpected media part mapping: %+v", wire.Contents[0].Parts)
	}
}

func TestToCountTokensRequest_RejectsMediaInSystemAndTool(t *testing.T) {
	_, err := ToCountTokensRequest(message.MessageList{
		&message.System{Content: message.Content{&message.ImageBlock{Data: []byte{1}, MIMEType: "image/png"}}},
	})
	if !errors.Is(err, profile.ErrUnsupportedModality) {
		t.Fatalf("expected ErrUnsupportedModality in ToCountTokensRequest system, got: %v", err)
	}

	_, err = ToCountTokensRequest(message.MessageList{
		&message.Tool{ToolCallID: "1", Name: "t", Content: message.Content{&message.DocumentBlock{URL: "https://example.com/a.pdf"}}},
	})
	if !errors.Is(err, profile.ErrUnsupportedModality) {
		t.Fatalf("expected ErrUnsupportedModality in ToCountTokensRequest tool, got: %v", err)
	}
}

func TestToGenerateContentRequest_PreservesAssistantThinkingBlock(t *testing.T) {
	req := &llm.Request{
		Model: "gemini-2.5-pro",
		Messages: []message.Message{
			message.NewUserText("solve 2+2"),
			&message.Assistant{
				Content: message.Content{
					&message.ThinkingBlock{
						Thinking: "2+2 is 4",
						Extras:   map[string]any{ThoughtSignatureKey: "sig-thought"},
					},
					&message.TextBlock{Text: "4"},
				},
			},
			message.NewUserText("thanks"),
		},
	}

	wire, err := toGenerateContentRequest(req)
	if err != nil {
		t.Fatalf("expected ThinkingBlock in assistant history to be preserved, got: %v", err)
	}
	if len(wire.Contents) != 3 || len(wire.Contents[1].Parts) != 2 {
		t.Fatalf("unexpected contents: %+v", wire.Contents)
	}
	if !wire.Contents[1].Parts[0].Thought || wire.Contents[1].Parts[0].ThoughtSignature != "sig-thought" {
		t.Errorf("expected Thought=true and ThoughtSignature='sig-thought', got %+v", wire.Contents[1].Parts[0])
	}
}

func TestValidateModalityAndCapability_StreamProfileAndWireChecks(t *testing.T) {
	var fetchModelsCalls atomic.Int32
	var streamCalls atomic.Int32

	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			switch req.URL.Path {
			case "/v1internal:loadCodeAssist":
				rec.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(rec).Encode(map[string]any{
					"cloudaicompanionProject": "test-proj",
				})
			case "/v1internal:fetchAvailableModels":
				fetchModelsCalls.Add(1)
				rec.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(rec).Encode(map[string]any{
					"models": map[string]any{
						"gemini-3.8-flash-tiered": map[string]any{
							"displayName":      "Gemini 3.8 Flash",
							"maxTokens":        1048576,
							"maxOutputTokens":  65536,
							"supportsImages":   true,
							"supportsVideo":    false,
							"supportsThinking": true,
						},
					},
				})
			case "/v1internal:streamGenerateContent":
				streamCalls.Add(1)
				rec.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(rec, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}}\n\n")
			default:
				http.NotFound(rec, req)
			}
			return rec.Result(), nil
		}),
	}

	// 1. Cold cache: no override / no SyncProfiles -> must NOT call fetchAvailableModels for profile validation.
	// Unlisted model with valid wire image + tools passes through.
	pCold, err := NewProvider(&Config{
		TokenSource: auth.StaticTokenSource("tok"),
		BaseURL:     "https://antigravity.test",
		HTTPClient:  httpClient,
		// Custom resolver avoids ensureAvailableModels network dependency checks.
		Resolver: func(model string, req sdk.GenerateContentRequest, _ map[string]bool) (string, sdk.GenerateContentRequest) {
			return model, req
		},
	})
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}

	// Simulate already-populated availableModels so ensureAvailableModels doesn't fetch either,
	// verifying Stream profile validation itself never fetches on cold p.cached.
	pCold.recordAvailableModels([]sdk.AvailableModel{{ID: "unlisted-model"}}, "tok")

	stream, err := pCold.Stream(context.Background(), &llm.Request{
		Model: "unlisted-model",
		Messages: []message.Message{
			message.NewUser(&message.ImageBlock{Data: []byte{1, 2, 3}, MIMEType: "image/png"}),
		},
		Tools: []tool.Definition{{Name: "ping"}},
	})
	if err != nil {
		t.Fatalf("expected cold/unlisted model with valid wire media to pass through, got: %v", err)
	}
	for range stream {
	}
	if fetchModelsCalls.Load() != 0 {
		t.Fatalf("expected 0 fetchAvailableModels calls on cold profile cache, got %d", fetchModelsCalls.Load())
	}
	if streamCalls.Load() != 1 {
		t.Fatalf("expected 1 stream call, got %d", streamCalls.Load())
	}

	// 2. OverrideProfile: text-only and no-tool profile rejects image & tools before HTTP call.
	pCold.OverrideProfile("text-only-no-tools", profile.Profile{
		ID: "text-only-no-tools",
		Capabilities: profile.Capabilities{
			Completion: true,
			ToolCall:   false,
		},
		Modalities: profile.Modalities{
			Inputs:  []profile.Modality{profile.ModalityText},
			Outputs: []profile.Modality{profile.ModalityText},
		},
	})

	_, err = pCold.Stream(context.Background(), &llm.Request{
		Model: "text-only-no-tools",
		Messages: []message.Message{
			message.NewUser(&message.ImageBlock{Data: []byte{1, 2, 3}, MIMEType: "image/png"}),
		},
	})
	if !errors.Is(err, profile.ErrUnsupportedModality) {
		t.Fatalf("expected ErrUnsupportedModality for text-only override, got: %v", err)
	}

	_, err = pCold.Stream(context.Background(), &llm.Request{
		Model:    "text-only-no-tools",
		Messages: []message.Message{message.NewUserText("hello")},
		Tools:    []tool.Definition{{Name: "get_weather"}},
	})
	if !errors.Is(err, profile.ErrUnsupportedCapability) {
		t.Fatalf("expected ErrUnsupportedCapability for ToolCall=false override, got: %v", err)
	}

	// Embedding-only override rejects Stream completion.
	pCold.OverrideProfile("embed-only", profile.Profile{
		ID: "embed-only",
		Capabilities: profile.Capabilities{
			Completion: false,
			Embedding:  true,
		},
	})
	_, err = pCold.Stream(context.Background(), &llm.Request{
		Model:    "embed-only",
		Messages: []message.Message{message.NewUserText("hello")},
	})
	if !errors.Is(err, profile.ErrUnsupportedCapability) {
		t.Fatalf("expected ErrUnsupportedCapability for non-completion override, got: %v", err)
	}

	// Minimal empty override (no declared modalities/capabilities) passes through cleanly.
	pCold.OverrideProfile("minimal-override", profile.Profile{
		ID:   "minimal-override",
		Name: "Minimal",
	})
	streamMin, err := pCold.Stream(context.Background(), &llm.Request{
		Model:    "minimal-override",
		Messages: []message.Message{message.NewUserText("hello")},
	})
	if err != nil {
		t.Fatalf("expected minimal override to pass through, got: %v", err)
	}
	for range streamMin {
	}

	if streamCalls.Load() != 2 {
		t.Fatalf("expected failed profile checks not to reach streamGenerateContent (got %d calls)", streamCalls.Load())
	}

	// 3. Discovered profiles via SyncProfiles + Alias resolution:
	//    gemini-3.8-flash supports images (true) but NOT video (false).
	pSynced, err := NewProvider(&Config{
		TokenSource: auth.StaticTokenSource("tok"),
		BaseURL:     "https://antigravity.test",
		HTTPClient:  httpClient,
		Aliases: map[string]string{
			"fast": "gemini-3.8-flash-low",
		},
	})
	if err != nil {
		t.Fatalf("NewProvider synced failed: %v", err)
	}
	if err := pSynced.SyncProfiles(context.Background()); err != nil {
		t.Fatalf("SyncProfiles failed: %v", err)
	}

	// Image allowed on gemini-3.8-flash (and via alias "fast")
	streamImg, err := pSynced.Stream(context.Background(), &llm.Request{
		Model: "fast",
		Messages: []message.Message{
			message.NewUser(&message.ImageBlock{Data: []byte{1, 2, 3}, MIMEType: "image/png"}),
		},
	})
	if err != nil {
		t.Fatalf("expected image on synced model via alias to succeed, got: %v", err)
	}
	for range streamImg {
	}

	// Video rejected on gemini-3.8-flash (and via alias "fast")
	_, err = pSynced.Stream(context.Background(), &llm.Request{
		Model: "fast",
		Messages: []message.Message{
			message.NewUser(&message.VideoBlock{Data: []byte{1, 2, 3}, MIMEType: "video/mp4"}),
		},
	})
	if !errors.Is(err, profile.ErrUnsupportedModality) {
		t.Fatalf("expected ErrUnsupportedModality for video on synced image-only model, got: %v", err)
	}

	// Unknown model after SyncProfiles still skips profile check and passes valid wire video through.
	streamUnlisted, err := pSynced.Stream(context.Background(), &llm.Request{
		Model: "brand-new-unlisted-model",
		Messages: []message.Message{
			message.NewUser(&message.VideoBlock{URL: "gs://bucket/video.mp4", MIMEType: "video/mp4"}),
		},
	})
	if err != nil {
		t.Fatalf("expected unlisted model to bypass profile check and pass wire video, got: %v", err)
	}
	for range streamUnlisted {
	}

	// Wire-level rejection in Stream also wraps profile.ErrUnsupportedModality.
	_, err = pSynced.Stream(context.Background(), &llm.Request{
		Model: "brand-new-unlisted-model",
		Messages: []message.Message{
			&message.System{Content: message.Content{&message.ImageBlock{Data: []byte{1}, MIMEType: "image/png"}}},
		},
	})
	if !errors.Is(err, profile.ErrUnsupportedModality) {
		t.Fatalf("expected Stream wire-level rejection to wrap ErrUnsupportedModality, got: %v", err)
	}
	if !strings.Contains(err.Error(), "antigravity") {
		t.Errorf("expected error to mention provider name, got: %v", err)
	}
}
