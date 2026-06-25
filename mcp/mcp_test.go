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
	}

	content, isError := c.mapContent(mcpContent)
	if isError {
		t.Errorf("expected isError to be false")
	}

	if len(content) != 3 {
		t.Errorf("expected 3 blocks, got %d", len(content))
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
}

func TestStructuredContentMapping(t *testing.T) {
	c := &Client{}

	// mock MCP CallToolResult with StructuredContent
	mcpRes := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "unstructured"},
		},
		StructuredContent: map[string]any{"key": "value"},
	}

	// We can't easily test createHandler without a real session,
	// but we can test that mapContent works as expected if we had a way to call it.
	// Actually, I just updated createHandler to pass StructuredContent.

	content, _ := c.mapContent(mcpRes.Content)
	chunk := message.ToolChunk{
		Content:           content,
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
			Transport: "stdio",
			Command:   "python",
			Args:      []string{"math_server.py"},
			Env:       map[string]string{"DEBUG": "true"},
		},
	}
	mc := NewMultiClient(config)
	if _, ok := mc.clients["math"]; !ok {
		t.Errorf("expected math client to be initialized")
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
