package loomcodex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/masterkeysrd/loom/auth"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	loomopenai "github.com/masterkeysrd/loom/providers/openai"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"golang.org/x/oauth2"
)

const resource = "https://api.openai.com/v1"

var (
	_ llm.Provider    = (*Provider)(nil)
	_ llm.ModelLister = (*Provider)(nil)
)

// Provider streams model responses using an OAuth grant for direct ChatGPT plan usage.
type Provider struct {
	client    *http.Client
	baseURL   string
	overrides sync.Map
	catalog   atomic.Pointer[modelCatalog]
	refresh   chan struct{}
	cacheTTL  time.Duration
}

// NewProvider creates a Codex provider for the selected ChatGPT account.
// source must supply a bearer token granted chatgpt.tokens.use.direct. The optional
// client supplies the HTTP transport and timeouts for Responses and model discovery.
func NewProvider(source oauth2.TokenSource, client *http.Client, opts ...Option) (*Provider, error) {
	if source == nil {
		return nil, auth.ErrAuthRequired
	}
	if client == nil {
		client = http.DefaultClient
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	clientCopy.Transport = &oauth2.Transport{Base: client.Transport, Source: source}

	p := &Provider{
		client:   &clientCopy,
		baseURL:  resource,
		refresh:  make(chan struct{}, 1),
		cacheTTL: time.Hour,
	}

	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// Name returns the Loom registry name "codex".
func (*Provider) Name() string { return "codex" }

// Stream sends the supplied messages and function tools to the public Responses API.
func (p *Provider) Stream(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
	if req == nil || req.Model == "" {
		return nil, errors.New("codex: request must include a model slug")
	}
	var opts loomopenai.ResponsesOptions
	switch ext := req.Extensions[loomopenai.ResponsesExtensionID].(type) {
	case loomopenai.ResponsesOptions:
		opts = ext
	case *loomopenai.ResponsesOptions:
		if ext != nil {
			opts = *ext
		}
	}
	if opts.ToolSearch || opts.CodeInterpreter || opts.FileSearch != nil {
		return nil, errors.New("codex: direct ChatGPT plan usage does not support tool_search, Code Interpreter, or file search")
	}
	for _, t := range req.Tools {
		if t.DeferLoading {
			return nil, fmt.Errorf("codex: tool %q requires unsupported deferred loading", t.Name)
		}
	}
	for _, msg := range req.Messages {
		for _, block := range msg.GetContent() {
			switch block.(type) {
			case *message.AudioBlock, *message.VideoBlock:
				return nil, errors.New("codex: direct ChatGPT plan usage does not support audio or video input")
			case *message.ToolReferenceBlock, *message.ServerToolCall, *message.ServerToolResult:
				return nil, errors.New("codex: direct ChatGPT plan usage does not support tool_search history")
			}
		}
	}
	if prof, ok := p.GetProfile(req.Model); ok {
		if err := prof.ValidateMessagesModalities(p.Name(), req.Model, req.Messages); err != nil {
			return nil, err
		}
		if err := prof.ValidateToolCallCapability(p.Name(), req.Model, len(req.Tools) > 0); err != nil {
			return nil, err
		}
	}
	client := responses.NewResponseService(option.WithBaseURL(p.baseURL), option.WithHTTPClient(p.client), option.WithMaxRetries(0))
	return loomopenai.StreamChatGPTPlanResponses(ctx, &client, req, p.Name(), opts)
}
