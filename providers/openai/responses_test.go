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

	// 10. Auto-routing: WithToolSearch()
	reqToolSearch := &llm.Request{
		Model: "gpt-4o",
		Extensions: map[string]llm.Extension{
			ResponsesExtensionID: WithToolSearch(),
		},
	}
	use, opts = defaultProv.shouldUseResponsesAPI(reqToolSearch)
	if !use || !opts.ToolSearch {
		t.Errorf("expected auto-routing to responses API when WithToolSearch() is used")
	}

	// 11. Feature-based trigger: DeferLoading: true
	reqDeferred := &llm.Request{
		Model: "gpt-4o",
		Tools: []tool.Definition{
			{Name: "search_docs", DeferLoading: true},
		},
	}
	use, _ = defaultProv.shouldUseResponsesAPI(reqDeferred)
	if !use {
		t.Errorf("expected auto-routing to responses API when tool has DeferLoading: true")
	}
	// But if defaultAPIMode is APIModeChatCompletions, it should not route to responses
	use, _ = chatOnlyProv.shouldUseResponsesAPI(reqDeferred)
	if use {
		t.Errorf("expected APIModeChatCompletions to not route to responses even with DeferLoading: true")
	}

	// 12. Feature-based trigger: ToolReferenceBlock
	reqToolRef := &llm.Request{
		Model: "gpt-4o",
		Messages: []message.Message{
			message.NewUser(&message.ToolReferenceBlock{ToolName: "search_docs"}),
		},
	}
	use, _ = defaultProv.shouldUseResponsesAPI(reqToolRef)
	if !use {
		t.Errorf("expected auto-routing to responses API when message has ToolReferenceBlock")
	}
	use, _ = chatOnlyProv.shouldUseResponsesAPI(reqToolRef)
	if use {
		t.Errorf("expected APIModeChatCompletions to not route to responses even with ToolReferenceBlock")
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
	weatherTool := params.Tools[1].OfFunction
	if weatherTool == nil {
		t.Fatalf("expected function tool at index 1")
	}
	if weatherTool.Name != "get_weather" {
		t.Errorf("expected tool name 'get_weather', got %s", weatherTool.Name)
	}
	if weatherTool.Description.Value != "Get the current weather" {
		t.Errorf("expected tool description 'Get the current weather', got %s", weatherTool.Description.Value)
	}
	if weatherTool.Strict.Value != false {
		t.Errorf("expected tool strict to be false by default, got %v", weatherTool.Strict.Value)
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

func TestToResponseNewParamsStrictTools(t *testing.T) {
	req := &llm.Request{
		Model: "gpt-4o",
		Tools: []tool.Definition{
			{
				Name:        "view",
				Description: "View a file with optional limit",
				InputSchema: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"path":  {Type: "string"},
						"limit": {Type: "integer"},
					},
					Required: []string{"path"},
				},
			},
		},
		Messages: []message.Message{
			message.NewUserText("view file"),
		},
	}

	// Default: StrictTools = false (optional properties like 'limit' are allowed without being in Required)
	paramsDefault, err := toResponseNewParams(req, ResponsesOptions{})
	if err != nil {
		t.Fatalf("failed default: %v", err)
	}
	if len(paramsDefault.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(paramsDefault.Tools))
	}
	funcTool := paramsDefault.Tools[0].OfFunction
	if funcTool.Strict.Value != false {
		t.Errorf("expected strict false by default, got %v", funcTool.Strict.Value)
	}
	if funcTool.Description.Value != "View a file with optional limit" {
		t.Errorf("expected description preserved, got %s", funcTool.Description.Value)
	}

	// Explicit: WithStrictTools(true)
	paramsStrict, err := toResponseNewParams(req, WithStrictTools(true))
	if err != nil {
		t.Fatalf("failed strict: %v", err)
	}
	if paramsStrict.Tools[0].OfFunction.Strict.Value != true {
		t.Errorf("expected strict true when enabled, got %v", paramsStrict.Tools[0].OfFunction.Strict.Value)
	}
}

func TestModelSupportsToolSearch(t *testing.T) {
	supported := []string{
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.4-pro",
		"gpt-5.5",
		"gpt-5.6-sol",
		"gpt-6-sol",
		"gpt-6.1-terra",
	}
	for _, m := range supported {
		if !modelSupportsToolSearch(m) {
			t.Errorf("expected modelSupportsToolSearch(%q) to be true, got false", m)
		}
	}

	unsupported := []string{
		"gpt-5",
		"gpt-5-pro",
		"gpt-5.1",
		"gpt-5.2-pro",
		"gpt-5.3-codex",
		"gpt-4o",
		"o3",
		"claude-opus-4-6",
	}
	for _, m := range unsupported {
		if modelSupportsToolSearch(m) {
			t.Errorf("expected modelSupportsToolSearch(%q) to be false, got true", m)
		}
	}
}

func TestToChatCompletionNewParams_FailFastValidation(t *testing.T) {
	// 1. DeferLoading: true
	reqDeferred := &llm.Request{
		Model: "gpt-4o",
		Tools: []tool.Definition{
			{Name: "fetch_data", DeferLoading: true},
		},
	}
	_, err := toChatCompletionNewParams(reqDeferred)
	if err == nil {
		t.Errorf("expected error in toChatCompletionNewParams with DeferLoading: true, got nil")
	}

	// 2. ToolReferenceBlock
	reqToolRef := &llm.Request{
		Model: "gpt-4o",
		Messages: []message.Message{
			message.NewUser(&message.ToolReferenceBlock{ToolName: "fetch_data"}),
		},
	}
	_, err = toChatCompletionNewParams(reqToolRef)
	if err == nil {
		t.Errorf("expected error in toChatCompletionNewParams with ToolReferenceBlock, got nil")
	}
}

func TestToResponseNewParams_UnsupportedModelValidation(t *testing.T) {
	// 1. ToolSearch on unsupported model
	reqToolSearch := &llm.Request{
		Model: "gpt-4o",
	}
	_, err := toResponseNewParams(reqToolSearch, WithToolSearch())
	if err == nil {
		t.Errorf("expected error with WithToolSearch() on gpt-4o, got nil")
	}

	// 2. DeferLoading on unsupported model
	reqDeferred := &llm.Request{
		Model: "gpt-5.2-pro",
		Tools: []tool.Definition{
			{Name: "fetch_data", DeferLoading: true},
		},
	}
	_, err = toResponseNewParams(reqDeferred, ResponsesOptions{})
	if err == nil {
		t.Errorf("expected error with DeferLoading on gpt-5.2-pro, got nil")
	}

	// 3. ToolReferenceBlock on unsupported model
	reqToolRef := &llm.Request{
		Model: "gpt-4o",
		Messages: []message.Message{
			message.NewUser(&message.ToolReferenceBlock{ToolName: "fetch_data"}),
		},
	}
	_, err = toResponseNewParams(reqToolRef, ResponsesOptions{})
	if err == nil {
		t.Errorf("expected error with ToolReferenceBlock on gpt-4o, got nil")
	}
}

func TestToResponseNewParams_ToolSearchAndDeferredTools(t *testing.T) {
	req := &llm.Request{
		Model: "gpt-5.4",
		Tools: []tool.Definition{
			{
				Name:         "query_db",
				Description:  "Execute database query",
				DeferLoading: true,
				InputSchema: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"sql": {Type: "string"},
					},
					Required: []string{"sql"},
				},
			},
			{
				Name:        "another_tool",
				Description: "Another tool definition",
			},
		},
		Messages: []message.Message{
			message.NewSystemText("Initial instructions"),
			message.NewUser(
				&message.TextBlock{Text: "Find users"},
				&message.ToolReferenceBlock{ToolName: "query_db"},
			),
			&message.Assistant{
				Content: message.Content{
					&message.TextBlock{Text: "Searching..."},
					&message.ServerToolCall{
						ID:   "call_tsc_1",
						Name: "tool_search",
						Args: map[string]any{"query": "database"},
					},
					&message.ServerToolResult{
						ToolCallID: "call_tsc_1",
						Name:       "tool_search",
						Content: []message.Block{
							&message.ToolReferenceBlock{ToolName: "query_db"},
						},
					},
				},
			},
			&message.Tool{
				ToolCallID: "call_db_1",
				Content: []message.Block{
					&message.TextBlock{Text: "DB result"},
					&message.ToolReferenceBlock{ToolName: "another_tool"},
				},
			},
			message.NewSystem(
				&message.TextBlock{Text: "Mid conversation system update"},
				&message.ToolReferenceBlock{ToolName: "query_db"},
			),
		},
	}

	opts := WithToolSearch()

	params, err := toResponseNewParams(req, opts)
	if err != nil {
		t.Fatalf("toResponseNewParams failed: %v", err)
	}

	// 1. Verify instructions
	if params.Instructions.Value != "Initial instructions" {
		t.Errorf("expected instructions 'Initial instructions', got %v", params.Instructions.Value)
	}

	// 2. Verify tools: tool_search + query_db (deferred) + another_tool
	if len(params.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(params.Tools))
	}
	if params.Tools[0].OfToolSearch == nil {
		t.Fatalf("expected tool[0] to be OfToolSearch")
	}
	if params.Tools[0].OfToolSearch.Execution != "server" {
		t.Errorf("expected tool_search execution 'server', got %v", params.Tools[0].OfToolSearch.Execution)
	}
	if params.Tools[1].OfFunction == nil {
		t.Fatalf("expected tool[1] to be OfFunction")
	}
	if params.Tools[1].OfFunction.Name != "query_db" {
		t.Errorf("expected tool[1] name 'query_db', got %s", params.Tools[1].OfFunction.Name)
	}
	if params.Tools[1].OfFunction.DeferLoading.Value != true {
		t.Errorf("expected tool[1] DeferLoading true, got %v", params.Tools[1].OfFunction.DeferLoading.Value)
	}
	if params.Tools[2].OfFunction == nil {
		t.Fatalf("expected tool[2] to be OfFunction")
	}
	if params.Tools[2].OfFunction.Name != "another_tool" {
		t.Errorf("expected tool[2] name 'another_tool', got %s", params.Tools[2].OfFunction.Name)
	}
	if params.Tools[2].OfFunction.DeferLoading.Value != false {
		t.Errorf("expected tool[2] DeferLoading false, got %v", params.Tools[2].OfFunction.DeferLoading.Value)
	}

	// 3. Verify input items:
	// Item 0: User message "Find users"
	// Item 1: additional_tools with query_db
	// Item 2: Assistant message "Searching..."
	// Item 3: tool_search_call (ServerToolCall)
	// Item 4: tool_search_output (ServerToolResult)
	// Item 5: function_call_output for call_db_1
	// Item 6: additional_tools with another_tool
	// Item 7: System message "Mid conversation system update"
	// Item 8: additional_tools with query_db
	inputList := params.Input.OfInputItemList
	if len(inputList) != 9 {
		t.Fatalf("expected 9 input items, got %d", len(inputList))
	}

	// Item 0: User message
	if inputList[0].OfMessage == nil {
		t.Fatalf("expected item 0 to be OfMessage, got %+v", inputList[0])
	}
	if inputList[0].OfMessage.Content.OfString.Value != "Find users" {
		t.Errorf("expected item 0 text 'Find users', got %v", inputList[0].OfMessage.Content.OfString.Value)
	}

	// Item 1: additional_tools (query_db)
	if inputList[1].OfAdditionalTools == nil {
		t.Fatalf("expected item 1 to be OfAdditionalTools, got %+v", inputList[1])
	}
	if len(inputList[1].OfAdditionalTools.Tools) != 1 {
		t.Fatalf("expected 1 tool in additional_tools, got %d", len(inputList[1].OfAdditionalTools.Tools))
	}
	if inputList[1].OfAdditionalTools.Tools[0].OfFunction == nil || inputList[1].OfAdditionalTools.Tools[0].OfFunction.Name != "query_db" {
		t.Errorf("expected additional_tools to contain query_db")
	}

	// Item 2: Assistant text
	if inputList[2].OfMessage == nil {
		t.Fatalf("expected item 2 to be OfMessage, got %+v", inputList[2])
	}
	if inputList[2].OfMessage.Content.OfString.Value != "Searching..." {
		t.Errorf("expected item 2 text 'Searching...', got %v", inputList[2].OfMessage.Content.OfString.Value)
	}

	// Item 3: tool_search_call
	if inputList[3].OfToolSearchCall == nil {
		t.Fatalf("expected item 3 to be OfToolSearchCall, got %+v", inputList[3])
	}
	if inputList[3].OfToolSearchCall.CallID.Value != "call_tsc_1" {
		t.Errorf("expected CallID 'call_tsc_1', got %v", inputList[3].OfToolSearchCall.CallID.Value)
	}
	if inputList[3].OfToolSearchCall.Execution != "server" {
		t.Errorf("expected Execution 'server', got %v", inputList[3].OfToolSearchCall.Execution)
	}

	// Item 4: tool_search_output
	if inputList[4].OfToolSearchOutput == nil {
		t.Fatalf("expected item 4 to be OfToolSearchOutput, got %+v", inputList[4])
	}
	if inputList[4].OfToolSearchOutput.CallID.Value != "call_tsc_1" {
		t.Errorf("expected CallID 'call_tsc_1', got %v", inputList[4].OfToolSearchOutput.CallID.Value)
	}
	if inputList[4].OfToolSearchOutput.Execution != "server" {
		t.Errorf("expected Execution 'server', got %v", inputList[4].OfToolSearchOutput.Execution)
	}
	if len(inputList[4].OfToolSearchOutput.Tools) != 1 {
		t.Fatalf("expected 1 tool in tool_search_output, got %d", len(inputList[4].OfToolSearchOutput.Tools))
	}
	if inputList[4].OfToolSearchOutput.Tools[0].OfFunction == nil || inputList[4].OfToolSearchOutput.Tools[0].OfFunction.Name != "query_db" {
		t.Errorf("expected tool_search_output to contain query_db")
	}

	// Item 5: function_call_output
	if inputList[5].OfFunctionCallOutput == nil {
		t.Fatalf("expected item 5 to be OfFunctionCallOutput, got %+v", inputList[5])
	}
	if inputList[5].OfFunctionCallOutput.CallID.Value != "call_db_1" {
		t.Errorf("expected CallID 'call_db_1', got %v", inputList[5].OfFunctionCallOutput.CallID.Value)
	}
	if inputList[5].OfFunctionCallOutput.Output.OfString.Value != "DB result" {
		t.Errorf("expected Output 'DB result', got %v", inputList[5].OfFunctionCallOutput.Output.OfString.Value)
	}

	// Item 6: additional_tools (another_tool)
	if inputList[6].OfAdditionalTools == nil {
		t.Fatalf("expected item 6 to be OfAdditionalTools, got %+v", inputList[6])
	}
	if len(inputList[6].OfAdditionalTools.Tools) != 1 {
		t.Fatalf("expected 1 tool in additional_tools, got %d", len(inputList[6].OfAdditionalTools.Tools))
	}
	if inputList[6].OfAdditionalTools.Tools[0].OfFunction == nil || inputList[6].OfAdditionalTools.Tools[0].OfFunction.Name != "another_tool" {
		t.Errorf("expected additional_tools to contain another_tool")
	}

	// Item 7: System message
	if inputList[7].OfMessage == nil {
		t.Fatalf("expected item 7 to be OfMessage, got %+v", inputList[7])
	}
	if inputList[7].OfMessage.Content.OfString.Value != "Mid conversation system update" {
		t.Errorf("expected item 7 text 'Mid conversation system update', got %v", inputList[7].OfMessage.Content.OfString.Value)
	}
	if inputList[7].OfMessage.Role != "system" {
		t.Errorf("expected item 7 role 'system', got %v", inputList[7].OfMessage.Role)
	}

	// Item 8: additional_tools (query_db)
	if inputList[8].OfAdditionalTools == nil {
		t.Fatalf("expected item 8 to be OfAdditionalTools, got %+v", inputList[8])
	}
	if len(inputList[8].OfAdditionalTools.Tools) != 1 {
		t.Fatalf("expected 1 tool in additional_tools, got %d", len(inputList[8].OfAdditionalTools.Tools))
	}
	if inputList[8].OfAdditionalTools.Tools[0].OfFunction == nil || inputList[8].OfAdditionalTools.Tools[0].OfFunction.Name != "query_db" {
		t.Errorf("expected additional_tools to contain query_db")
	}
}
