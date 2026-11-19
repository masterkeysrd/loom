package loomgenai

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/tool"
	"google.golang.org/genai"
)

func TestToModelParts(t *testing.T) {
	signature := []byte("test-signature")
	encodedSignature := encodeThoughtSignature(signature)

	content := message.Content{
		&message.ThinkingBlock{
			Thinking: "thinking content",
			Extras: map[string]any{
				ThoughtSignatureKey: encodedSignature,
			},
		},
		&message.TextBlock{
			Text: "final answer",
			Extras: map[string]any{
				ThoughtSignatureKey: encodedSignature,
			},
		},
		&message.ToolCall{
			ID:   "call_123",
			Name: "test_tool",
			Args: map[string]any{"arg1": "val1"},
			Extras: map[string]any{
				ThoughtSignatureKey: encodedSignature,
			},
		},
	}

	parts, err := toModelParts(content)
	if err != nil {
		t.Fatalf("toModelParts failed: %v", err)
	}

	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}

	// Check ThinkingBlock part
	if parts[0].Text != "thinking content" || !parts[0].Thought {
		t.Errorf("parts[0] mismatch: Text=%q, Thought=%v", parts[0].Text, parts[0].Thought)
	}
	if !reflect.DeepEqual(parts[0].ThoughtSignature, signature) {
		t.Errorf("parts[0] ThoughtSignature mismatch: expected %v, got %v", signature, parts[0].ThoughtSignature)
	}

	// Check TextBlock part
	if parts[1].Text != "final answer" || parts[1].Thought {
		t.Errorf("parts[1] mismatch: Text=%q, Thought=%v", parts[1].Text, parts[1].Thought)
	}
	if !reflect.DeepEqual(parts[1].ThoughtSignature, signature) {
		t.Errorf("parts[1] ThoughtSignature mismatch: expected %v, got %v", signature, parts[1].ThoughtSignature)
	}

	// Check ToolCall part
	if parts[2].FunctionCall == nil || parts[2].FunctionCall.ID != "call_123" {
		t.Errorf("parts[2] FunctionCall mismatch")
	}
	if !reflect.DeepEqual(parts[2].ThoughtSignature, signature) {
		t.Errorf("parts[2] ThoughtSignature mismatch: expected %v, got %v", signature, parts[2].ThoughtSignature)
	}
}

func TestToModelParts_DummySignature(t *testing.T) {
	content := message.Content{
		&message.ToolCall{
			ID:   "call_1",
			Name: "tool1",
		},
		&message.ToolCall{
			ID:   "call_2",
			Name: "tool2",
		},
	}

	parts, err := toModelParts(content)
	if err != nil {
		t.Fatalf("toModelParts failed: %v", err)
	}

	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}

	// First ToolCall should have dummy signature
	decodedDummy, _ := decodeThoughtSignature(DummyThoughtSignature)
	if !reflect.DeepEqual(parts[0].ThoughtSignature, decodedDummy) {
		t.Errorf("parts[0] expected dummy signature, got %v", parts[0].ThoughtSignature)
	}

	// Second ToolCall should NOT have a signature (only first one needs it if missing)
	if len(parts[1].ThoughtSignature) > 0 {
		t.Errorf("parts[1] should not have a signature, got %q", string(parts[1].ThoughtSignature))
	}
}

func TestToModelParts_EmptyThinkingBlock(t *testing.T) {
	sig := encodeThoughtSignature([]byte("sig"))
	content := message.Content{
		&message.ThinkingBlock{Thinking: "", Extras: map[string]any{ThoughtSignatureKey: sig}},
		&message.TextBlock{Text: "answer"},
	}

	parts, err := toModelParts(content)
	if err != nil {
		t.Fatalf("toModelParts failed: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part (empty thought dropped), got %d", len(parts))
	}
	if parts[0].Thought || parts[0].Text != "answer" || string(parts[0].ThoughtSignature) != "sig" {
		t.Errorf("unexpected part: %+v", parts[0])
	}
}

func TestToAssistantChunk(t *testing.T) {
	signature := []byte("test-signature")
	encodedSignature := encodeThoughtSignature(signature)

	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{
							Text:             "thinking content",
							Thought:          true,
							ThoughtSignature: signature,
						},
						{
							Text:             "final answer",
							ThoughtSignature: signature,
						},
						{
							FunctionCall: &genai.FunctionCall{
								ID:   "call_123",
								Name: "test_tool",
								Args: map[string]any{"arg1": "val1"},
							},
							ThoughtSignature: signature,
						},
					},
				},
			},
		},
	}

	chunk, err := toAssistantChunk(resp)
	if err != nil {
		t.Fatalf("toAssistantChunk failed: %v", err)
	}

	if len(chunk.Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(chunk.Content))
	}

	// Check ThinkingBlock
	tb, ok := chunk.Content[0].(*message.ThinkingBlock)
	if !ok {
		t.Fatalf("expected ThinkingBlock, got %T", chunk.Content[0])
	}
	if tb.Thinking != "thinking content" {
		t.Errorf("ThinkingBlock mismatch: expected %q, got %q", "thinking content", tb.Thinking)
	}
	if tb.Extras[ThoughtSignatureKey] != encodedSignature {
		t.Errorf("ThinkingBlock signature mismatch: expected %q, got %q", encodedSignature, tb.Extras[ThoughtSignatureKey])
	}

	// Check TextBlock
	txt, ok := chunk.Content[1].(*message.TextBlock)
	if !ok {
		t.Fatalf("expected TextBlock, got %T", chunk.Content[1])
	}
	if txt.Text != "final answer" {
		t.Errorf("TextBlock mismatch: expected %q, got %q", "final answer", txt.Text)
	}
	if txt.Extras[ThoughtSignatureKey] != encodedSignature {
		t.Errorf("TextBlock signature mismatch: expected %q, got %q", encodedSignature, txt.Extras[ThoughtSignatureKey])
	}

	// Check ToolCall
	tc, ok := chunk.Content[2].(*message.ToolCall)
	if !ok {
		t.Fatalf("expected ToolCall, got %T", chunk.Content[2])
	}
	if tc.ID != "call_123" {
		t.Errorf("ToolCall ID mismatch: expected %q, got %q", "call_123", tc.ID)
	}
	if tc.Extras[ThoughtSignatureKey] != encodedSignature {
		t.Errorf("ToolCall signature mismatch: expected %q, got %q", encodedSignature, tc.Extras[ThoughtSignatureKey])
	}
}

func TestToUserPartsMultimodal(t *testing.T) {
	content := message.Content{
		&message.TextBlock{Text: "Look at this image"},
		&message.ImageBlock{
			Data:     []byte("data"),
			MIMEType: "image/png",
		},
	}

	parts, err := toUserParts(content)
	if err != nil {
		t.Fatalf("toUserParts failed: %v", err)
	}

	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}

	if parts[0].Text != "Look at this image" {
		t.Errorf("expected parts[0] to be text, got %q", parts[0].Text)
	}
	if parts[1].InlineData == nil || parts[1].InlineData.MIMEType != "image/png" {
		t.Errorf("expected parts[1] to be inline data image/png, got %#v", parts[1].InlineData)
	}
}

func TestToGenerateContentArgsStructuredOutput(t *testing.T) {
	schema, _ := jsonschema.For[map[string]string](nil)

	req := &llm.Request{
		Model:          "gemini-1.5-flash",
		ResponseSchema: schema,
	}

	_, config, err := toGenerateContentArgs(req)
	if err != nil {
		t.Fatalf("toGenerateContentArgs failed: %v", err)
	}

	if config.ResponseMIMEType != "application/json" {
		t.Errorf("expected ResponseMIMEType to be application/json, got %q", config.ResponseMIMEType)
	}
	if config.ResponseJsonSchema != schema {
		t.Error("ResponseJsonSchema not set correctly in config")
	}
}

func TestToGenerateContentArgsThinkingLevelCasing(t *testing.T) {
	cases := []struct {
		effort string
		want   genai.ThinkingLevel
	}{
		{"low", genai.ThinkingLevelLow},
		{"MEDIUM", genai.ThinkingLevelMedium},
		{" high ", genai.ThinkingLevelHigh},
		{"minimal", genai.ThinkingLevelMinimal},
		{"xhigh", genai.ThinkingLevel("XHIGH")},
	}
	for _, tc := range cases {
		req := &llm.Request{
			Model:    "gemini-4-neon",
			Thinking: &llm.ThinkingConfig{Enabled: true, Effort: tc.effort},
			Messages: message.MessageList{message.NewUser(&message.TextBlock{Text: "hi"})},
		}
		_, config, err := toGenerateContentArgs(req)
		if err != nil {
			t.Fatalf("toGenerateContentArgs: %v", err)
		}
		if config.ThinkingConfig == nil {
			t.Fatalf("expected ThinkingConfig for effort %q", tc.effort)
		}
		if config.ThinkingConfig.ThinkingLevel != tc.want {
			t.Errorf("effort %q: expected ThinkingLevel %q, got %q", tc.effort, tc.want, config.ThinkingConfig.ThinkingLevel)
		}
		if !config.ThinkingConfig.IncludeThoughts {
			t.Errorf("effort %q: expected IncludeThoughts true", tc.effort)
		}
	}
}

func TestToGenerateContentArgs_GroupsConsecutiveToolResults(t *testing.T) {
	req := &llm.Request{
		Model: "gemini-2.5-flash",
		Messages: message.MessageList{
			&message.User{
				Content: message.Content{&message.TextBlock{Text: "Fetch info"}},
			},
			&message.Assistant{
				Content: message.Content{
					&message.ToolCall{ID: "call_1", Name: "tool_1"},
					&message.ToolCall{ID: "call_2", Name: "tool_2"},
				},
			},
			&message.Tool{
				ToolCallID: "call_1",
				Name:       "tool_1",
				Content:    message.Content{&message.TextBlock{Text: "result 1"}},
			},
			&message.Tool{
				ToolCallID: "call_2",
				Name:       "tool_2",
				Content:    message.Content{&message.TextBlock{Text: "result 2"}},
			},
			&message.User{
				Content: message.Content{&message.TextBlock{Text: "Thank you"}},
			},
		},
	}

	contents, _, err := toGenerateContentArgs(req)
	if err != nil {
		t.Fatalf("toGenerateContentArgs failed: %v", err)
	}

	// Should produce 4 Content turns:
	// 0: user (Text)
	// 1: model (2 FunctionCalls)
	// 2: user (2 FunctionResponses grouped into single turn)
	// 3: user (Text)
	if len(contents) != 4 {
		t.Fatalf("expected 4 contents, got %d", len(contents))
	}

	toolTurn := contents[2]
	if toolTurn.Role != "user" {
		t.Errorf("expected role user, got %q", toolTurn.Role)
	}
	if len(toolTurn.Parts) != 2 {
		t.Fatalf("expected 2 parts in tool turn, got %d", len(toolTurn.Parts))
	}
	if toolTurn.Parts[0].FunctionResponse == nil || toolTurn.Parts[0].FunctionResponse.ID != "call_1" {
		t.Errorf("expected first part to be FunctionResponse for call_1, got %#v", toolTurn.Parts[0])
	}
	if toolTurn.Parts[1].FunctionResponse == nil || toolTurn.Parts[1].FunctionResponse.ID != "call_2" {
		t.Errorf("expected second part to be FunctionResponse for call_2, got %#v", toolTurn.Parts[1])
	}
}

func TestToGenerateContentArgs_DeferredToolsFailFast(t *testing.T) {
	// 1. Tool with DeferLoading: true
	reqDeferred := &llm.Request{
		Tools: []tool.Definition{
			{Name: "search_tool", DeferLoading: true},
		},
	}
	_, _, err := toGenerateContentArgs(reqDeferred)
	if err == nil {
		t.Errorf("expected error for deferred tool, got nil")
	}

	// 2. Message with ToolReferenceBlock
	reqRef := &llm.Request{
		Messages: []message.Message{
			message.NewUser(&message.ToolReferenceBlock{ToolName: "search_tool"}),
		},
	}
	_, _, err = toGenerateContentArgs(reqRef)
	if err == nil {
		t.Errorf("expected error for ToolReferenceBlock, got nil")
	}

	// 3. Message with nested ToolReferenceBlock in ServerToolResult
	reqNested := &llm.Request{
		Messages: []message.Message{
			&message.Assistant{
				Content: message.Content{
					&message.ServerToolResult{
						ToolCallID: "srv-1",
						Content: message.Content{
							&message.ToolReferenceBlock{ToolName: "search_tool"},
						},
					},
				},
			},
		},
	}
	_, _, err = toGenerateContentArgs(reqNested)
	if err == nil {
		t.Errorf("expected error for nested ToolReferenceBlock, got nil")
	}

	// 4. Message with ToolDefinitionBlock
	reqDef := &llm.Request{
		Messages: []message.Message{
			message.NewUser(&message.ToolDefinitionBlock{Name: "custom_tool"}),
		},
	}
	_, _, err = toGenerateContentArgs(reqDef)
	if err == nil {
		t.Errorf("expected error for ToolDefinitionBlock, got nil")
	}
}

func TestToGenaiTools_DeferredToolFailFast(t *testing.T) {
	_, err := toGenaiTools([]tool.Definition{{Name: "deferred_tool", DeferLoading: true}})
	if err == nil {
		t.Errorf("expected error for deferred tool in toGenaiTools, got nil")
	}
}

func TestToAssistantChunk_TrailingSignatureOnlyPartRoundTrip(t *testing.T) {
	sigBytes := []byte("sig-abc")

	c1, err := toAssistantChunk(&genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Parts: []*genai.Part{{Text: "Hello"}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("chunk 1 failed: %v", err)
	}

	c2, err := toAssistantChunk(&genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Parts: []*genai.Part{{Text: "", ThoughtSignature: sigBytes}},
			},
			FinishReason: genai.FinishReasonStop,
		}},
	})
	if err != nil {
		t.Fatalf("chunk 2 failed: %v", err)
	}

	agg := message.NewAssistantAggregator()
	agg.Add(&c1)
	agg.Add(&c2)
	asst, err := agg.Build()
	if err != nil {
		t.Fatalf("Aggregator.Build failed: %v", err)
	}

	parts, err := toModelParts(asst.GetContent())
	if err != nil {
		t.Fatalf("toModelParts failed: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0].Text != "Hello" || parts[0].Thought || !reflect.DeepEqual(parts[0].ThoughtSignature, sigBytes) {
		t.Errorf("expected Part{Text: \"Hello\", ThoughtSignature: %q}, got %+v", sigBytes, parts[0])
	}
}

func TestToGenerateContentArgs_WireModalityValidation(t *testing.T) {
	mediaBlocks := []struct {
		name     string
		block    message.Block
		empty    message.Block
		modality profile.Modality
	}{
		{
			name:     "image",
			block:    &message.ImageBlock{Data: []byte("img"), MIMEType: "image/png"},
			empty:    &message.ImageBlock{MIMEType: "image/png"},
			modality: profile.ModalityImage,
		},
		{
			name:     "audio",
			block:    &message.AudioBlock{Data: []byte("aud"), MIMEType: "audio/mp3"},
			empty:    &message.AudioBlock{MIMEType: "audio/mp3"},
			modality: profile.ModalityAudio,
		},
		{
			name:     "video",
			block:    &message.VideoBlock{Data: []byte("vid"), MIMEType: "video/mp4"},
			empty:    &message.VideoBlock{MIMEType: "video/mp4"},
			modality: profile.ModalityVideo,
		},
		{
			name:     "document",
			block:    &message.DocumentBlock{Data: []byte("pdf"), MIMEType: "application/pdf"},
			empty:    &message.DocumentBlock{MIMEType: "application/pdf"},
			modality: profile.ModalityPDF,
		},
	}

	for _, tc := range mediaBlocks {
		t.Run("system_rejects_"+tc.name, func(t *testing.T) {
			req := &llm.Request{
				Model: "gemini-2.5-flash",
				Messages: message.MessageList{
					&message.System{Content: message.Content{
						&message.TextBlock{Text: "system prompt"},
						tc.block,
					}},
				},
			}
			_, _, err := toGenerateContentArgs(req)
			if !errors.Is(err, profile.ErrUnsupportedModality) {
				t.Fatalf("expected ErrUnsupportedModality for %s in system message, got %v", tc.name, err)
			}
		})

		t.Run("assistant_rejects_"+tc.name, func(t *testing.T) {
			req := &llm.Request{
				Model: "gemini-2.5-flash",
				Messages: message.MessageList{
					&message.Assistant{Content: message.Content{
						&message.TextBlock{Text: "reply"},
						tc.block,
					}},
				},
			}
			_, _, err := toGenerateContentArgs(req)
			if !errors.Is(err, profile.ErrUnsupportedModality) {
				t.Fatalf("expected ErrUnsupportedModality for %s in assistant message, got %v", tc.name, err)
			}
		})

		t.Run("user_rejects_empty_"+tc.name, func(t *testing.T) {
			req := &llm.Request{
				Model: "gemini-2.5-flash",
				Messages: message.MessageList{
					&message.User{Content: message.Content{
						&message.TextBlock{Text: "hello"},
						tc.empty,
					}},
				},
			}
			_, _, err := toGenerateContentArgs(req)
			if err == nil {
				t.Fatalf("expected error for empty %s block in user message, got nil", tc.name)
			}
		})

		t.Run("tool_rejects_empty_"+tc.name, func(t *testing.T) {
			req := &llm.Request{
				Model: "gemini-2.5-flash",
				Messages: message.MessageList{
					&message.Tool{
						ToolCallID: "call_1",
						Name:       "my_tool",
						Content: message.Content{
							&message.TextBlock{Text: "ok"},
							tc.empty,
						},
					},
				},
			}
			_, _, err := toGenerateContentArgs(req)
			if err == nil {
				t.Fatalf("expected error for empty %s block in tool message, got nil", tc.name)
			}
		})
	}

	t.Run("system_rejects_unknown_block", func(t *testing.T) {
		req := &llm.Request{
			Messages: message.MessageList{
				&message.System{Content: message.Content{&message.ToolCall{ID: "c1", Name: "t1"}}},
			},
		}
		if _, _, err := toGenerateContentArgs(req); err == nil {
			t.Fatal("expected error for ToolCall block in system message")
		}
	})

	t.Run("tool_rejects_unknown_block", func(t *testing.T) {
		req := &llm.Request{
			Messages: message.MessageList{
				&message.Tool{
					ToolCallID: "call_1",
					Name:       "my_tool",
					Content:    message.Content{&message.ToolCall{ID: "c1", Name: "t1"}},
				},
			},
		}
		if _, _, err := toGenerateContentArgs(req); err == nil {
			t.Fatal("expected error for ToolCall block in tool message")
		}
	})

	t.Run("tool_supports_all_valid_modalities", func(t *testing.T) {
		req := &llm.Request{
			Model: "gemini-2.5-flash",
			Messages: message.MessageList{
				&message.Tool{
					ToolCallID: "call_1",
					Name:       "multi_tool",
					Content: message.Content{
						&message.TextBlock{Text: "done"},
						&message.ImageBlock{Data: []byte("img"), MIMEType: "image/png"},
						&message.AudioBlock{URL: "gs://bucket/a.mp3", MIMEType: "audio/mp3"},
						&message.VideoBlock{Data: []byte("vid"), MIMEType: "video/mp4"},
						&message.DocumentBlock{URL: "gs://bucket/d.pdf", MIMEType: "application/pdf"},
					},
				},
			},
		}
		contents, _, err := toGenerateContentArgs(req)
		if err != nil {
			t.Fatalf("unexpected error for multimodal tool message: %v", err)
		}
		if len(contents) != 1 || len(contents[0].Parts) != 1 || contents[0].Parts[0].FunctionResponse == nil {
			t.Fatalf("unexpected contents: %+v", contents)
		}
		if got := len(contents[0].Parts[0].FunctionResponse.Parts); got != 4 {
			t.Fatalf("expected 4 multimodal FunctionResponse parts, got %d", got)
		}
	})
}

func TestProfileValidation_StreamAndCreateCache(t *testing.T) {
	newOKProvider := func(t *testing.T, called *bool) *Provider {
		t.Helper()
		return newTestGenAIProvider(t, func(w http.ResponseWriter, r *http.Request) {
			if called != nil {
				*called = true
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"cachedContents/test-cache","candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
		})
	}

	t.Run("text_only_model_rejects_image_and_audio", func(t *testing.T) {
		var called bool
		p := newOKProvider(t, &called)

		for _, blk := range []message.Block{
			&message.ImageBlock{Data: []byte("img"), MIMEType: "image/png"},
			&message.AudioBlock{Data: []byte("aud"), MIMEType: "audio/mp3"},
		} {
			req := &llm.Request{
				Model:    "gemini-2.5-flash-preview-tts",
				Messages: message.MessageList{message.NewUser(blk)},
			}
			if _, err := p.Stream(context.Background(), req); !errors.Is(err, profile.ErrUnsupportedModality) {
				t.Fatalf("Stream(%T): expected ErrUnsupportedModality, got %v", blk, err)
			}
			if _, err := p.CreateCache(context.Background(), req); !errors.Is(err, profile.ErrUnsupportedModality) {
				t.Fatalf("CreateCache(%T): expected ErrUnsupportedModality, got %v", blk, err)
			}
		}
		if called {
			t.Fatal("expected HTTP transport not to be called on validation failure")
		}
	})

	t.Run("non_tool_model_rejects_tools", func(t *testing.T) {
		var called bool
		p := newOKProvider(t, &called)

		req := &llm.Request{
			Model:    "gemini-2.5-flash-preview-tts",
			Messages: message.MessageList{message.NewUser(&message.TextBlock{Text: "hello"})},
			Tools:    []tool.Definition{{Name: "weather", Description: "get weather"}},
		}
		if _, err := p.Stream(context.Background(), req); !errors.Is(err, profile.ErrUnsupportedCapability) {
			t.Fatalf("Stream: expected ErrUnsupportedCapability, got %v", err)
		}
		if _, err := p.CreateCache(context.Background(), req); !errors.Is(err, profile.ErrUnsupportedCapability) {
			t.Fatalf("CreateCache: expected ErrUnsupportedCapability, got %v", err)
		}
		if called {
			t.Fatal("expected HTTP transport not to be called on capability failure")
		}
	})

	t.Run("embedding_model_rejected_by_stream_and_cache", func(t *testing.T) {
		p := newOKProvider(t, nil)
		req := &llm.Request{
			Model:    "gemini-embedding-001",
			Messages: message.MessageList{message.NewUser(&message.TextBlock{Text: "hello"})},
		}
		if _, err := p.Stream(context.Background(), req); !errors.Is(err, profile.ErrUnsupportedCapability) {
			t.Fatalf("Stream: expected ErrUnsupportedCapability, got %v", err)
		}
		if _, err := p.CreateCache(context.Background(), req); !errors.Is(err, profile.ErrUnsupportedCapability) {
			t.Fatalf("CreateCache: expected ErrUnsupportedCapability, got %v", err)
		}
	})

	t.Run("vision_only_gemma_allows_image_and_tools_but_rejects_audio_and_pdf", func(t *testing.T) {
		var called bool
		p := newOKProvider(t, &called)

		okReq := &llm.Request{
			Model: "gemma-4-26b-a4b-it",
			Messages: message.MessageList{
				message.NewUser(
					&message.TextBlock{Text: "describe"},
					&message.ImageBlock{Data: []byte("img"), MIMEType: "image/png"},
				),
			},
			Tools: []tool.Definition{{Name: "tag"}},
		}
		if _, err := p.Stream(context.Background(), okReq); err != nil {
			t.Fatalf("Stream with supported image+tools failed: %v", err)
		}

		for _, blk := range []message.Block{
			&message.AudioBlock{Data: []byte("aud"), MIMEType: "audio/mp3"},
			&message.DocumentBlock{Data: []byte("pdf"), MIMEType: "application/pdf"},
		} {
			badReq := &llm.Request{
				Model:    "gemma-4-26b-a4b-it",
				Messages: message.MessageList{message.NewUser(blk)},
			}
			if _, err := p.Stream(context.Background(), badReq); !errors.Is(err, profile.ErrUnsupportedModality) {
				t.Fatalf("Stream(%T): expected ErrUnsupportedModality, got %v", blk, err)
			}
		}
	})

	t.Run("unknown_model_passes_through_wire_supported_modalities", func(t *testing.T) {
		var called bool
		p := newOKProvider(t, &called)

		req := &llm.Request{
			Model: "gemini-99-experimental",
			Messages: message.MessageList{
				message.NewUser(
					&message.TextBlock{Text: "analyze"},
					&message.ImageBlock{Data: []byte("img"), MIMEType: "image/png"},
					&message.AudioBlock{Data: []byte("aud"), MIMEType: "audio/mp3"},
					&message.VideoBlock{Data: []byte("vid"), MIMEType: "video/mp4"},
					&message.DocumentBlock{Data: []byte("pdf"), MIMEType: "application/pdf"},
				),
			},
			Tools: []tool.Definition{{Name: "lookup"}},
		}
		if _, err := p.Stream(context.Background(), req); err != nil {
			t.Fatalf("expected unknown model to pass through, got %v", err)
		}

		// Wire-level checks still apply for unknown models.
		badSystem := &llm.Request{
			Model: "gemini-99-experimental",
			Messages: message.MessageList{
				&message.System{Content: message.Content{&message.ImageBlock{Data: []byte("img"), MIMEType: "image/png"}}},
			},
		}
		if _, err := p.Stream(context.Background(), badSystem); !errors.Is(err, profile.ErrUnsupportedModality) {
			t.Fatalf("expected wire ErrUnsupportedModality for unknown model system image, got %v", err)
		}
	})
}
