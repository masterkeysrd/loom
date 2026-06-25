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
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/telemetry"
	"github.com/masterkeysrd/loom/tool"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/semconv/v1.41.0/rpcconv"
	"go.opentelemetry.io/otel/trace"
)

// Icon provides a visual identifier for a server.
type Icon struct {
	Source   string
	MIMEType string
	Sizes    []string
	Theme    string
}

// PromptArgument describes an argument that a prompt can accept.
type PromptArgument struct {
	Name        string
	Title       string
	Description string
	Required    bool
}

// Prompt represents a prompt or prompt template offered by the server.
type Prompt struct {
	Name        string
	Title       string
	Description string
	Arguments   []PromptArgument
	Icons       []Icon
	Meta        map[string]any
}

// Annotations provides optional hints to the client.
type Annotations struct {
	Audience     []string
	LastModified string
	Priority     float64
}

// Resource represents a specific resource available on the server.
type Resource struct {
	Name        string
	Title       string
	Description string
	MIMEType    string
	Size        int64
	URI         string
	Icons       []Icon
	Meta        map[string]any
	Annotations *Annotations
}

// ResourceTemplate represents a template for a parameterized resource.
type ResourceTemplate struct {
	Name        string
	Title       string
	Description string
	MIMEType    string
	URITemplate string
	Icons       []Icon
	Meta        map[string]any
	Annotations *Annotations
}

// CompleteReference represents the context for an autocompletion request.
type CompleteReference struct {
	Type string // "ref/prompt" or "ref/resource"
	Name string // Used when Type is "ref/prompt"
	URI  string // Used when Type is "ref/resource"
}

// CompleteResult holds the autocomplete suggestions from the server.
type CompleteResult struct {
	Values  []string
	HasMore bool
	Total   int
}

// ServerInfo represents the initialization metadata from an MCP server.
type ServerInfo struct {
	Name            string
	Title           string
	Version         string
	WebsiteURL      string
	Icons           []Icon
	ProtocolVersion string
	Instructions    string
	Meta            map[string]any
	Capabilities    ServerCapabilities
}

// ServerCapabilities describes the capabilities a server supports.
type ServerCapabilities struct {
	Experimental map[string]any
	Extensions   map[string]any
	Completions  bool
	Logging      bool
	Prompts      *PromptCapabilities
	Resources    *ResourceCapabilities
	Tools        *ToolCapabilities
}

// PromptCapabilities describes the server's support for prompts.
type PromptCapabilities struct {
	ListChanged bool
}

// ResourceCapabilities describes the server's support for resources.
type ResourceCapabilities struct {
	ListChanged bool
	Subscribe   bool
}

// ToolCapabilities describes the server's support for tools.
type ToolCapabilities struct {
	ListChanged bool
}

// Status represents the current connection state of an MCP server.
type Status string

const (
	StatusDisconnected Status = "disconnected"
	StatusConnecting   Status = "connecting"
	StatusConnected    Status = "connected"
	StatusError        Status = "error"
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

	// Callbacks for dynamic updates from the server
	OnToolsChanged     func(ctx context.Context)                                                             `json:"-"`
	OnPromptsChanged   func(ctx context.Context)                                                             `json:"-"`
	OnResourcesChanged func(ctx context.Context)                                                             `json:"-"`
	OnResourceUpdated  func(ctx context.Context, uri string)                                                 `json:"-"`
	OnLogMessage       func(ctx context.Context, level string, logger string, data any)                      `json:"-"`
	OnProgress         func(ctx context.Context, token any, progress float64, total float64, message string) `json:"-"`
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

	mu          sync.RWMutex
	mcpSession  *mcp.ClientSession
	status      Status
	lastErr     error
	idleTimer   *time.Timer
	idleTimeout time.Duration
}

// NewClient creates a new Client with the given configuration.
func NewClient(config Config) *Client {
	c := &Client{
		config:      config,
		status:      StatusDisconnected,
		idleTimeout: 5 * time.Minute,
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

	if config.OnToolsChanged != nil {
		opts.ToolListChangedHandler = func(ctx context.Context, _ *mcp.ToolListChangedRequest) {
			config.OnToolsChanged(ctx)
		}
	}
	if config.OnPromptsChanged != nil {
		opts.PromptListChangedHandler = func(ctx context.Context, _ *mcp.PromptListChangedRequest) {
			config.OnPromptsChanged(ctx)
		}
	}
	if config.OnResourcesChanged != nil {
		opts.ResourceListChangedHandler = func(ctx context.Context, _ *mcp.ResourceListChangedRequest) {
			config.OnResourcesChanged(ctx)
		}
	}
	if config.OnResourceUpdated != nil {
		opts.ResourceUpdatedHandler = func(ctx context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
			config.OnResourceUpdated(ctx, req.Params.URI)
		}
	}
	if config.OnLogMessage != nil {
		opts.LoggingMessageHandler = func(ctx context.Context, req *mcp.LoggingMessageRequest) {
			config.OnLogMessage(ctx, string(req.Params.Level), req.Params.Logger, req.Params.Data)
		}
	}
	if config.OnProgress != nil {
		opts.ProgressNotificationHandler = func(ctx context.Context, req *mcp.ProgressNotificationClientRequest) {
			config.OnProgress(ctx, req.Params.ProgressToken, req.Params.Progress, req.Params.Total, req.Params.Message)
		}
	}

	c.client = mcp.NewClient(&mcp.Implementation{
		Name:    "loom-mcp-client",
		Version: "0.1.0",
	}, opts)

	return c
}

// getSession returns the active session, establishing a new one if necessary.
func (c *Client) getSession(ctx context.Context) (*mcp.ClientSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.status == StatusConnected && c.mcpSession != nil {
		if c.idleTimer != nil {
			c.idleTimer.Reset(c.idleTimeout)
		}
		return c.mcpSession, nil
	}

	c.status = StatusConnecting

	var transport mcp.Transport
	switch c.config.Transport {
	case "stdio":
		cmd := exec.CommandContext(context.Background(), c.config.Command, c.config.Args...)
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
				c.status = StatusError
				c.lastErr = err
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
		err := fmt.Errorf("unsupported transport: %s", c.config.Transport)
		c.status = StatusError
		c.lastErr = err
		return nil, err
	}

	session, err := c.client.Connect(ctx, transport, nil)
	if err != nil {
		c.status = StatusError
		c.lastErr = err
		return nil, err
	}

	c.mcpSession = session
	c.status = StatusConnected
	c.lastErr = nil

	if c.idleTimer != nil {
		c.idleTimer.Stop()
	}
	c.idleTimer = time.AfterFunc(c.idleTimeout, func() {
		_ = c.Close()
	})

	return session, nil
}

// Close explicitly closes the MCP session and stops the idle timer.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.idleTimer != nil {
		c.idleTimer.Stop()
		c.idleTimer = nil
	}

	var err error
	if c.mcpSession != nil {
		err = c.mcpSession.Close()
		c.mcpSession = nil
	}

	c.status = StatusDisconnected
	return err
}

// Restart explicitly closes any active session and establishes a new one.
func (c *Client) Restart(ctx context.Context) error {
	_ = c.Close()
	_, err := c.getSession(ctx)
	return err
}

// Status returns the current connection status and the last error (if any).
func (c *Client) Status() (Status, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status, c.lastErr
}

// Info returns the initialization metadata (capabilities, instructions, etc.) from the server.
func (c *Client) Info(ctx context.Context) (*ServerInfo, error) {
	session, err := c.getSession(ctx)
	if err != nil {
		return nil, err
	}

	res := session.InitializeResult()
	if res == nil {
		return nil, fmt.Errorf("initialization result not available")
	}

	info := &ServerInfo{
		ProtocolVersion: res.ProtocolVersion,
		Instructions:    res.Instructions,
		Meta:            res.Meta,
	}
	if res.ServerInfo != nil {
		info.Name = res.ServerInfo.Name
		info.Title = res.ServerInfo.Title
		info.Version = res.ServerInfo.Version
		info.WebsiteURL = res.ServerInfo.WebsiteURL
		for _, icon := range res.ServerInfo.Icons {
			info.Icons = append(info.Icons, Icon{
				Source:   icon.Source,
				MIMEType: icon.MIMEType,
				Sizes:    icon.Sizes,
				Theme:    string(icon.Theme),
			})
		}
	}
	if res.Capabilities != nil {
		info.Capabilities.Experimental = res.Capabilities.Experimental
		info.Capabilities.Extensions = res.Capabilities.Extensions
		info.Capabilities.Completions = res.Capabilities.Completions != nil
		info.Capabilities.Logging = res.Capabilities.Logging != nil

		if res.Capabilities.Prompts != nil {
			info.Capabilities.Prompts = &PromptCapabilities{
				ListChanged: res.Capabilities.Prompts.ListChanged,
			}
		}
		if res.Capabilities.Resources != nil {
			info.Capabilities.Resources = &ResourceCapabilities{
				ListChanged: res.Capabilities.Resources.ListChanged,
				Subscribe:   res.Capabilities.Resources.Subscribe,
			}
		}
		if res.Capabilities.Tools != nil {
			info.Capabilities.Tools = &ToolCapabilities{
				ListChanged: res.Capabilities.Tools.ListChanged,
			}
		}
	}

	return info, nil
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

// Tools retrieves the tools available on the MCP server and adapts them to Loom tools.
func (c *Client) Tools(ctx context.Context) ([]*tool.Tool, error) {
	session, err := c.getSession(ctx)
	if err != nil {
		return nil, err
	}

	var tools []*tool.Tool
	for t, err := range session.Tools(ctx, &mcp.ListToolsParams{}) {
		if err != nil {
			return nil, err
		}
		lt, err := c.adaptTool(t)
		if err != nil {
			return nil, err
		}
		tools = append(tools, lt)
	}
	return tools, nil
}

// Prompts retrieves the list of prompts available on the MCP server.
func (c *Client) Prompts(ctx context.Context) ([]Prompt, error) {
	session, err := c.getSession(ctx)
	if err != nil {
		return nil, err
	}

	var prompts []Prompt
	for p, err := range session.Prompts(ctx, &mcp.ListPromptsParams{}) {
		if err != nil {
			return nil, err
		}
		prompt := Prompt{
			Name:        p.Name,
			Title:       p.Title,
			Description: p.Description,
			Meta:        p.Meta,
		}
		for _, arg := range p.Arguments {
			prompt.Arguments = append(prompt.Arguments, PromptArgument{
				Name:        arg.Name,
				Title:       arg.Title,
				Description: arg.Description,
				Required:    arg.Required,
			})
		}
		for _, icon := range p.Icons {
			prompt.Icons = append(prompt.Icons, Icon{
				Source:   icon.Source,
				MIMEType: icon.MIMEType,
				Sizes:    icon.Sizes,
				Theme:    string(icon.Theme),
			})
		}
		prompts = append(prompts, prompt)
	}
	return prompts, nil
}

// mapAnnotations converts MCP annotations to Loom annotations.
func mapAnnotations(a *mcp.Annotations) *Annotations {
	if a == nil {
		return nil
	}
	var audience []string
	for _, r := range a.Audience {
		audience = append(audience, string(r))
	}
	return &Annotations{
		Audience:     audience,
		LastModified: a.LastModified,
		Priority:     a.Priority,
	}
}

// Resources retrieves the list of resources available on the MCP server.
func (c *Client) Resources(ctx context.Context) ([]Resource, error) {
	session, err := c.getSession(ctx)
	if err != nil {
		return nil, err
	}

	var resources []Resource
	for r, err := range session.Resources(ctx, &mcp.ListResourcesParams{}) {
		if err != nil {
			return nil, err
		}
		resource := Resource{
			Name:        r.Name,
			Title:       r.Title,
			Description: r.Description,
			MIMEType:    r.MIMEType,
			Size:        r.Size,
			URI:         r.URI,
			Meta:        r.Meta,
			Annotations: mapAnnotations(r.Annotations),
		}
		for _, icon := range r.Icons {
			resource.Icons = append(resource.Icons, Icon{
				Source:   icon.Source,
				MIMEType: icon.MIMEType,
				Sizes:    icon.Sizes,
				Theme:    string(icon.Theme),
			})
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

// ResourceTemplates retrieves the list of resource templates available on the MCP server.
func (c *Client) ResourceTemplates(ctx context.Context) ([]ResourceTemplate, error) {
	session, err := c.getSession(ctx)
	if err != nil {
		return nil, err
	}

	var templates []ResourceTemplate
	for rt, err := range session.ResourceTemplates(ctx, &mcp.ListResourceTemplatesParams{}) {
		if err != nil {
			return nil, err
		}
		template := ResourceTemplate{
			Name:        rt.Name,
			Title:       rt.Title,
			Description: rt.Description,
			MIMEType:    rt.MIMEType,
			URITemplate: rt.URITemplate,
			Meta:        rt.Meta,
			Annotations: mapAnnotations(rt.Annotations),
		}
		for _, icon := range rt.Icons {
			template.Icons = append(template.Icons, Icon{
				Source:   icon.Source,
				MIMEType: icon.MIMEType,
				Sizes:    icon.Sizes,
				Theme:    string(icon.Theme),
			})
		}
		templates = append(templates, template)
	}
	return templates, nil
}

// GetResources retrieves resources from the server using the pooled session.
func (c *Client) GetResources(ctx context.Context, uris []string) (message.Content, error) {
	session, err := c.getSession(ctx)
	if err != nil {
		return nil, err
	}

	if len(uris) == 0 {
		for r, err := range session.Resources(ctx, &mcp.ListResourcesParams{}) {
			if err != nil {
				return nil, err
			}
			uris = append(uris, r.URI)
		}
	}

	var content message.Content
	for _, uri := range uris {
		res, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			return nil, err
		}
		for _, rc := range res.Contents {
			if block := c.mapResource(rc); block != nil {
				content = append(content, block)
			}
		}
	}
	return content, nil
}

// SubscribeResource subscribes to updates for a specific resource URI.
func (c *Client) SubscribeResource(ctx context.Context, uri string) error {
	session, err := c.getSession(ctx)
	if err != nil {
		return err
	}
	return session.Subscribe(ctx, &mcp.SubscribeParams{URI: uri})
}

// UnsubscribeResource unsubscribes from updates for a specific resource URI.
func (c *Client) UnsubscribeResource(ctx context.Context, uri string) error {
	session, err := c.getSession(ctx)
	if err != nil {
		return err
	}
	return session.Unsubscribe(ctx, &mcp.UnsubscribeParams{URI: uri})
}

// SetLoggingLevel configures the logging level the server should send to the client.
func (c *Client) SetLoggingLevel(ctx context.Context, level string) error {
	session, err := c.getSession(ctx)
	if err != nil {
		return err
	}
	return session.SetLoggingLevel(ctx, &mcp.SetLoggingLevelParams{Level: mcp.LoggingLevel(level)})
}

// Complete requests autocomplete suggestions from the server for prompts or resource templates.
func (c *Client) Complete(ctx context.Context, ref CompleteReference, argName string, argValue string) (*CompleteResult, error) {
	session, err := c.getSession(ctx)
	if err != nil {
		return nil, err
	}

	res, err := session.Complete(ctx, &mcp.CompleteParams{
		Ref: &mcp.CompleteReference{
			Type: ref.Type,
			Name: ref.Name,
			URI:  ref.URI,
		},
		Argument: mcp.CompleteParamsArgument{
			Name:  argName,
			Value: argValue,
		},
	})
	if err != nil {
		return nil, err
	}

	return &CompleteResult{
		Values:  res.Completion.Values,
		HasMore: res.Completion.HasMore,
		Total:   res.Completion.Total,
	}, nil
}

// GetPrompt retrieves a prompt from the server using the pooled session.
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) ([]message.Message, error) {
	session, err := c.getSession(ctx)
	if err != nil {
		return nil, err
	}

	res, err := session.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, err
	}
	return c.mapMessages(res.Messages), nil
}

// CallTool executes a tool on the server using the pooled session.
func (c *Client) CallTool(ctx context.Context, toolName string, args map[string]any) (tool.ToolStream, error) {
	session, err := c.getSession(ctx)
	if err != nil {
		return nil, err
	}

	token := uuid.New().String()
	progressChan := make(chan message.ToolChunk, 10)
	c.progressRegistry.Store(token, progressChan)

	params := &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	}
	params.SetProgressToken(token)

	if params.Meta == nil {
		params.Meta = make(map[string]any)
	}
	otel.GetTextMapPropagator().Inject(ctx, anyMapCarrier(params.Meta))

	return func(yield func(message.ToolChunk, error) bool) {
		defer c.progressRegistry.Delete(token)

		ctx, span := telemetry.Start(ctx, "tools/call", trace.WithSpanKind(trace.SpanKindClient))
		defer span.End()

		span.SetAttributes(
			telemetry.WithRPCMethod("tools/call"),
			attribute.String("rpc.system.name", "jsonrpc"),
			telemetry.WithToolName(toolName),
			attribute.String("loom.tool.type", "mcp"),
		)

		type result struct {
			res *mcp.CallToolResult
			err error
		}
		resChan := make(chan result, 1)
		startTime := time.Now()
		go func() {
			res, err := session.CallTool(ctx, params)
			resChan <- result{res: res, err: err}
		}()

		for {
			select {
			case chunk := <-progressChan:
				if !yield(chunk, nil) {
					return
				}
			case r := <-resChan:
				telemetry.RecordRPCDuration(ctx, time.Since(startTime), rpcconv.SystemNameJSONRPC, "tools/call")
				if r.err != nil {
					span.RecordError(r.err)
					span.SetStatus(codes.Error, r.err.Error())
					yield(message.ToolChunk{}, r.err)
					return
				}
				content, isError := c.mapContent(r.res.Content)
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

func (c *Client) mapMessages(mcpMessages []*mcp.PromptMessage) []message.Message {
	var messages []message.Message
	for _, m := range mcpMessages {
		content, _ := c.mapContent([]mcp.Content{m.Content})
		switch m.Role {
		case "user":
			messages = append(messages, &message.User{Content: content})
		case "assistant":
			messages = append(messages, &message.Assistant{Content: content})
		default:
			if m.Role == "system" {
				messages = append(messages, &message.System{Content: content})
			} else {
				messages = append(messages, &message.User{Content: content})
			}
		}
	}
	return messages
}

func (c *Client) adaptTool(mcpTool *mcp.Tool) (*tool.Tool, error) {
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
		Handler: func(ctx context.Context, call *message.ToolCall) (tool.ToolStream, error) {
			if err := resolvedInput.Validate(call.Args); err != nil {
				return nil, &tool.ValidationError{ToolName: mcpTool.Name, Err: err}
			}
			return c.CallTool(ctx, mcpTool.Name, call.Args)
		},
	}, nil
}

type anyMapCarrier map[string]any

func (c anyMapCarrier) Get(key string) string {
	if v, ok := c[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (c anyMapCarrier) Set(key string, value string) {
	c[key] = value
}

func (c anyMapCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

func (c *Client) mapContent(mcpContent []mcp.Content) (message.Content, bool) {
	var content message.Content
	isError := false
	for _, mc := range mcpContent {
		switch v := mc.(type) {
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
				if block := c.mapResource(v.Resource); block != nil {
					content = append(content, block)
				}
			}
		}
	}
	return content, isError
}

func (c *Client) mapResource(rc *mcp.ResourceContents) message.Block {
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
		return &message.DocumentBlock{Data: rc.Blob, MIMEType: mime, Extras: extras}
	}
	return nil
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

// Close closes all managed MCP sessions.
func (m *MultiClient) Close() error {
	var lastErr error
	for _, c := range m.clients {
		if err := c.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// CloseServer explicitly closes the session for the named MCP server.
func (m *MultiClient) CloseServer(serverName string) error {
	c, ok := m.clients[serverName]
	if !ok {
		return fmt.Errorf("server %q not found", serverName)
	}
	return c.Close()
}

// Restart explicitly closes and re-establishes the session for the named MCP server.
func (m *MultiClient) Restart(ctx context.Context, serverName string) error {
	c, ok := m.clients[serverName]
	if !ok {
		return fmt.Errorf("server %q not found", serverName)
	}
	return c.Restart(ctx)
}

// Status returns the current connection status of the named MCP server.
func (m *MultiClient) Status(serverName string) (Status, error) {
	c, ok := m.clients[serverName]
	if !ok {
		return StatusDisconnected, fmt.Errorf("server %q not found", serverName)
	}
	return c.Status()
}

// Info returns the initialization metadata for the named MCP server.
func (m *MultiClient) Info(ctx context.Context, serverName string) (*ServerInfo, error) {
	c, ok := m.clients[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not found", serverName)
	}
	return c.Info(ctx)
}

// Tools retrieves the tools available on the named MCP server.
func (m *MultiClient) Tools(ctx context.Context, serverName string) ([]*tool.Tool, error) {
	c, ok := m.clients[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not found", serverName)
	}
	return c.Tools(ctx)
}

// Prompts retrieves the list of prompts available on the named MCP server.
func (m *MultiClient) Prompts(ctx context.Context, serverName string) ([]Prompt, error) {
	c, ok := m.clients[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not found", serverName)
	}
	return c.Prompts(ctx)
}

// Resources retrieves the list of resources available on the named MCP server.
func (m *MultiClient) Resources(ctx context.Context, serverName string) ([]Resource, error) {
	c, ok := m.clients[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not found", serverName)
	}
	return c.Resources(ctx)
}

// ResourceTemplates retrieves the list of resource templates available on the named MCP server.
func (m *MultiClient) ResourceTemplates(ctx context.Context, serverName string) ([]ResourceTemplate, error) {
	c, ok := m.clients[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not found", serverName)
	}
	return c.ResourceTemplates(ctx)
}

// GetResources retrieves resources from the named MCP server using a pooled session.
func (m *MultiClient) GetResources(ctx context.Context, serverName string, uris []string) (message.Content, error) {
	c, ok := m.clients[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not found", serverName)
	}
	return c.GetResources(ctx, uris)
}

// SubscribeResource subscribes to updates for a specific resource URI on the named MCP server.
func (m *MultiClient) SubscribeResource(ctx context.Context, serverName string, uri string) error {
	c, ok := m.clients[serverName]
	if !ok {
		return fmt.Errorf("server %q not found", serverName)
	}
	return c.SubscribeResource(ctx, uri)
}

// UnsubscribeResource unsubscribes from updates for a specific resource URI on the named MCP server.
func (m *MultiClient) UnsubscribeResource(ctx context.Context, serverName string, uri string) error {
	c, ok := m.clients[serverName]
	if !ok {
		return fmt.Errorf("server %q not found", serverName)
	}
	return c.UnsubscribeResource(ctx, uri)
}

// SetLoggingLevel configures the logging level for the named MCP server.
func (m *MultiClient) SetLoggingLevel(ctx context.Context, serverName string, level string) error {
	c, ok := m.clients[serverName]
	if !ok {
		return fmt.Errorf("server %q not found", serverName)
	}
	return c.SetLoggingLevel(ctx, level)
}

// Complete requests autocomplete suggestions from the named MCP server.
func (m *MultiClient) Complete(ctx context.Context, serverName string, ref CompleteReference, argName string, argValue string) (*CompleteResult, error) {
	c, ok := m.clients[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not found", serverName)
	}
	return c.Complete(ctx, ref, argName, argValue)
}

// GetPrompt retrieves a prompt from the named MCP server using a pooled session.
func (m *MultiClient) GetPrompt(ctx context.Context, serverName string, promptName string, args map[string]string) ([]message.Message, error) {
	c, ok := m.clients[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not found", serverName)
	}
	return c.GetPrompt(ctx, promptName, args)
}

// CallTool executes a tool on the named MCP server using a pooled session.
func (m *MultiClient) CallTool(ctx context.Context, serverName string, toolName string, args map[string]any) (tool.ToolStream, error) {
	c, ok := m.clients[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not found", serverName)
	}
	return c.CallTool(ctx, toolName, args)
}
