package loomopenai

import (
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/tool"
)

func TestShouldUseResponsesAPI(t *testing.T) {
	defaultProv := &Provider{}
	responsesProv := &Provider{defaultAPIMode: APIModeResponses}

	// 1. Default without extensions should be false
	req := &llm.Request{Model: "gpt-4o"}
	use, _ := defaultProv.shouldUseResponsesAPI(req)
	if use {
		t.Errorf("expected default provider to not use responses API")
	}

	// 2. Provider configured with APIModeResponses should be true
	use, _ = responsesProv.shouldUseResponsesAPI(req)
	if !use {
		t.Errorf("expected responses provider to use responses API")
	}

	// 3. Auto-routing: WebSearch: true
	reqWeb := &llm.Request{
		Model: "gpt-4o",
		Extensions: map[string]llm.Extension{
			ResponsesExtensionID: ResponsesOptions{WebSearch: true},
		},
	}
	use, opts := defaultProv.shouldUseResponsesAPI(reqWeb)
	if !use || !opts.WebSearch {
		t.Errorf("expected auto-routing to responses API when WebSearch is true")
	}

	// 4. Auto-routing: FileSearch != nil
	reqFile := &llm.Request{
		Model: "gpt-4o",
		Extensions: map[string]llm.Extension{
			ResponsesExtensionID: ResponsesOptions{
				FileSearch: &FileSearchConfig{VectorStoreIDs: []string{"vs_123"}},
			},
		},
	}
	use, opts = defaultProv.shouldUseResponsesAPI(reqFile)
	if !use || opts.FileSearch == nil {
		t.Errorf("expected auto-routing to responses API when FileSearch is set")
	}

	// 5. Auto-routing: UsePreviousResponseID: true
	reqPrev := &llm.Request{
		Model: "gpt-4o",
		Extensions: map[string]llm.Extension{
			ResponsesExtensionID: ResponsesOptions{UsePreviousResponseID: true},
		},
	}
	use, opts = defaultProv.shouldUseResponsesAPI(reqPrev)
	if !use || !opts.UsePreviousResponseID {
		t.Errorf("expected auto-routing to responses API when UsePreviousResponseID is true")
	}

	// 6. Explicit enabled flag
	reqExplicit := &llm.Request{
		Model: "gpt-4o",
		Extensions: map[string]llm.Extension{
			ResponsesExtensionID: ResponsesOptions{Enabled: true},
		},
	}
	use, _ = defaultProv.shouldUseResponsesAPI(reqExplicit)
	if !use {
		t.Errorf("expected responses API when explicitly enabled")
	}

	// 7. Model-based auto-routing: gpt-5-pro, gpt-5.6-sol, codex, deep-research, computer-use
	for _, m := range []string{
		"gpt-5-pro-preview",
		"gpt-5.4-pro",
		"gpt-5.6-sol",
		"codex-davinci",
		"o3-deep-research",
		"o4-mini-deep-research",
		"computer-use-preview",
		"gpt-5.6-cyber",
		"gpt-rosalind-research",
	} {
		reqModel := &llm.Request{Model: m}
		use, _ := defaultProv.shouldUseResponsesAPI(reqModel)
		if !use {
			t.Errorf("expected model %q to auto-route to Responses API", m)
		}
	}

	// 8. Model-based auto-routing: gpt-6 with tools
	reqGpt6NoTools := &llm.Request{Model: "gpt-6-astra"}
	use, _ = defaultProv.shouldUseResponsesAPI(reqGpt6NoTools)
	if use {
		t.Errorf("expected gpt-6 without tools to default to chat completions")
	}

	reqGpt6WithTools := &llm.Request{
		Model: "gpt-6-astra",
		Tools: []tool.Definition{{Name: "test_tool"}},
	}
	use, _ = defaultProv.shouldUseResponsesAPI(reqGpt6WithTools)
	if !use {
		t.Errorf("expected gpt-6 with tools to auto-route to Responses API")
	}

	// 9. Explicit provider-level APIModeChatCompletions overrides model preference
	chatOnlyProv := &Provider{defaultAPIMode: APIModeChatCompletions}
	use, _ = chatOnlyProv.shouldUseResponsesAPI(&llm.Request{Model: "gpt-5.4-pro"})
	if use {
		t.Errorf("expected APIModeChatCompletions to override model preference")
	}
}

func TestToResponseNewParamsBasic(t *testing.T) {
	temp := float32(0.7)
	topP := float32(0.9)
	req := &llm.Request{
		Model:       "gpt-4o",
		MaxTokens:   500,
		Temperature: &temp,
		TopP:        &topP,
		Thinking: &llm.ThinkingConfig{
			Effort: "medium",
		},
		Messages: []message.Message{
			message.NewSystemText("You are an expert tutor."),
			message.NewUserText("Explain quantum computing."),
		},
		Tools: []tool.Definition{
			{
				Name:        "get_weather",
				Description: "Get the current weather",
			},
		},
	}

	opts := ResponsesOptions{
		WebSearch: true,
	}

	params, err := toResponseNewParams(req, opts)
	if err != nil {
		t.Fatalf("toResponseNewParams failed: %v", err)
	}

	// Verify model
	if params.Model != "gpt-4o" {
		t.Errorf("expected model 'gpt-4o', got %v", params.Model)
	}

	// Verify instructions
	if params.Instructions.Value != "You are an expert tutor." {
		t.Errorf("expected instructions 'You are an expert tutor.', got %v", params.Instructions.Value)
	}

	// Verify temperature, topP, maxTokens
	if params.MaxOutputTokens.Value != 500 {
		t.Errorf("expected max output tokens 500, got %d", params.MaxOutputTokens.Value)
	}
	if params.Temperature.Value != float64(temp) {
		t.Errorf("expected temperature %v, got %v", temp, params.Temperature.Value)
	}
	if params.TopP.Value != float64(topP) {
		t.Errorf("expected topP %v, got %v", topP, params.TopP.Value)
	}

	// Verify reasoning effort
	if params.Reasoning.Effort != "medium" {
		t.Errorf("expected reasoning effort 'medium', got %v", params.Reasoning.Effort)
	}

	// Verify tools: WebSearch + get_weather
	if len(params.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(params.Tools))
	}

	// Verify input has only the User message (System message moved to Instructions)
	inputList := params.Input.OfInputItemList
	if len(inputList) != 1 {
		t.Fatalf("expected 1 input item, got %d", len(inputList))
	}
}

func TestToResponseNewParamsStatePruning(t *testing.T) {
	// Simulate multi-turn conversation:
	// Turn 1: User: "Hi, I'm Bob."
	// Turn 1 Assistant: "Hello Bob!" (with ResponseIDKey in metadata)
	// Turn 2: User: "What is my name?"
	msgUser1 := message.NewUserText("Hi, I'm Bob.")
	msgAsst1 := message.NewAssistantText("Hello Bob!")
	msgAsst1.SetMetadata(map[string]any{
		ResponseIDKey: "resp_abc123",
	})
	msgUser2 := message.NewUserText("What is my name?")

	req := &llm.Request{
		Model: "gpt-4o",
		Messages: []message.Message{
			msgUser1,
			msgAsst1,
			msgUser2,
		},
	}

	opts := ResponsesOptions{
		UsePreviousResponseID: true,
	}

	params, err := toResponseNewParams(req, opts)
	if err != nil {
		t.Fatalf("toResponseNewParams failed: %v", err)
	}

	// Verify previous_response_id was set
	if params.PreviousResponseID.Value != "resp_abc123" {
		t.Errorf("expected PreviousResponseID 'resp_abc123', got %v", params.PreviousResponseID.Value)
	}

	// Verify message history was pruned: only msgUser2 should remain in input!
	inputList := params.Input.OfInputItemList
	if len(inputList) != 1 {
		t.Fatalf("expected 1 input item after pruning, got %d", len(inputList))
	}
}

func TestToResponseNewParamsStatePruningFromExtras(t *testing.T) {
	// Verify response ID is also recognized from block Extras (parity with thought signatures)
	msgUser1 := message.NewUserText("Hi, I'm Bob.")
	msgAsst1 := &message.Assistant{
		Content: message.Content{
			&message.TextBlock{
				Text: "Hello Bob!",
				Extras: map[string]any{
					ResponseIDKey: "resp_from_extras_456",
				},
			},
		},
	}
	msgUser2 := message.NewUserText("What is my name?")

	req := &llm.Request{
		Model: "gpt-4o",
		Messages: []message.Message{
			msgUser1,
			msgAsst1,
			msgUser2,
		},
	}

	opts := ResponsesOptions{
		UsePreviousResponseID: true,
	}

	params, err := toResponseNewParams(req, opts)
	if err != nil {
		t.Fatalf("toResponseNewParams failed: %v", err)
	}

	if params.PreviousResponseID.Value != "resp_from_extras_456" {
		t.Errorf("expected PreviousResponseID 'resp_from_extras_456', got %v", params.PreviousResponseID.Value)
	}

	inputList := params.Input.OfInputItemList
	if len(inputList) != 1 {
		t.Fatalf("expected 1 input item after pruning, got %d", len(inputList))
	}
}

func TestToResponseNewParamsStoreFalseEncryptedContent(t *testing.T) {
	storeFalse := false

	// In Store: false mode (Zero Data Retention):
	// - Previous messages must NOT be pruned
	// - PreviousResponseID must NOT be set
	// - Encrypted reasoning content in ThinkingBlock.Extras MUST be passed in input as reasoning item!
	msgUser1 := message.NewUserText("Calculate 17 * 43")
	msgAsst1 := &message.Assistant{
		Content: message.Content{
			&message.ThinkingBlock{
				Thinking: "Calculating 17 * 43...",
				Extras: map[string]any{
					EncryptedContentKey: "enc_blob_crypto_789",
					ReasoningIDKey:      "rs_reasoning_1",
					ResponseIDKey:       "resp_zdr_001",
				},
			},
			&message.TextBlock{
				Text: "The result is 731.",
			},
		},
	}
	msgUser2 := message.NewUserText("Now double it.")

	req := &llm.Request{
		Model: "o3-mini",
		Messages: []message.Message{
			msgUser1,
			msgAsst1,
			msgUser2,
		},
	}

	opts := ResponsesOptions{
		Store:                 &storeFalse,
		UsePreviousResponseID: true, // Should be ignored when Store is false
	}

	params, err := toResponseNewParams(req, opts)
	if err != nil {
		t.Fatalf("toResponseNewParams failed: %v", err)
	}

	// Verify store is false
	if params.Store.Value != false {
		t.Errorf("expected Store false, got %v", params.Store.Value)
	}

	// Verify PreviousResponseID is NOT set
	if params.PreviousResponseID.Value != "" {
		t.Errorf("expected empty PreviousResponseID when Store is false, got %v", params.PreviousResponseID.Value)
	}

	// Verify input items:
	// 1. User1
	// 2. Reasoning item with encrypted_content
	// 3. Assistant text
	// 4. User2
	inputList := params.Input.OfInputItemList
	if len(inputList) != 4 {
		t.Fatalf("expected 4 input items, got %d", len(inputList))
	}

	// Item 1: User message
	// Item 2: Reasoning item
	reasoningItem := inputList[1].OfReasoning
	if reasoningItem == nil {
		t.Fatalf("expected item 1 to be OfReasoning, got nil")
	}
	if reasoningItem.EncryptedContent.Value != "enc_blob_crypto_789" {
		t.Errorf("expected encrypted content 'enc_blob_crypto_789', got %v", reasoningItem.EncryptedContent.Value)
	}
	if reasoningItem.ID != "rs_reasoning_1" {
		t.Errorf("expected reasoning ID 'rs_reasoning_1', got %v", reasoningItem.ID)
	}
}

func TestToResponseNewParamsStructuredOutput(t *testing.T) {
	schema := &jsonschema.Schema{
		Title:       "UserResponse",
		Description: "A structured user response",
		Type:        "object",
		Properties: map[string]*jsonschema.Schema{
			"name": {Type: "string"},
			"age":  {Type: "integer"},
		},
	}

	req := &llm.Request{
		Model:          "gpt-4o",
		ResponseSchema: schema,
		Messages: []message.Message{
			message.NewUserText("Provide profile for Alice, age 30"),
		},
	}

	params, err := toResponseNewParams(req, ResponsesOptions{})
	if err != nil {
		t.Fatalf("toResponseNewParams failed: %v", err)
	}

	jsonSchema := params.Text.Format.OfJSONSchema
	if jsonSchema == nil {
		t.Fatalf("expected OfJSONSchema to be set")
	}
	if jsonSchema.Name != "UserResponse" {
		t.Errorf("expected schema name 'UserResponse', got %v", jsonSchema.Name)
	}
	if jsonSchema.Strict.Value != true {
		t.Errorf("expected strict true, got %v", jsonSchema.Strict.Value)
	}
}
