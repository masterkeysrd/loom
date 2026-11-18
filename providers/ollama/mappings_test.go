package loomollama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/tool"
	"github.com/ollama/ollama/api"
)

func TestToChatRequestStructuredOutput(t *testing.T) {
	schema, _ := jsonschema.For[map[string]string](nil)

	req := &llm.Request{
		Model:          "llama3",
		ResponseSchema: schema,
	}

	ollamaReq, err := toChatRequest(req)
	if err != nil {
		t.Fatalf("toChatRequest failed: %v", err)
	}

	if len(ollamaReq.Format) == 0 {
		t.Fatal("Format should be set")
	}

	var gotSchema jsonschema.Schema
	if err := json.Unmarshal(ollamaReq.Format, &gotSchema); err != nil {
		t.Fatalf("failed to unmarshal format as schema: %v", err)
	}
}

func TestToChatRequestJSONMode(t *testing.T) {
	req := &llm.Request{
		Model:          "llama3",
		ResponseFormat: "json_object",
	}

	ollamaReq, err := toChatRequest(req)
	if err != nil {
		t.Fatalf("toChatRequest failed: %v", err)
	}

	if string(ollamaReq.Format) != `"json"` {
		t.Errorf("expected Format to be \"json\", got %s", string(ollamaReq.Format))
	}
}

func TestToChatRequestMaxTokensAndContextWindow(t *testing.T) {
	req := &llm.Request{
		Model:         "llama3",
		MaxTokens:     100,
		ContextWindow: 8192,
	}

	ollamaReq, err := toChatRequest(req)
	if err != nil {
		t.Fatalf("toChatRequest failed: %v", err)
	}

	if val, ok := ollamaReq.Options["num_predict"].(int); !ok || val != 100 {
		t.Errorf("expected num_predict to be 100, got %v", ollamaReq.Options["num_predict"])
	}

	if val, ok := ollamaReq.Options["num_ctx"].(int); !ok || val != 8192 {
		t.Errorf("expected num_ctx to be 8192, got %v", ollamaReq.Options["num_ctx"])
	}
}

func TestToChatRequestThinkingToggle(t *testing.T) {
	// Test thinking enabled
	reqEnabled := &llm.Request{
		Model: "llama3",
		Thinking: &llm.ThinkingConfig{
			Enabled: true,
		},
	}
	ollamaReqEnabled, err := toChatRequest(reqEnabled)
	if err != nil {
		t.Fatalf("toChatRequest failed: %v", err)
	}
	if ollamaReqEnabled.Think == nil || ollamaReqEnabled.Think.Value != true {
		t.Errorf("expected Think to be true, got %v", ollamaReqEnabled.Think)
	}

	// Test thinking disabled
	reqDisabled := &llm.Request{
		Model: "llama3",
		Thinking: &llm.ThinkingConfig{
			Enabled: false,
		},
	}
	ollamaReqDisabled, err := toChatRequest(reqDisabled)
	if err != nil {
		t.Fatalf("toChatRequest failed: %v", err)
	}
	if ollamaReqDisabled.Think == nil || ollamaReqDisabled.Think.Value != false {
		t.Errorf("expected Think to be false, got %v", ollamaReqDisabled.Think)
	}
}

func TestToAPIToolCall_DeterministicArgumentOrder(t *testing.T) {
	tc := &message.ToolCall{
		ID:   "call_1",
		Name: "test_fn",
		Args: map[string]any{
			"alpha":   1,
			"beta":    2,
			"gamma":   3,
			"delta":   4,
			"epsilon": 5,
			"zeta":    6,
			"eta":     7,
			"theta":   8,
			"iota":    9,
			"kappa":   10,
		},
	}

	firstBytes, err := json.Marshal(toAPIToolCall(tc).Function.Arguments)
	if err != nil {
		t.Fatalf("failed to marshal first arguments: %v", err)
	}

	for i := range 50 {
		iterBytes, err := json.Marshal(toAPIToolCall(tc).Function.Arguments)
		if err != nil {
			t.Fatalf("failed to marshal iter arguments: %v", err)
		}
		if string(firstBytes) != string(iterBytes) {
			t.Fatalf("expected deterministic argument ordering across iterations, but iteration %d differed:\nFirst: %s\nIter:  %s", i, string(firstBytes), string(iterBytes))
		}
	}
}

func TestToChatRequest_DeferredToolsFailFast(t *testing.T) {
	// 1. Tool with DeferLoading: true
	reqDeferred := &llm.Request{
		Model: "llama3",
		Tools: []tool.Definition{
			{Name: "search_tool", DeferLoading: true},
		},
	}
	_, err := toChatRequest(reqDeferred)
	if err == nil {
		t.Errorf("expected error for deferred tool, got nil")
	}

	// 2. Message with ToolReferenceBlock
	reqRef := &llm.Request{
		Model: "llama3",
		Messages: []message.Message{
			message.NewUser(&message.ToolReferenceBlock{ToolName: "search_tool"}),
		},
	}
	_, err = toChatRequest(reqRef)
	if err == nil {
		t.Errorf("expected error for ToolReferenceBlock, got nil")
	}

	// 3. Message with nested ToolReferenceBlock in ServerToolResult
	reqNested := &llm.Request{
		Model: "llama3",
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
	_, err = toChatRequest(reqNested)
	if err == nil {
		t.Errorf("expected error for nested ToolReferenceBlock in ServerToolResult, got nil")
	}

	// 4. Message with ToolDefinitionBlock
	reqDef := &llm.Request{
		Model: "llama3",
		Messages: []message.Message{
			message.NewUser(&message.ToolDefinitionBlock{Name: "custom_tool"}),
		},
	}
	_, err = toChatRequest(reqDef)
	if err == nil {
		t.Errorf("expected error for ToolDefinitionBlock, got nil")
	}
}

func TestToAPITool_DeferredToolFailFast(t *testing.T) {
	_, err := toAPITool(tool.Definition{Name: "deferred_tool", DeferLoading: true})
	if err == nil {
		t.Errorf("expected error for deferred tool in toAPITool, got nil")
	}
}

func TestOllamaChatModalityAndCapabilityValidation(t *testing.T) {
	t.Run("wire rejects audio video document empty-image url-image and non-user image", func(t *testing.T) {
		badMsgs := []message.Message{
			message.NewUser(&message.ImageBlock{}),
			message.NewUser(&message.ImageBlock{URL: "https://example.com/a.png"}),
			&message.System{Content: message.Content{&message.ImageBlock{Data: []byte("png")}}},
			&message.Assistant{Content: message.Content{&message.ImageBlock{Data: []byte("png")}}},
			&message.Tool{ToolCallID: "1", Name: "t", Content: message.Content{&message.ImageBlock{Data: []byte("png")}}},
			message.NewUser(&message.AudioBlock{Data: []byte("wav")}),
			message.NewUser(&message.VideoBlock{Data: []byte("mp4")}),
			message.NewUser(&message.DocumentBlock{Data: []byte("pdf")}),
		}

		for i, msg := range badMsgs {
			_, err := toChatRequest(&llm.Request{
				Model:    "gemma3",
				Messages: []message.Message{msg},
			})
			if !errors.Is(err, profile.ErrUnsupportedModality) {
				t.Fatalf("case %d: expected ErrUnsupportedModality, got: %v", i, err)
			}
		}
	})

	t.Run("stream enforces authoritative /api/show capabilities while allowing legacy no-caps servers", func(t *testing.T) {
		p := newTestOllamaProvider(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/show":
				var req api.ShowRequest
				_ = json.NewDecoder(r.Body).Decode(&req)
				switch req.Model {
				case "text-no-tools":
					_ = json.NewEncoder(w).Encode(map[string]any{
						"details":      map[string]any{"family": "llama"},
						"capabilities": []string{"completion"},
					})
				case "legacy-model":
					// Older Ollama server omitting capabilities slice
					_ = json.NewEncoder(w).Encode(map[string]any{
						"details": map[string]any{"family": "llava"},
					})
				default:
					http.NotFound(w, r)
				}
			case "/api/chat":
				_ = json.NewEncoder(w).Encode(api.ChatResponse{
					Model:   "legacy-model",
					Message: api.Message{Role: "assistant", Content: "ok"},
					Done:    true,
				})
			default:
				http.NotFound(w, r)
			}
		})

		// 1. Authoritative text-only model rejects ImageBlock
		_, err := p.Stream(context.Background(), &llm.Request{
			Model:    "text-no-tools",
			Messages: []message.Message{message.NewUser(&message.ImageBlock{Data: []byte("png")})},
		})
		if !errors.Is(err, profile.ErrUnsupportedModality) {
			t.Fatalf("expected ErrUnsupportedModality for text-no-tools with image, got: %v", err)
		}

		// 2. Authoritative non-tools model rejects Tools
		_, err = p.Stream(context.Background(), &llm.Request{
			Model:    "text-no-tools",
			Messages: []message.Message{message.NewUserText("hi")},
			Tools:    []tool.Definition{{Name: "my_tool"}},
		})
		if !errors.Is(err, profile.ErrUnsupportedCapability) {
			t.Fatalf("expected ErrUnsupportedCapability for text-no-tools with tools, got: %v", err)
		}

		// 3. Legacy server with empty capabilities slice is NOT falsely blocked
		stream, err := p.Stream(context.Background(), &llm.Request{
			Model:    "legacy-model",
			Messages: []message.Message{message.NewUser(&message.ImageBlock{Data: []byte("png")})},
		})
		if err != nil {
			t.Fatalf("expected legacy model without capabilities array to pass through, got: %v", err)
		}
		for _, sErr := range stream {
			if sErr != nil {
				t.Fatalf("unexpected stream error: %v", sErr)
			}
		}
	})
}
