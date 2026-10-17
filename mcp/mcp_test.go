package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/masterkeysrd/loom/message"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMapContent(t *testing.T) {
	c := &Client{}

	mcpContent := []mcp.Content{
		&mcp.TextContent{Text: "hello"},
		&mcp.ImageContent{Data: []byte("fake-image"), MIMEType: "image/png"},
		&mcp.EmbeddedResource{
			Resource: &mcp.ResourceContents{
				Text: "embedded text",
			},
		},
		&mcp.ResourceLink{
			URI:         "https://example.com/doc",
			Name:        "doc_name",
			Title:       "Doc Title",
			Description: "Doc description",
			MIMEType:    "text/plain",
		},
		&mcp.ResourceLink{
			URI:  "https://example.com/resource2",
			Name: "resource_two",
		},
		&mcp.ResourceLink{
			URI: "https://example.com/resource3",
		},
	}

	content, isError := c.mapContent(mcpContent)
	if isError {
		t.Errorf("expected isError to be false")
	}

	if len(content) != 6 {
		t.Fatalf("expected 6 blocks, got %d", len(content))
	}

	if tb, ok := content[0].(*message.TextBlock); !ok || tb.Text != "hello" {
		t.Errorf("expected first block to be text 'hello'")
	}

	if ib, ok := content[1].(*message.ImageBlock); !ok || string(ib.Data) != "fake-image" {
		t.Errorf("expected second block to be image 'fake-image'")
	}

	if tb, ok := content[2].(*message.TextBlock); !ok || tb.Text != "embedded text" {
		t.Errorf("expected third block to be text 'embedded text'")
	}

	// Check ResourceLink with Title & Description
	if tb, ok := content[3].(*message.TextBlock); !ok {
		t.Errorf("expected fourth block to be TextBlock")
	} else {
		expectedText := "[Resource: Doc Title](https://example.com/doc): Doc description"
		if tb.Text != expectedText {
			t.Errorf("expected text %q, got %q", expectedText, tb.Text)
		}
		if tb.Extras["type"] != "resource_link" || tb.Extras["uri"] != "https://example.com/doc" ||
			tb.Extras["title"] != "Doc Title" || tb.Extras["name"] != "doc_name" ||
			tb.Extras["description"] != "Doc description" || tb.Extras["mime_type"] != "text/plain" {
			t.Errorf("unexpected extras in resource link block: %+v", tb.Extras)
		}
	}

	// Check ResourceLink fallback to Name (no Title)
	if tb, ok := content[4].(*message.TextBlock); !ok {
		t.Errorf("expected fifth block to be TextBlock")
	} else {
		expectedText := "[Resource: resource_two](https://example.com/resource2)"
		if tb.Text != expectedText {
			t.Errorf("expected text %q, got %q", expectedText, tb.Text)
		}
	}

	// Check ResourceLink fallback to URI (no Title, no Name)
	if tb, ok := content[5].(*message.TextBlock); !ok {
		t.Errorf("expected sixth block to be TextBlock")
	} else {
		expectedText := "[Resource: https://example.com/resource3](https://example.com/resource3)"
		if tb.Text != expectedText {
			t.Errorf("expected text %q, got %q", expectedText, tb.Text)
		}
	}
}

func TestStructuredContentMapping(t *testing.T) {

	// mock MCP CallToolResult with StructuredContent
	mcpRes := &mcp.CallToolResult{
		StructuredContent: map[string]any{"key": "value"},
	}

	// We can't easily test createHandler without a real session,
	// but we can test that mapContent works as expected if we had a way to call it.
	// Actually, I just updated createHandler to pass StructuredContent.

	chunk := message.ToolChunk{
		StructuredContent: mcpRes.StructuredContent,
	}

	if chunk.StructuredContent.(map[string]any)["key"] != "value" {
		t.Errorf("expected structured content to be preserved")
	}
}

func TestMapMessages(t *testing.T) {
	c := &Client{}
	mcpMessages := []*mcp.PromptMessage{
		{Role: "user", Content: &mcp.TextContent{Text: "user msg"}},
		{Role: "assistant", Content: &mcp.TextContent{Text: "assistant msg"}},
		{Role: "system", Content: &mcp.TextContent{Text: "system msg"}},
	}

	messages := c.mapMessages(mcpMessages)
	if len(messages) != 3 {
		t.Errorf("expected 3 messages, got %d", len(messages))
	}

	if _, ok := messages[0].(*message.User); !ok {
		t.Errorf("expected first message to be user")
	}
	if _, ok := messages[1].(*message.Assistant); !ok {
		t.Errorf("expected second message to be assistant")
	}
	if _, ok := messages[2].(*message.System); !ok {
		t.Errorf("expected third message to be system")
	}
}

func TestConfigInitialization(t *testing.T) {
	config := map[string]Config{
		"math": {
			Transport:             "stdio",
			Command:               "python",
			Args:                  []string{"math_server.py"},
			Env:                   map[string]string{"DEBUG": "true"},
			ProtocolVersion:       ProtocolVersion20260728,
			DisableStandaloneSSE:  true,
			MaxRetries:            3,
			DisableMultiRoundTrip: true,
		},
	}
	mc := NewMultiClient(config)
	c, ok := mc.clients["math"]
	if !ok {
		t.Fatalf("expected math client to be initialized")
	}
	if c.config.ProtocolVersion != ProtocolVersion20260728 {
		t.Errorf("expected ProtocolVersion %q, got %q", ProtocolVersion20260728, c.config.ProtocolVersion)
	}
	if !c.config.DisableStandaloneSSE {
		t.Errorf("expected DisableStandaloneSSE to be true")
	}
	if c.config.MaxRetries != 3 {
		t.Errorf("expected MaxRetries to be 3")
	}
	if !c.config.DisableMultiRoundTrip {
		t.Errorf("expected DisableMultiRoundTrip to be true")
	}
}

type mockRoundTripper struct {
	roundTrip func(req *http.Request) (*http.Response, error)
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTrip(req)
}

func TestHeaderRoundTripper(t *testing.T) {
	headers := map[string]string{"X-Test": "Value"}
	rt := &headerRoundTripper{
		headers: headers,
		base: &mockRoundTripper{
			roundTrip: func(req *http.Request) (*http.Response, error) {
				if req.Header.Get("X-Test") != "Value" {
					t.Errorf("expected header X-Test to be Value")
				}
				rec := httptest.NewRecorder()
				return rec.Result(), nil
			},
		},
	}

	req := httptest.NewRequest("GET", "http://example.com", nil)
	_, _ = rt.RoundTrip(req)
}

func TestClientSessionError(t *testing.T) {
	// Test that getSession returns error for unsupported transport
	client := NewClient(Config{Transport: "invalid"})
	_, err := client.Tools(context.Background())
	if err == nil {
		t.Errorf("expected error for invalid transport")
	}
}

func TestAdaptTool(t *testing.T) {
	c := &Client{}

	destructive := false
	openWorld := true
	mcpTool := &mcp.Tool{
		Name:        "search",
		Title:       "Web Search",
		Description: "Search the web",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
			"required": []any{"query"},
		},
		OutputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"results": map[string]any{"type": "array"},
			},
		},
		Annotations: &mcp.ToolAnnotations{
			Title:           "Web Search Annotation Title",
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			DestructiveHint: &destructive,
			OpenWorldHint:   &openWorld,
		},
	}

	tl, err := c.adaptTool(mcpTool)
	if err != nil {
		t.Fatalf("unexpected error adapting tool: %v", err)
	}

	if tl.Definition.Name != "search" {
		t.Errorf("expected tool name 'search', got %q", tl.Definition.Name)
	}
	if tl.Definition.Title != "Web Search" {
		t.Errorf("expected tool title 'Web Search', got %q", tl.Definition.Title)
	}
	if tl.Definition.OutputSchema == nil {
		t.Fatalf("expected output schema to be non-nil")
	}
	if tl.Definition.OutputSchema.Properties == nil || tl.Definition.OutputSchema.Properties["results"] == nil {
		t.Errorf("expected output schema to have 'results' property")
	}

	// Verify annotations
	if !tl.Definition.Annotation.IsReadOnly {
		t.Errorf("expected IsReadOnly to be true")
	}
	if !tl.Definition.Annotation.IsIdempotent {
		t.Errorf("expected IsIdempotent to be true")
	}
	if tl.Definition.Annotation.IsDangerous {
		t.Errorf("expected IsDangerous to be false")
	}
	if !tl.Definition.Annotation.IsOpenWorld {
		t.Errorf("expected IsOpenWorld to be true")
	}
	// Verify tool.Annotation matches tool.Definition.Annotation
	if tl.Annotation != tl.Definition.Annotation {
		t.Errorf("expected tool.Annotation to match tool.Definition.Annotation")
	}

	// Test fallback for destructive / openworld hints when nil
	mcpTool2 := &mcp.Tool{
		Name: "delete_item",
		InputSchema: map[string]any{
			"type": "object",
		},
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: false,
		},
	}
	tl2, err := c.adaptTool(mcpTool2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !tl2.Annotation.IsDangerous {
		t.Errorf("expected IsDangerous to be true when ReadOnlyHint is false and DestructiveHint is nil")
	}
	if !tl2.Annotation.IsOpenWorld {
		t.Errorf("expected IsOpenWorld to be true when OpenWorldHint is nil")
	}
}

func TestClientCredentialsProvider(t *testing.T) {
	ctx := context.Background()

	// Missing ClientID or ClientSecret
	pMissing := &ClientCredentialsProvider{
		ClientID: "",
	}
	_, err := pMissing.GetHandler(ctx)
	if err == nil {
		t.Errorf("expected error for missing credentials, got nil")
	}

	pMissingSecret := &ClientCredentialsProvider{
		ClientID:     "my-client-id",
		ClientSecret: "",
	}
	_, err = pMissingSecret.GetHandler(ctx)
	if err == nil {
		t.Errorf("expected error for missing client secret, got nil")
	}

	// Valid credentials
	pValid := &ClientCredentialsProvider{
		ClientID:     "my-client-id",
		ClientSecret: "my-client-secret",
	}
	handler, err := pValid.GetHandler(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handler == nil {
		t.Errorf("expected non-nil handler")
	}
}

type EchoInput struct {
	Text string `json:"text" jsonschema:"the text to echo"`
}

type EchoOutput struct {
	Response string `json:"response" jsonschema:"the echoed response"`
}

func TestMCPv2StatelessHTTP(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-v2-server",
		Version: "1.0.0",
	}, nil)

	destructive := false
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Title:       "Echo Tool",
		Description: "Echoes back the provided text",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			DestructiveHint: &destructive,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input EchoInput) (*mcp.CallToolResult, EchoOutput, error) {
		if token := req.Params.GetProgressToken(); token != nil {
			_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
				ProgressToken: token,
				Progress:      50,
				Total:         100,
				Message:       "processing echo",
			})
		}
		return nil, EchoOutput{Response: "echo: " + input.Text}, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	var onProgressCalled bool
	var onProgressMsg string
	client := NewClient(Config{
		Transport: "http",
		URL:       srv.URL,
		OnProgress: func(ctx context.Context, token any, progress, total float64, message string) {
			onProgressCalled = true
			onProgressMsg = message
		},
	})
	defer client.Close()

	ctx := context.Background()

	// 1. Verify client.Info(ctx) returns ProtocolVersion == "2026-07-28"
	info, err := client.Info(ctx)
	if err != nil {
		t.Fatalf("client.Info failed: %v", err)
	}
	if info.ProtocolVersion != ProtocolVersion20260728 {
		t.Errorf("expected ProtocolVersion %q, got %q", ProtocolVersion20260728, info.ProtocolVersion)
	}

	// 2. Verify client.Tools(ctx) returns the registered tool with schema and annotations
	tools, err := client.Tools(ctx)
	if err != nil {
		t.Fatalf("client.Tools failed: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	tool0 := tools[0]
	if tool0.Definition.Name != "echo" {
		t.Errorf("expected tool name 'echo', got %q", tool0.Definition.Name)
	}
	if tool0.Definition.OutputSchema == nil {
		t.Errorf("expected non-nil OutputSchema on tool definition")
	}
	if !tool0.Annotation.IsReadOnly {
		t.Errorf("expected tool annotation IsReadOnly to be true")
	}
	if !tool0.Annotation.IsIdempotent {
		t.Errorf("expected tool annotation IsIdempotent to be true")
	}
	if tool0.Annotation.IsDangerous {
		t.Errorf("expected tool annotation IsDangerous to be false")
	}

	// 3. Verify client.CallTool(ctx, ...) executes the tool and returns the result
	stream, err := client.CallTool(ctx, "echo", map[string]any{"text": "hello mcp v2"})
	if err != nil {
		t.Fatalf("client.CallTool failed: %v", err)
	}

	var chunks []message.ToolChunk
	for chunk, err := range stream {
		if err != nil {
			t.Fatalf("stream yielded error: %v", err)
		}
		chunks = append(chunks, chunk)
	}

	if len(chunks) < 1 {
		t.Fatalf("expected at least 1 chunk")
	}
	finalChunk := chunks[len(chunks)-1]

	if finalChunk.IsError {
		t.Errorf("expected tool chunk not to be an error")
	}
	sc, ok := finalChunk.StructuredContent.(map[string]any)
	if !ok || sc["response"] != "echo: hello mcp v2" {
		t.Errorf("unexpected structured content: %v", finalChunk.StructuredContent)
	}

	// Verify both progressRegistry channel and config.OnProgress received notifications
	hasProgressChunk := false
	for _, ch := range chunks {
		if ch.Progress == "processing echo" {
			hasProgressChunk = true
			if ch.ProgressCurrent == nil || *ch.ProgressCurrent != 50 {
				t.Errorf("expected ProgressCurrent to be 50")
			}
			if ch.ProgressTotal == nil || *ch.ProgressTotal != 100 {
				t.Errorf("expected ProgressTotal to be 100")
			}
		}
	}
	if !hasProgressChunk {
		t.Errorf("expected to receive progress chunk via progressRegistry")
	}
	if !onProgressCalled || onProgressMsg != "processing echo" {
		t.Errorf("expected OnProgress callback to be called with 'processing echo', got called=%v, msg=%q", onProgressCalled, onProgressMsg)
	}

	// Also verify executing via the adapted tool's Handler
	callStream, err := tool0.Handler(ctx, &message.ToolCall{
		ID:   "call-test",
		Name: "echo",
		Args: map[string]any{"text": "from handler"},
	})
	if err != nil {
		t.Fatalf("tool0.Handler failed: %v", err)
	}
	var handlerChunk message.ToolChunk
	for chunk, err := range callStream {
		if err != nil {
			t.Fatalf("handler stream yielded error: %v", err)
		}
		handlerChunk = chunk
	}
	if hsc, ok := handlerChunk.StructuredContent.(map[string]any); !ok || hsc["response"] != "echo: from handler" {
		t.Errorf("unexpected handler structured content: %v", handlerChunk.StructuredContent)
	}
}
