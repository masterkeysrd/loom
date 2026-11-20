package loomcodex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
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
	opts.Store = new(bool)
	params, err := loomopenai.EncodeResponsesRequest(req, opts, responses.EasyInputMessageRoleDeveloper)
	if err != nil {
		return nil, fmt.Errorf("codex: encode request: %w", err)
	}
	clientOptions := []option.RequestOption{option.WithBaseURL(p.baseURL), option.WithHTTPClient(p.client), option.WithMaxRetries(0)}
	id := requestSessionID(req)
	if id != "" {
		clientOptions = append(clientOptions, option.WithHeader("session-id", id))
	}
	client := responses.NewResponseService(clientOptions...)
	stream := loomopenai.StreamResponsesRequest(ctx, &client, adaptRequest(params), p.Name(), true)
	return func(yield func(message.AssistantChunk, error) bool) {
		for chunk, err := range stream {
			if err == nil && id != "" {
				if chunk.Metadata == nil {
					chunk.Metadata = make(map[string]any)
				}
				chunk.Metadata[SessionIDKey] = id
			}
			if !yield(chunk, err) {
				return
			}
		}
	}, nil
}

// SessionIDKey identifies the conversation's cache session on assistant messages and chunks.
const SessionIDKey = "codex.session_id"

func requestSessionID(req *llm.Request) string {
	switch session := req.Extensions[SessionIDKey].(type) {
	case SessionID:
		return string(session)
	case *SessionID:
		if session != nil {
			return string(*session)
		}
	}
	for _, msg := range slices.Backward(req.Messages) {
		if assistant, ok := msg.(*message.Assistant); ok {
			if id, ok := assistant.Metadata[SessionIDKey].(string); ok && id != "" {
				return id
			}
		}
	}
	return uuid.NewString()
}

// SessionID optionally overrides the inferred session for an inference call.
// An empty SessionID omits the header for that call.
type SessionID string

// ExtensionID identifies the Codex session override.
func (SessionID) ExtensionID() string { return SessionIDKey }
