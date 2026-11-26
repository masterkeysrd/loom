package loomopenai

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/tool"
	"github.com/openai/openai-go/v3"
)

func TestToAssistantMessageParamToolCallsUseNullContent(t *testing.T) {
	msg := &message.Assistant{
		Content: message.Content{
			&message.ToolCall{
				ID:   "call_123",
				Name: "lookup",
				Args: map[string]any{"query": "loom"},
			},
		},
	}

	param, err := toAssistantMessageParam(msg)
	if err != nil {
		t.Fatalf("toAssistantMessageParam failed: %v", err)
	}
	data, err := json.Marshal(param)
	if err != nil {
		t.Fatalf("marshal assistant message: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal assistant message: %v", err)
	}

	if _, ok := got["content"]; !ok {
		t.Fatalf("expected explicit content field, got JSON %s", string(data))
	}
	if got["content"] != nil {
		t.Fatalf("expected content to be null, got %#v", got["content"])
	}

	toolCalls, ok := got["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", got["tool_calls"])
	}
}

func TestToAssistantMessageParamPreservesTextWithToolCalls(t *testing.T) {
	msg := &message.Assistant{
		Content: message.Content{
			&message.TextBlock{Text: "I will call a tool."},
			&message.ToolCall{
				ID:   "call_123",
				Name: "lookup",
				Args: map[string]any{"query": "loom"},
			},
		},
	}

	param, err := toAssistantMessageParam(msg)
	if err != nil {
		t.Fatalf("toAssistantMessageParam failed: %v", err)
	}
	data, err := json.Marshal(param)
	if err != nil {
		t.Fatalf("marshal assistant message: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal assistant message: %v", err)
	}

	if got["content"] != "I will call a tool." {
		t.Fatalf("expected text content, got %#v", got["content"])
	}
}

func TestToUserMessageParamMultimodal(t *testing.T) {
	msg := &message.User{
		Content: message.Content{
			&message.TextBlock{Text: "Analyze this image."},
			&message.ImageBlock{
				Data:     []byte("fake-image-data"),
				MIMEType: "image/png",
			},
		},
	}

	param, err := toUserMessageParam(msg)
	if err != nil {
		t.Fatalf("toUserMessageParam failed: %v", err)
	}

	data, err := json.Marshal(param)
	if err != nil {
		t.Fatalf("marshal user message: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal user message: %v", err)
	}

	content, ok := got["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("expected 2 content parts, got %#v", got["content"])
	}

	if content[0].(map[string]any)["type"] != "text" {
		t.Errorf("expected first part to be text, got %#v", content[0])
	}
	if content[1].(map[string]any)["type"] != "image_url" {
		t.Errorf("expected second part to be image_url, got %#v", content[1])
	}
}

func TestToUserMessageParamDocument(t *testing.T) {
	msg := &message.User{
		Content: message.Content{
			&message.DocumentBlock{
				URL: "file-123",
			},
		},
	}

	param, err := toUserMessageParam(msg)
	if err != nil {
		t.Fatalf("toUserMessageParam failed: %v", err)
	}

	data, err := json.Marshal(param)
	if err != nil {
		t.Fatalf("marshal user message: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal user message: %v", err)
	}

	content, ok := got["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("expected 1 content part, got %#v", got["content"])
	}

	if content[0].(map[string]any)["type"] != "file" {
		t.Errorf("expected part to be file, got %#v", content[0])
	}
	file := content[0].(map[string]any)["file"].(map[string]any)
	if file["file_id"] != "file-123" {
		t.Errorf("expected file_id to be file-123, got %#v", file["file_id"])
	}
}

func TestToChatCompletionNewParamsStructuredOutput(t *testing.T) {
	schema, _ := jsonschema.For[map[string]string](nil)
	schema.Title = "TestSchema"
	schema.Description = "A test schema"

	req := &llm.Request{
		Model:          "gpt-4o",
		ResponseSchema: schema,
	}

	params, err := toChatCompletionNewParams(req)
	if err != nil {
		t.Fatalf("toChatCompletionNewParams failed: %v", err)
	}

	if params.ResponseFormat.OfJSONSchema == nil {
		t.Fatal("ResponseFormat.OfJSONSchema should be set")
	}

	jsonSchema := params.ResponseFormat.OfJSONSchema.JSONSchema
	if jsonSchema.Name != "TestSchema" {
		t.Errorf("expected Name to be TestSchema, got %q", jsonSchema.Name)
	}
	if !jsonSchema.Strict.Value {
		t.Error("expected Strict to be true")
	}
}

func TestToAssistantChunkPopulatesResponseID(t *testing.T) {
	chunk := &openai.ChatCompletionChunk{
		ID: "chatcmpl_abc123",
		Choices: []openai.ChatCompletionChunkChoice{
			{
				Delta: openai.ChatCompletionChunkChoiceDelta{
					Content: "Hello world",
				},
			},
		},
	}

	res, err := toAssistantChunk(chunk)
	if err != nil {
		t.Fatalf("toAssistantChunk failed: %v", err)
	}

	if res.ID != "chatcmpl_abc123" {
		t.Errorf("expected res.ID 'chatcmpl_abc123', got %q", res.ID)
	}
	if res.Metadata == nil || res.Metadata[ResponseIDKey] != "chatcmpl_abc123" {
		t.Errorf("expected res.Metadata[%q] == 'chatcmpl_abc123', got %v", ResponseIDKey, res.Metadata)
	}
}

func TestModalityValidation_ChatCompletionsWire(t *testing.T) {
	mediaBlocks := []struct {
		name  string
		block message.Block
	}{
		{"image", &message.ImageBlock{Data: []byte("img"), MIMEType: "image/png"}},
		{"audio", &message.AudioBlock{Data: []byte("aud"), MIMEType: "audio/wav"}},
		{"video", &message.VideoBlock{Data: []byte("vid"), MIMEType: "video/mp4"}},
		{"document", &message.DocumentBlock{Data: []byte("pdf"), MIMEType: "application/pdf"}},
	}

	for _, tc := range mediaBlocks {
		t.Run("system/"+tc.name, func(t *testing.T) {
			_, err := toChatCompletionNewParams(&llm.Request{
				Model: "custom-model",
				Messages: []message.Message{
					message.NewSystem(&message.TextBlock{Text: "sys"}, tc.block),
				},
			})
			if !errors.Is(err, profile.ErrUnsupportedModality) {
				t.Fatalf("expected ErrUnsupportedModality, got %v", err)
			}
		})

		t.Run("assistant/"+tc.name, func(t *testing.T) {
			_, err := toChatCompletionNewParams(&llm.Request{
				Model: "custom-model",
				Messages: []message.Message{
					&message.Assistant{Content: message.Content{&message.TextBlock{Text: "hi"}, tc.block}},
				},
			})
			if !errors.Is(err, profile.ErrUnsupportedModality) {
				t.Fatalf("expected ErrUnsupportedModality, got %v", err)
			}
		})

		t.Run("tool/"+tc.name, func(t *testing.T) {
			_, err := toChatCompletionNewParams(&llm.Request{
				Model: "custom-model",
				Messages: []message.Message{
					&message.Tool{
						ToolCallID: "call_1",
						Content:    message.Content{&message.TextBlock{Text: "ok"}, tc.block},
					},
				},
			})
			if !errors.Is(err, profile.ErrUnsupportedModality) {
				t.Fatalf("expected ErrUnsupportedModality, got %v", err)
			}
		})
	}

	t.Run("assistant_thinking_lenient", func(t *testing.T) {
		params, err := toChatCompletionNewParams(&llm.Request{
			Model: "custom-model",
			Messages: []message.Message{
				&message.Assistant{
					Content: message.Content{
						&message.ThinkingBlock{Thinking: "hmm"},
						&message.TextBlock{Text: "done"},
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("expected ThinkingBlock in assistant to remain lenient, got %v", err)
		}
		if len(params.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(params.Messages))
		}
	})

	userRejections := []struct {
		name  string
		block message.Block
	}{
		{"video", &message.VideoBlock{Data: []byte("vid"), MIMEType: "video/mp4"}},
		{"empty_image", &message.ImageBlock{}},
		{"empty_audio", &message.AudioBlock{}},
		{"audio_url_only", &message.AudioBlock{URL: "https://example.com/a.wav"}},
		{"empty_document", &message.DocumentBlock{}},
	}

	for _, tc := range userRejections {
		t.Run("user/"+tc.name, func(t *testing.T) {
			_, err := toUserMessageParam(&message.User{Content: message.Content{tc.block}})
			if !errors.Is(err, profile.ErrUnsupportedModality) {
				t.Fatalf("expected ErrUnsupportedModality, got %v", err)
			}
		})
	}

	t.Run("user/audio_inline_supported", func(t *testing.T) {
		param, err := toUserMessageParam(&message.User{
			Content: message.Content{
				&message.AudioBlock{Data: []byte("pcm-wav"), MIMEType: "audio/wav"},
			},
		})
		if err != nil {
			t.Fatalf("expected inline audio to succeed, got %v", err)
		}
		if param.OfUser == nil {
			t.Fatalf("expected OfUser message param")
		}
	})
}

func TestValidateChatProfileAndCapabilities(t *testing.T) {
	p := &Provider{}
	ctx := context.Background()

	p.OverrideProfile("test-text-only", profile.Profile{
		ID: "test-text-only",
		Capabilities: profile.Capabilities{
			Completion: true,
			ToolCall:   true,
		},
		Modalities: profile.Modalities{
			Inputs:  []profile.Modality{profile.ModalityText},
			Outputs: []profile.Modality{profile.ModalityText},
		},
	})
	p.OverrideProfile("test-vision-no-audio", profile.Profile{
		ID: "test-vision-no-audio",
		Capabilities: profile.Capabilities{
			Completion: true,
			ToolCall:   true,
		},
		Modalities: profile.Modalities{
			Inputs:  []profile.Modality{profile.ModalityText, profile.ModalityImage},
			Outputs: []profile.Modality{profile.ModalityText},
		},
	})
	p.OverrideProfile("test-no-toolcall", profile.Profile{
		ID: "test-no-toolcall",
		Capabilities: profile.Capabilities{
			Completion: true,
			ToolCall:   false,
		},
		Modalities: profile.Modalities{
			Inputs:  []profile.Modality{profile.ModalityText},
			Outputs: []profile.Modality{profile.ModalityText},
		},
	})
	p.OverrideProfile("test-embedding-only", profile.Profile{
		ID: "test-embedding-only",
		Capabilities: profile.Capabilities{
			Completion: false,
			Embedding:  true,
		},
		Modalities: profile.Modalities{
			Inputs:  []profile.Modality{profile.ModalityText},
			Outputs: []profile.Modality{profile.ModalityEmbedding},
		},
	})

	t.Run("known_text_only_rejects_image", func(t *testing.T) {
		_, err := p.Stream(ctx, &llm.Request{
			Model: "test-text-only",
			Messages: []message.Message{
				message.NewUser(&message.ImageBlock{URL: "https://example.com/a.png"}),
			},
		})
		if !errors.Is(err, profile.ErrUnsupportedModality) {
			t.Fatalf("expected ErrUnsupportedModality, got %v", err)
		}
	})

	t.Run("known_vision_model_rejects_audio", func(t *testing.T) {
		_, err := p.Stream(ctx, &llm.Request{
			Model: "test-vision-no-audio",
			Messages: []message.Message{
				message.NewUser(&message.AudioBlock{Data: []byte("aud"), MIMEType: "audio/wav"}),
			},
		})
		if !errors.Is(err, profile.ErrUnsupportedModality) {
			t.Fatalf("expected ErrUnsupportedModality, got %v", err)
		}
	})

	t.Run("known_no_toolcall_rejects_tools", func(t *testing.T) {
		_, err := p.Stream(ctx, &llm.Request{
			Model:    "test-no-toolcall",
			Messages: []message.Message{message.NewUserText("hi")},
			Tools:    []tool.Definition{{Name: "ping"}},
		})
		if !errors.Is(err, profile.ErrUnsupportedCapability) {
			t.Fatalf("expected ErrUnsupportedCapability, got %v", err)
		}
	})

	t.Run("known_embedding_only_rejects_completion", func(t *testing.T) {
		_, err := p.Stream(ctx, &llm.Request{
			Model:    "test-embedding-only",
			Messages: []message.Message{message.NewUserText("hi")},
		})
		if !errors.Is(err, profile.ErrUnsupportedCapability) {
			t.Fatalf("expected ErrUnsupportedCapability, got %v", err)
		}
	})

	t.Run("unknown_model_passthrough_supported_wire_modalities", func(t *testing.T) {
		streamResp, err := p.Stream(ctx, &llm.Request{
			Model: "custom-unlisted-model",
			Messages: []message.Message{
				message.NewUser(
					&message.TextBlock{Text: "describe"},
					&message.ImageBlock{Data: []byte("img"), MIMEType: "image/png"},
					&message.AudioBlock{Data: []byte("aud"), MIMEType: "audio/mp3"},
					&message.DocumentBlock{URL: "file-123"},
				),
			},
			Tools: []tool.Definition{{Name: "ping"}},
		})
		if err != nil {
			t.Fatalf("expected unknown model with valid wire modalities to pass validation, got %v", err)
		}
		if streamResp == nil {
			t.Fatal("expected non-nil stream response")
		}
	})

	t.Run("unknown_model_still_rejects_unsupported_wire_modality", func(t *testing.T) {
		_, err := p.Stream(ctx, &llm.Request{
			Model: "custom-unlisted-model",
			Messages: []message.Message{
				message.NewUser(&message.VideoBlock{Data: []byte("vid"), MIMEType: "video/mp4"}),
			},
		})
		if !errors.Is(err, profile.ErrUnsupportedModality) {
			t.Fatalf("expected ErrUnsupportedModality from wire validation, got %v", err)
		}
	})
}
