package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/tool"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Config defines the connection parameters for an MCP server.
type Config struct {
	Transport string            `json:"transport"`         // "stdio" or "http"
	Command   string            `json:"command,omitempty"` // for "stdio"
	Args      []string          `json:"args,omitempty"`    // for "stdio"
	Env       map[string]string `json:"env,omitempty"`     // for "stdio"
	URL       string            `json:"url,omitempty"`     // for "http"
	Headers   map[string]string `json:"headers,omitempty"` // Static headers (e.g., API keys)

	// Auth provides dynamic authentication (OAuth2/Enterprise).
	// This is not serialized to JSON automatically.
	Auth AuthProvider `json:"-"`

	// Elicitation provides interactive user input handling.
	// This is not serialized to JSON automatically.
	Elicitation ElicitationProvider `json:"-"`
}

// AuthProvider defines a standard interface for generating an MCP OAuthHandler.
type AuthProvider interface {
	GetHandler(ctx context.Context) (auth.OAuthHandler, error)
}

// ElicitationProvider defines how the client handles interactive requests
// from the server for user input or actions.
type ElicitationProvider interface {
	// HandleElicit is called when the server requests information (Form)
	// or an action (URL).
	HandleElicit(ctx context.Context, params *mcp.ElicitParams) (*mcp.ElicitResult, error)

	// HandleElicitComplete is called when an out-of-band elicitation (like a URL flow)
	// is finished on the server side.
	HandleElicitComplete(ctx context.Context, params *mcp.ElicitationCompleteParams)
}

// Client represents a client for a single MCP server.
type Client struct {
	config Config
	client *mcp.Client

	progressRegistry sync.Map // map[any]chan message.ToolChunk
}

// NewClient creates a new Client with the given configuration.
func NewClient(config Config) *Client {
	c := &Client{
		config: config,
	}

	opts := &mcp.ClientOptions{
		ProgressNotificationHandler: func(ctx context.Context, req *mcp.ProgressNotificationClientRequest) {
			if ch, ok := c.progressRegistry.Load(req.Params.ProgressToken); ok {
				var total *float64
				if req.Params.Total != 0 {
					t := req.Params.Total
					total = &t
				}
				current := req.Params.Progress
				select {
				case ch.(chan message.ToolChunk) <- message.ToolChunk{
					Progress:        req.Params.Message,
					ProgressCurrent: &current,
					ProgressTotal:   total,
				}:
				default:
					// Drop if full
				}
			}
		},
	}

	if config.Elicitation != nil {
		opts.ElicitationHandler = func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return config.Elicitation.HandleElicit(ctx, req.Params)
		}
		opts.ElicitationCompleteHandler = func(ctx context.Context, req *mcp.ElicitationCompleteNotificationRequest) {
			config.Elicitation.HandleElicitComplete(ctx, req.Params)
		}
	}

	c.client = mcp.NewClient(&mcp.Implementation{
		Name:    "loom-mcp-client",
		Version: "0.1.0",
	}, opts)

	return c
}

// Session creates a new session with the MCP server.
// The caller is responsible for closing the session.
func (c *Client) Session(ctx context.Context) (*SessionClient, error) {
	var transport mcp.Transport
	switch c.config.Transport {
	case "stdio":
		cmd := exec.CommandContext(ctx, c.config.Command, c.config.Args...)
		if len(c.config.Env) > 0 {
			cmd.Env = os.Environ()
			for k, v := range c.config.Env {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
			}
		}
		transport = &mcp.CommandTransport{
			Command: cmd,
		}
	case "http":
		var oauthHandler auth.OAuthHandler
		if c.config.Auth != nil {
			var err error
			oauthHandler, err = c.config.Auth.GetHandler(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get auth handler: %w", err)
			}
		}

		httpClient := http.DefaultClient
		if len(c.config.Headers) > 0 {
			httpClient = &http.Client{
				Transport: &headerRoundTripper{
					headers: c.config.Headers,
					base:    http.DefaultTransport,
				},
			}
		}

		transport = &mcp.StreamableClientTransport{
			Endpoint:     c.config.URL,
			OAuthHandler: oauthHandler,
			HTTPClient:   httpClient,
		}
	default:
		return nil, fmt.Errorf("unsupported transport: %s", c.config.Transport)
	}

	session, err := c.client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}

	return &SessionClient{session: session, parent: c}, nil
}

type headerRoundTripper struct {
	headers map[string]string
	base    http.RoundTripper
}

func (t *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}

// GetResources retrieves resources from the server using a transient session.
func (c *Client) GetResources(ctx context.Context, uris []string) (message.Content, error) {
	session, err := c.Session(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	return session.GetResources(ctx, uris)
}

// GetPrompt retrieves a prompt from the server using a transient session.
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) ([]message.Message, error) {
	session, err := c.Session(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	return session.GetPrompt(ctx, name, args)
}

// MultiClient manages multiple MCP server clients.
type MultiClient struct {
	clients map[string]*Client
}

// NewMultiClient creates a new MultiClient with the given configurations.
func NewMultiClient(configs map[string]Config) *MultiClient {
	clients := make(map[string]*Client)
	for name, config := range configs {
		clients[name] = NewClient(config)
	}
	return &MultiClient{clients: clients}
}

// Session creates a new session with the named MCP server.
func (m *MultiClient) Session(ctx context.Context, serverName string) (*SessionClient, error) {
	c, ok := m.clients[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not found", serverName)
	}
	return c.Session(ctx)
}

// GetResources retrieves resources from the named MCP server using a transient session.
func (m *MultiClient) GetResources(ctx context.Context, serverName string, uris []string) (message.Content, error) {
	c, ok := m.clients[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not found", serverName)
	}
	return c.GetResources(ctx, uris)
}

// GetPrompt retrieves a prompt from the named MCP server using a transient session.
func (m *MultiClient) GetPrompt(ctx context.Context, serverName string, promptName string, args map[string]string) ([]message.Message, error) {
	c, ok := m.clients[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not found", serverName)
	}
	return c.GetPrompt(ctx, promptName, args)
}

// SessionClient provides session-scoped access to an MCP server.
type SessionClient struct {
	session *mcp.ClientSession
	parent  *Client
}

// Close closes the underlying MCP session and its transport.
func (s *SessionClient) Close() error {
	return s.session.Close()
}

// Tools retrieves the tools available on the MCP server and adapts them to Loom tools.
func (s *SessionClient) Tools(ctx context.Context) ([]*tool.Tool, error) {
	res, err := s.session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, err
	}

	var tools []*tool.Tool
	for _, t := range res.Tools {
		lt, err := s.adaptTool(t)
		if err != nil {
			return nil, err
		}
		tools = append(tools, lt)
	}
	return tools, nil
}

// GetResources retrieves resources from the server.
func (s *SessionClient) GetResources(ctx context.Context, uris []string) (message.Content, error) {
	if len(uris) == 0 {
		res, err := s.session.ListResources(ctx, &mcp.ListResourcesParams{})
		if err != nil {
			return nil, err
		}
		for _, r := range res.Resources {
			uris = append(uris, r.URI)
		}
	}

	var content message.Content
	for _, uri := range uris {
		res, err := s.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			return nil, err
		}
		for _, rc := range res.Contents {
			if block := s.mapResource(rc); block != nil {
				content = append(content, block)
			}
		}
	}
	return content, nil
}

// GetPrompt retrieves a prompt from the server.
func (s *SessionClient) GetPrompt(ctx context.Context, name string, args map[string]string) ([]message.Message, error) {
	res, err := s.session.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, err
	}
	return s.mapMessages(res.Messages), nil
}

func (s *SessionClient) mapMessages(mcpMessages []*mcp.PromptMessage) []message.Message {
	var messages []message.Message
	for _, m := range mcpMessages {
		content, _ := s.mapContent([]mcp.Content{m.Content})
		switch m.Role {
		case "user":
			messages = append(messages, &message.User{Content: content})
		case "assistant":
			messages = append(messages, &message.Assistant{Content: content})
		default:
			// Fallback to user if role is unknown or if it's "system" (Loom has System message too)
			if m.Role == "system" {
				messages = append(messages, &message.System{Content: content})
			} else {
				messages = append(messages, &message.User{Content: content})
			}
		}
	}
	return messages
}

func (s *SessionClient) adaptTool(mcpTool *mcp.Tool) (*tool.Tool, error) {
	data, err := json.Marshal(mcpTool.InputSchema)
	if err != nil {
		return nil, err
	}
	var inputSchema jsonschema.Schema
	if err := json.Unmarshal(data, &inputSchema); err != nil {
		return nil, err
	}

	resolvedInput, err := inputSchema.Resolve(nil)
	if err != nil {
		return nil, err
	}

	title := mcpTool.Title
	if title == "" && mcpTool.Annotations != nil {
		title = mcpTool.Annotations.Title
	}
	if title == "" {
		title = mcpTool.Name
	}

	return &tool.Tool{
		Definition: tool.Definition{
			Name:        mcpTool.Name,
			Title:       title,
			Description: mcpTool.Description,
			InputSchema: &inputSchema,
		},
		Handler: s.createHandler(mcpTool.Name, resolvedInput),
	}, nil
}

func (s *SessionClient) createHandler(name string, schema *jsonschema.Resolved) tool.ToolHandler {
	return func(ctx context.Context, call *message.ToolCall) (tool.ToolStream, error) {
		if err := schema.Validate(call.Args); err != nil {
			return nil, &tool.ValidationError{ToolName: name, Err: err}
		}

		token := uuid.New().String()
		progressChan := make(chan message.ToolChunk, 10)
		s.parent.progressRegistry.Store(token, progressChan)
		defer s.parent.progressRegistry.Delete(token)

		params := &mcp.CallToolParams{
			Name:      name,
			Arguments: call.Args,
		}
		params.SetProgressToken(token)

		return func(yield func(message.ToolChunk, error) bool) {
			type result struct {
				res *mcp.CallToolResult
				err error
			}
			resChan := make(chan result, 1)
			go func() {
				res, err := s.session.CallTool(ctx, params)
				resChan <- result{res: res, err: err}
			}()

			for {
				select {
				case chunk := <-progressChan:
					if !yield(chunk, nil) {
						return
					}
				case r := <-resChan:
					if r.err != nil {
						yield(message.ToolChunk{}, r.err)
						return
					}
					content, isError := s.mapContent(r.res.Content)
					yield(message.ToolChunk{
						Content:           content,
						StructuredContent: r.res.StructuredContent,
						IsError:           isError || r.res.IsError,
					}, nil)
					return
				case <-ctx.Done():
					yield(message.ToolChunk{}, ctx.Err())
					return
				}
			}
		}, nil
	}
}

func (s *SessionClient) mapContent(mcpContent []mcp.Content) (message.Content, bool) {
	var content message.Content
	isError := false
	for _, c := range mcpContent {
		switch v := c.(type) {
		case *mcp.TextContent:
			content = append(content, &message.TextBlock{Text: v.Text})
		case *mcp.ImageContent:
			content = append(content, &message.ImageBlock{
				Data:     v.Data,
				MIMEType: v.MIMEType,
			})
		case *mcp.AudioContent:
			content = append(content, &message.AudioBlock{
				Data:     v.Data,
				MIMEType: v.MIMEType,
			})
		case *mcp.EmbeddedResource:
			if v.Resource != nil {
				if block := s.mapResource(v.Resource); block != nil {
					content = append(content, block)
				}
			}
		}
	}
	return content, isError
}

func (s *SessionClient) mapResource(rc *mcp.ResourceContents) message.Block {
	extras := map[string]any{"uri": rc.URI}
	if rc.Text != "" {
		return &message.TextBlock{
			Text:   rc.Text,
			Extras: extras,
		}
	}
	if len(rc.Blob) > 0 {
		mime := rc.MIMEType
		if strings.HasPrefix(mime, "image/") {
			return &message.ImageBlock{Data: rc.Blob, MIMEType: mime, Extras: extras}
		}
		if strings.HasPrefix(mime, "audio/") {
			return &message.AudioBlock{Data: rc.Blob, MIMEType: mime, Extras: extras}
		}
		if strings.HasPrefix(mime, "video/") {
			return &message.VideoBlock{Data: rc.Blob, MIMEType: mime, Extras: extras}
		}
		// Fallback to DocumentBlock for all other binary data (PDF, Office, etc.)
		return &message.DocumentBlock{Data: rc.Blob, MIMEType: mime, Extras: extras}
	}
	return nil
}
