package llm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/tool"
)

type middlewareMockProvider struct {
	llm.Provider
	lastRequest *llm.Request
}

func (m *middlewareMockProvider) Name() string {
	return "mock"
}

func (m *middlewareMockProvider) Stream(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
	m.lastRequest = req
	return func(yield func(message.AssistantChunk, error) bool) {}, nil
}

func (m *middlewareMockProvider) GetProfile(id string) (profile.Profile, bool) {
	return profile.Profile{}, false
}

func TestModel_Middleware(t *testing.T) {
	provider := &middlewareMockProvider{}
	model, _ := llm.NewModel(provider, "test-model", nil)

	var executionOrder []string

	mw1 := func(next llm.Streamer) llm.Streamer {
		return func(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
			executionOrder = append(executionOrder, "mw1_start")
			res, err := next(ctx, req)
			executionOrder = append(executionOrder, "mw1_end")
			return res, err
		}
	}

	mw2 := func(next llm.Streamer) llm.Streamer {
		return func(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
			executionOrder = append(executionOrder, "mw2_start")
			res, err := next(ctx, req)
			executionOrder = append(executionOrder, "mw2_end")
			return res, err
		}
	}

	model = model.WithMiddleware(mw1, mw2)

	_, _ = model.Stream(context.Background(), nil)

	expectedOrder := []string{"mw1_start", "mw2_start", "mw2_end", "mw1_end"}
	if len(executionOrder) != len(expectedOrder) {
		t.Fatalf("expected %d execution steps, got %d", len(expectedOrder), len(executionOrder))
	}

	for i, v := range expectedOrder {
		if executionOrder[i] != v {
			t.Errorf("at index %d: expected %s, got %s", i, v, executionOrder[i])
		}
	}
}

type mockExtension struct {
	val string
}

func (m mockExtension) ExtensionID() string {
	return "mock.extension"
}

func TestModel_Extensions(t *testing.T) {
	provider := &middlewareMockProvider{}
	model, _ := llm.NewModel(provider, "test-model", nil)

	ext := mockExtension{val: "val1"}
	model = model.WithExtension(ext)

	_, _ = model.Stream(context.Background(), nil)

	req := provider.lastRequest
	if req.Extensions[ext.ExtensionID()].(mockExtension).val != "val1" {
		t.Errorf("expected extensions to be propagated, got %v", req.Extensions)
	}

	// Test per-call override
	override := mockExtension{val: "override"}
	_, _ = model.Stream(context.Background(), nil, llm.WithExtensionOption(override))
	if req := provider.lastRequest; req.Extensions[ext.ExtensionID()].(mockExtension).val != "override" {
		t.Errorf("expected per-call extension override, got %v", req.Extensions[ext.ExtensionID()])
	}
}

func TestModel_MiddlewareMutation(t *testing.T) {
	provider := &middlewareMockProvider{}
	model, _ := llm.NewModel(provider, "test-model", nil)

	mw := func(next llm.Streamer) llm.Streamer {
		return func(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
			if req.Extensions == nil {
				req.Extensions = make(map[string]llm.Extension)
			}
			req.Extensions["injected"] = mockExtension{val: "true"}
			return next(ctx, req)
		}
	}

	model = model.WithMiddleware(mw)

	_, _ = model.Stream(context.Background(), nil)

	if provider.lastRequest.Extensions["injected"].(mockExtension).val != "true" {
		t.Error("middleware failed to inject extension")
	}
}

type deferredMockProvider struct {
	llm.Provider
	supportsDeferred bool
	supportsInline   bool
	lastRequest      *llm.Request
	cannedChunks     []message.AssistantChunk
}

func (d *deferredMockProvider) Name() string { return "deferred-mock" }
func (d *deferredMockProvider) Stream(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
	d.lastRequest = req
	return func(yield func(message.AssistantChunk, error) bool) {
		for _, c := range d.cannedChunks {
			if !yield(c, nil) {
				return
			}
		}
	}, nil
}
func (d *deferredMockProvider) GetProfile(id string) (profile.Profile, bool) {
	return profile.Profile{}, false
}
func (d *deferredMockProvider) SupportsDeferredTools(model string) bool {
	return d.supportsDeferred
}
func (d *deferredMockProvider) SupportsInlineTools(model string) bool {
	return d.supportsInline
}

func TestDeferredToolsMiddleware_NativeSupport(t *testing.T) {
	provider := &deferredMockProvider{supportsDeferred: true}
	model, err := llm.NewModel(provider, "test-model", nil)
	if err != nil {
		t.Fatalf("NewModel failed: %v", err)
	}
	model = model.WithMiddleware(llm.DeferredToolsMiddleware(nil))

	origTools := []tool.Definition{
		{Name: "active_tool", DeferLoading: false},
		{Name: "deferred_tool", DeferLoading: true},
	}
	model = model.BindToolDefs(origTools...)

	origMsgs := []message.Message{
		message.NewUser(
			&message.TextBlock{Text: "Please run tool"},
			&message.ToolReferenceBlock{ToolName: "deferred_tool"},
		),
	}

	_, err = model.Stream(context.Background(), origMsgs)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	req := provider.lastRequest
	if len(req.Tools) != 2 {
		t.Fatalf("expected 2 tools on native provider, got %d", len(req.Tools))
	}
	if req.Tools[1].Name != "deferred_tool" || !req.Tools[1].DeferLoading {
		t.Errorf("expected deferred_tool to pass through untouched")
	}

	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	content := req.Messages[0].GetContent()
	if len(content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(content))
	}
	if _, ok := content[1].(*message.ToolReferenceBlock); !ok {
		t.Errorf("expected ToolReferenceBlock untouched on native provider, got %T", content[1])
	}
}

func TestDeferredToolsMiddleware_Fallback(t *testing.T) {
	provider := &deferredMockProvider{supportsDeferred: false}
	model, err := llm.NewModel(provider, "test-model", nil)
	if err != nil {
		t.Fatalf("NewModel failed: %v", err)
	}
	model = model.WithMiddleware(llm.DeferredToolsMiddleware(nil))

	origTools := []tool.Definition{
		{Name: "active_tool", DeferLoading: false},
		{Name: "deferred_tool", Description: "A deferred tool", DeferLoading: true},
	}
	model = model.BindToolDefs(origTools...)

	origUserMsg := message.NewUser(
		&message.TextBlock{Text: "Please run tool"},
		&message.ToolReferenceBlock{ToolName: "deferred_tool"},
	)
	origAsstMsg := &message.Assistant{
		Content: message.Content{
			&message.ServerToolResult{
				ToolCallID: "srv-1",
				Name:       "tool_search",
				Content: message.Content{
					&message.ToolReferenceBlock{ToolName: "deferred_tool"},
				},
			},
		},
	}
	origMsgs := []message.Message{origUserMsg, origAsstMsg}

	_, err = model.Stream(context.Background(), origMsgs)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	req := provider.lastRequest
	// 1. DeferLoading: true tools must be filtered out
	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 active tool on fallback provider, got %d", len(req.Tools))
	}
	if req.Tools[0].Name != "active_tool" {
		t.Errorf("expected only active_tool, got %s", req.Tools[0].Name)
	}

	// 2. ToolReferenceBlocks must be transformed into schema TextBlocks
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}
	userBlocks := req.Messages[0].GetContent()
	refBlock, ok := userBlocks[1].(*message.TextBlock)
	if !ok {
		t.Fatalf("expected ToolReferenceBlock to be transformed to *message.TextBlock, got %T", userBlocks[1])
	}
	if refBlock.Text == "" || !strings.Contains(refBlock.Text, "tool_definition") || !strings.Contains(refBlock.Text, "deferred_tool") {
		t.Errorf("expected expanded JSON schema text block, got %s", refBlock.Text)
	}

	// 3. Nested ToolReferenceBlocks in ServerToolResult must also be transformed
	asstBlocks := req.Messages[1].GetContent()
	str, ok := asstBlocks[0].(*message.ServerToolResult)
	if !ok {
		t.Fatalf("expected ServerToolResult, got %T", asstBlocks[0])
	}
	nestedRefBlock, ok := str.Content[0].(*message.TextBlock)
	if !ok {
		t.Fatalf("expected nested ToolReferenceBlock to be transformed to *message.TextBlock, got %T", str.Content[0])
	}
	if !strings.Contains(nestedRefBlock.Text, "deferred_tool") {
		t.Errorf("expected nested expanded schema text block, got %s", nestedRefBlock.Text)
	}

	// 4. Caller's original messages must NOT be mutated
	origUserBlocks := origUserMsg.GetContent()
	if _, ok := origUserBlocks[1].(*message.ToolReferenceBlock); !ok {
		t.Errorf("expected original caller's message to remain a ToolReferenceBlock, got %T", origUserBlocks[1])
	}
	origAsstBlocks := origAsstMsg.GetContent()
	origStr := origAsstBlocks[0].(*message.ServerToolResult)
	if _, ok := origStr.Content[0].(*message.ToolReferenceBlock); !ok {
		t.Errorf("expected original caller's ServerToolResult content to remain a ToolReferenceBlock, got %T", origStr.Content[0])
	}
}

func TestDeferredToolsMiddleware_CustomMatch(t *testing.T) {
	// Provider does not natively support deferred tools, but custom Match returns false (do not transform)
	provider := &deferredMockProvider{supportsDeferred: false}
	opts := &llm.DeferredToolsOptions{
		Match: func(ctx context.Context, req *llm.Request) bool {
			return false
		},
	}
	model, err := llm.NewModel(provider, "test-model", nil)
	if err != nil {
		t.Fatalf("NewModel failed: %v", err)
	}
	model = model.WithMiddleware(llm.DeferredToolsMiddleware(opts))
	model = model.BindToolDefs(tool.Definition{Name: "deferred_tool", DeferLoading: true})

	_, err = model.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	if len(provider.lastRequest.Tools) != 1 {
		t.Fatalf("expected Match=false to preserve deferred tools, got %d", len(provider.lastRequest.Tools))
	}

	// Conversely, provider supports deferred tools, but custom Match returns true (force transform)
	providerNative := &deferredMockProvider{supportsDeferred: true}
	optsTransform := &llm.DeferredToolsOptions{
		Match: func(ctx context.Context, req *llm.Request) bool {
			return true
		},
	}
	modelNative, err := llm.NewModel(providerNative, "test-model", nil)
	if err != nil {
		t.Fatalf("NewModel failed: %v", err)
	}
	modelNative = modelNative.WithMiddleware(llm.DeferredToolsMiddleware(optsTransform))
	modelNative = modelNative.BindToolDefs(tool.Definition{Name: "deferred_tool", DeferLoading: true})

	_, err = modelNative.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	if len(providerNative.lastRequest.Tools) != 0 {
		t.Fatalf("expected Match=true to filter deferred tools, got %d", len(providerNative.lastRequest.Tools))
	}
}

func TestDeferredToolsMiddleware_CustomFormatReference(t *testing.T) {
	provider := &deferredMockProvider{supportsDeferred: false}
	opts := &llm.DeferredToolsOptions{
		FormatReference: func(def tool.Definition) message.Block {
			return &message.TextBlock{Text: "CUSTOM_SCHEMA:" + def.Name}
		},
	}
	model, err := llm.NewModel(provider, "test-model", nil)
	if err != nil {
		t.Fatalf("NewModel failed: %v", err)
	}
	model = model.WithMiddleware(llm.DeferredToolsMiddleware(opts))
	model = model.BindToolDefs(tool.Definition{Name: "fetch_info", DeferLoading: true})

	msgs := []message.Message{
		message.NewUser(&message.ToolReferenceBlock{ToolName: "fetch_info"}),
	}

	_, err = model.Stream(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	userBlocks := provider.lastRequest.Messages[0].GetContent()
	txtBlock, ok := userBlocks[0].(*message.TextBlock)
	if !ok {
		t.Fatalf("expected *message.TextBlock, got %T", userBlocks[0])
	}
	if txtBlock.Text != "CUSTOM_SCHEMA:fetch_info" {
		t.Errorf("expected custom formatted block 'CUSTOM_SCHEMA:fetch_info', got %q", txtBlock.Text)
	}
}

func TestDeferredToolsMiddleware_Coercion(t *testing.T) {
	type subObj struct {
		Key string `json:"key"`
	}
	type testInput struct {
		NumVal  float64 `json:"num_val"`
		IntVal  int     `json:"int_val"`
		BoolVal bool    `json:"bool_val"`
		ArrVal  []int   `json:"arr_val"`
		ObjVal  subObj  `json:"obj_val"`
		StrVal  string  `json:"str_val"`
	}

	deferredTool, err := tool.New("deferred_calc", "Deferred Calc", "A deferred calculator", func(ctx context.Context, in testInput) (string, error) {
		return "deferred ok", nil
	}, tool.WithDeferLoading())
	if err != nil {
		t.Fatalf("tool.New failed: %v", err)
	}

	activeTool, err := tool.New("active_calc", "Active Calc", "An active calculator", func(ctx context.Context, in testInput) (string, error) {
		return "active ok", nil
	})
	if err != nil {
		t.Fatalf("tool.New failed: %v", err)
	}

	cannedChunks := []message.AssistantChunk{
		{
			Content: message.Content{
				&message.ToolCall{
					ID:   "call_def",
					Name: "deferred_calc",
					Args: map[string]any{
						"num_val":  "42.5",
						"int_val":  "10",
						"bool_val": "true",
						"arr_val":  "[1, 2, 3]",
						"obj_val":  `{"key":"hello"}`,
						"str_val":  "world",
					},
				},
				&message.ToolCall{
					ID:   "call_act",
					Name: "active_calc",
					Args: map[string]any{
						"num_val":  "42.5",
						"int_val":  "10",
						"bool_val": "true",
						"arr_val":  "[1, 2, 3]",
						"obj_val":  `{"key":"hello"}`,
						"str_val":  "world",
					},
				},
			},
		},
	}

	provider := &deferredMockProvider{
		supportsDeferred: false,
		cannedChunks:     cannedChunks,
	}

	model, err := llm.NewModel(provider, "test-model", nil)
	if err != nil {
		t.Fatalf("NewModel failed: %v", err)
	}
	model = model.WithMiddleware(llm.DeferredToolsMiddleware(nil))
	model = model.BindTools(deferredTool, activeTool)

	stream, err := model.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var chunks []message.AssistantChunk
	for chunk, err := range stream {
		if err != nil {
			t.Fatalf("stream chunk error: %v", err)
		}
		chunks = append(chunks, chunk)
	}

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	var defCall, actCall *message.ToolCall
	for _, b := range chunks[0].Content {
		if tc, ok := b.(*message.ToolCall); ok {
			switch tc.Name {
			case "deferred_calc":
				defCall = tc
			case "active_calc":
				actCall = tc
			}
		}
	}

	if defCall == nil {
		t.Fatal("missing deferred_calc tool call")
	}
	if actCall == nil {
		t.Fatal("missing active_calc tool call")
	}

	// 1. Verify deferred_calc tool call arguments were coerced
	if v, ok := defCall.Args["num_val"].(float64); !ok || v != 42.5 {
		t.Errorf("expected num_val to be float64(42.5), got %T (%v)", defCall.Args["num_val"], defCall.Args["num_val"])
	}
	if v, ok := defCall.Args["int_val"].(int64); !ok || v != 10 {
		t.Errorf("expected int_val to be int64(10), got %T (%v)", defCall.Args["int_val"], defCall.Args["int_val"])
	}
	if v, ok := defCall.Args["bool_val"].(bool); !ok || !v {
		t.Errorf("expected bool_val to be bool(true), got %T (%v)", defCall.Args["bool_val"], defCall.Args["bool_val"])
	}
	if arr, ok := defCall.Args["arr_val"].([]any); !ok || len(arr) != 3 {
		t.Errorf("expected arr_val to be []any of len 3, got %T (%v)", defCall.Args["arr_val"], defCall.Args["arr_val"])
	}
	if obj, ok := defCall.Args["obj_val"].(map[string]any); !ok || obj["key"] != "hello" {
		t.Errorf("expected obj_val to be map[string]any with key='hello', got %T (%v)", defCall.Args["obj_val"], defCall.Args["obj_val"])
	}
	if v, ok := defCall.Args["str_val"].(string); !ok || v != "world" {
		t.Errorf("expected str_val to be string('world'), got %T (%v)", defCall.Args["str_val"], defCall.Args["str_val"])
	}

	// 2. Verify schema.Validate and tool.Container.Call succeed on coerced tool call
	if err := deferredTool.Validate(defCall.Args); err != nil {
		t.Errorf("deferredTool.Validate failed on coerced args: %v", err)
	}
	container := tool.NewContainer(deferredTool, activeTool)
	res, err := container.Call(context.Background(), defCall)
	if err != nil {
		t.Fatalf("container.Call failed on coerced deferred tool call: %v", err)
	}
	if res.StructuredContent != "deferred ok" {
		t.Errorf("expected 'deferred ok', got %v", res.StructuredContent)
	}

	// 3. Verify active_calc tool call arguments were NOT coerced (remain strings)
	for k, expected := range map[string]string{
		"num_val":  "42.5",
		"int_val":  "10",
		"bool_val": "true",
		"arr_val":  "[1, 2, 3]",
		"obj_val":  `{"key":"hello"}`,
		"str_val":  "world",
	} {
		if v, ok := actCall.Args[k].(string); !ok || v != expected {
			t.Errorf("expected active_calc %s to remain string %q, got %T (%v)", k, expected, actCall.Args[k], actCall.Args[k])
		}
	}

	// And active_calc fails validation when called directly because its args are uncoerced strings
	if err := activeTool.Validate(actCall.Args); err == nil {
		t.Errorf("expected activeTool.Validate to fail on uncoerced string args, but got nil")
	}
}

func TestDeferredToolsMiddleware_DisableCoercion(t *testing.T) {
	type testInput struct {
		NumVal  float64 `json:"num_val"`
		IntVal  int     `json:"int_val"`
		BoolVal bool    `json:"bool_val"`
	}

	deferredTool, err := tool.New("deferred_calc", "Deferred Calc", "A deferred calculator", func(ctx context.Context, in testInput) (string, error) {
		return "deferred ok", nil
	}, tool.WithDeferLoading())
	if err != nil {
		t.Fatalf("tool.New failed: %v", err)
	}

	cannedChunks := []message.AssistantChunk{
		{
			Content: message.Content{
				&message.ToolCall{
					ID:   "call_def",
					Name: "deferred_calc",
					Args: map[string]any{
						"num_val":  "42.5",
						"int_val":  "10",
						"bool_val": "true",
					},
				},
			},
		},
	}

	provider := &deferredMockProvider{
		supportsDeferred: false,
		cannedChunks:     cannedChunks,
	}

	opts := &llm.DeferredToolsOptions{
		DisableCoercion: true,
	}

	model, err := llm.NewModel(provider, "test-model", nil)
	if err != nil {
		t.Fatalf("NewModel failed: %v", err)
	}
	model = model.WithMiddleware(llm.DeferredToolsMiddleware(opts))
	model = model.BindTools(deferredTool)

	stream, err := model.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	var chunks []message.AssistantChunk
	for chunk, err := range stream {
		if err != nil {
			t.Fatalf("stream chunk error: %v", err)
		}
		chunks = append(chunks, chunk)
	}

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	tc, ok := chunks[0].Content[0].(*message.ToolCall)
	if !ok {
		t.Fatalf("expected *message.ToolCall, got %T", chunks[0].Content[0])
	}

	// Verify all arguments remain untouched strings
	if v, ok := tc.Args["num_val"].(string); !ok || v != "42.5" {
		t.Errorf("expected num_val to remain string '42.5', got %T (%v)", tc.Args["num_val"], tc.Args["num_val"])
	}
	if v, ok := tc.Args["int_val"].(string); !ok || v != "10" {
		t.Errorf("expected int_val to remain string '10', got %T (%v)", tc.Args["int_val"], tc.Args["int_val"])
	}
	if v, ok := tc.Args["bool_val"].(string); !ok || v != "true" {
		t.Errorf("expected bool_val to remain string 'true', got %T (%v)", tc.Args["bool_val"], tc.Args["bool_val"])
	}
}

func TestInlineToolsMiddleware_NativeSupport(t *testing.T) {
	provider := &deferredMockProvider{supportsInline: true}
	model, err := llm.NewModel(provider, "test-model", nil)
	if err != nil {
		t.Fatalf("NewModel failed: %v", err)
	}
	model = model.WithMiddleware(llm.InlineToolsMiddleware(nil))

	origTools := []tool.Definition{
		{Name: "inline_tool", Description: "Initial definition"},
	}
	model = model.BindToolDefs(origTools...)

	origMsgs := []message.Message{
		message.NewUser(
			&message.TextBlock{Text: "Please run tool"},
			&message.ToolDefinitionBlock{Name: "inline_tool", Description: "Inline definition"},
		),
	}

	_, err = model.Stream(context.Background(), origMsgs)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	req := provider.lastRequest
	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool on native provider, got %d", len(req.Tools))
	}

	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	content := req.Messages[0].GetContent()
	if len(content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(content))
	}
	if _, ok := content[1].(*message.ToolDefinitionBlock); !ok {
		t.Errorf("expected ToolDefinitionBlock untouched on native provider, got %T", content[1])
	}
}

func TestInlineToolsMiddleware_FallbackAndCoercion(t *testing.T) {
	type testInput struct {
		NumVal  float64 `json:"num_val"`
		IntVal  int     `json:"int_val"`
		BoolVal bool    `json:"bool_val"`
	}

	myTool, err := tool.New("calc_tool", "Calculator", "A calculator tool", func(ctx context.Context, in testInput) (string, error) {
		return "calc ok", nil
	})
	if err != nil {
		t.Fatalf("tool.New failed: %v", err)
	}

	cannedChunks := []message.AssistantChunk{
		{
			Content: message.Content{
				&message.ToolCall{
					ID:   "call_inline",
					Name: "calc_tool",
					Args: map[string]any{
						"num_val":  "99.5",
						"int_val":  "42",
						"bool_val": "true",
					},
				},
			},
		},
	}

	provider := &deferredMockProvider{
		supportsInline: false,
		cannedChunks:   cannedChunks,
	}

	model, err := llm.NewModel(provider, "test-model", nil)
	if err != nil {
		t.Fatalf("NewModel failed: %v", err)
	}
	model = model.WithMiddleware(llm.InlineToolsMiddleware(nil))
	// Also bind calc_tool in initial tools to verify it is filtered out from req.Tools
	model = model.BindTools(myTool)

	origUserMsg := message.NewUser(
		&message.TextBlock{Text: "Activate calc:"},
		myTool.ToBlock(),
	)
	origMsgs := []message.Message{origUserMsg}

	stream, err := model.Stream(context.Background(), origMsgs)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	req := provider.lastRequest
	// 1. Tool should be filtered out from req.Tools because it was injected inline
	if len(req.Tools) != 0 {
		t.Fatalf("expected 0 tools in req.Tools (filtered out), got %d", len(req.Tools))
	}

	// 2. ToolDefinitionBlock should be expanded into TextBlock with <tools> schema
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	blocks := req.Messages[0].GetContent()
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	txtBlock, ok := blocks[1].(*message.TextBlock)
	if !ok {
		t.Fatalf("expected expanded *message.TextBlock, got %T", blocks[1])
	}
	if !strings.Contains(txtBlock.Text, "calc_tool") || !strings.Contains(txtBlock.Text, "<tools>") {
		t.Errorf("expected expanded schema text block, got %s", txtBlock.Text)
	}

	// 3. Original user message was NOT mutated
	if _, ok := origUserMsg.GetContent()[1].(*message.ToolDefinitionBlock); !ok {
		t.Errorf("expected original message to remain *message.ToolDefinitionBlock, got %T", origUserMsg.GetContent()[1])
	}

	// 4. Verify stream argument coercion
	var chunks []message.AssistantChunk
	for chunk, err := range stream {
		if err != nil {
			t.Fatalf("chunk error: %v", err)
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	tc := chunks[0].Content[0].(*message.ToolCall)
	if v, ok := tc.Args["num_val"].(float64); !ok || v != 99.5 {
		t.Errorf("expected num_val to be float64(99.5), got %T (%v)", tc.Args["num_val"], tc.Args["num_val"])
	}
	if v, ok := tc.Args["int_val"].(int64); !ok || v != 42 {
		t.Errorf("expected int_val to be int64(42), got %T (%v)", tc.Args["int_val"], tc.Args["int_val"])
	}
	if v, ok := tc.Args["bool_val"].(bool); !ok || !v {
		t.Errorf("expected bool_val to be true, got %T (%v)", tc.Args["bool_val"], tc.Args["bool_val"])
	}

	// 5. Verify tool execution via container
	c := tool.NewContainer(myTool)
	res, err := c.Call(context.Background(), tc)
	if err != nil {
		t.Fatalf("container.Call failed on coerced inline tool: %v", err)
	}
	if res.StructuredContent != "calc ok" {
		t.Errorf("expected 'calc ok', got %v", res.StructuredContent)
	}
}

func TestInlineToolsMiddleware_CustomOptions(t *testing.T) {
	// Test Match override: provider reports supportsInline: false, but Match returns false (no transform)
	provider := &deferredMockProvider{supportsInline: false}
	opts := &llm.InlineToolsOptions{
		Match: func(ctx context.Context, req *llm.Request) bool {
			return false
		},
	}
	model, err := llm.NewModel(provider, "test-model", nil)
	if err != nil {
		t.Fatalf("NewModel failed: %v", err)
	}
	model = model.WithMiddleware(llm.InlineToolsMiddleware(opts))
	msgs := []message.Message{
		message.NewUser(&message.ToolDefinitionBlock{Name: "unmodified"}),
	}
	_, err = model.Stream(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	if _, ok := provider.lastRequest.Messages[0].GetContent()[0].(*message.ToolDefinitionBlock); !ok {
		t.Errorf("expected ToolDefinitionBlock to remain untouched when Match=false")
	}

	// Test FormatDefinition and DisableCoercion
	canned := []message.AssistantChunk{
		{
			Content: message.Content{
				&message.ToolCall{
					ID:   "call-1",
					Name: "format_test",
					Args: map[string]any{"count": "123"},
				},
			},
		},
	}
	providerCustom := &deferredMockProvider{
		supportsInline: false,
		cannedChunks:   canned,
	}
	optsCustom := &llm.InlineToolsOptions{
		FormatDefinition: func(def tool.Definition) message.Block {
			return &message.TextBlock{Text: "CUSTOM_INLINE:" + def.Name}
		},
		DisableCoercion: true,
	}
	modelCustom, err := llm.NewModel(providerCustom, "test-model", nil)
	if err != nil {
		t.Fatalf("NewModel failed: %v", err)
	}
	modelCustom = modelCustom.WithMiddleware(llm.InlineToolsMiddleware(optsCustom))

	tdb := &message.ToolDefinitionBlock{
		Name: "format_test",
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"count": {Type: "integer"},
			},
		},
	}
	stream, err := modelCustom.Stream(context.Background(), []message.Message{message.NewUser(tdb)})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	sentBlock := providerCustom.lastRequest.Messages[0].GetContent()[0].(*message.TextBlock)
	if sentBlock.Text != "CUSTOM_INLINE:format_test" {
		t.Errorf("expected custom format 'CUSTOM_INLINE:format_test', got %q", sentBlock.Text)
	}

	var chunks []message.AssistantChunk
	for chunk, err := range stream {
		if err != nil {
			t.Fatalf("chunk err: %v", err)
		}
		chunks = append(chunks, chunk)
	}
	tc := chunks[0].Content[0].(*message.ToolCall)
	if v, ok := tc.Args["count"].(string); !ok || v != "123" {
		t.Errorf("expected count to remain string '123' when DisableCoercion=true, got %T (%v)", tc.Args["count"], tc.Args["count"])
	}
}
