package loomcodex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/masterkeysrd/loom/auth"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
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
}

// NewProvider creates a Codex provider for the selected ChatGPT account.
// source must supply a bearer token granted chatgpt.tokens.use.direct. The optional
// client supplies the HTTP transport and timeouts for Responses and model discovery.
func NewProvider(source oauth2.TokenSource, client *http.Client) (*Provider, error) {
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
	return &Provider{client: &clientCopy, baseURL: resource}, nil
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

// ListProfiles returns profiles explicitly overridden for this provider.
// Use [Provider.ListModels] for the currently signed-in account's catalog.
func (p *Provider) ListProfiles() []profile.Profile {
	var result []profile.Profile
	p.overrides.Range(func(_, value any) bool {
		result = append(result, value.(profile.Profile))
		return true
	})
	return result
}

// GetProfile looks up a profile explicitly overridden for this provider.
func (p *Provider) GetProfile(id string) (profile.Profile, bool) {
	value, ok := p.overrides.Load(id)
	if !ok {
		return profile.Profile{}, false
	}
	return value.(profile.Profile), true
}

// SearchProfiles filters overridden profiles by ID or display name.
func (p *Provider) SearchProfiles(query string) []profile.Profile {
	return profile.Search(p.ListProfiles(), query)
}

// OverrideProfile registers model metadata for this provider.
func (p *Provider) OverrideProfile(id string, prof profile.Profile) {
	p.overrides.Store(id, prof)
}

// ListModels returns the selected ChatGPT account's visible model slugs, in server order.
func (p *Provider) ListModels(ctx context.Context) ([]profile.Profile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex: list models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("codex: list models: HTTP %d", resp.StatusCode)
	}
	var catalog struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
		return nil, fmt.Errorf("codex: decode models: %w", err)
	}
	var result []profile.Profile
	for _, model := range catalog.Models {
		if model.Visibility == "list" && model.Slug != "" {
			prof, ok := p.GetProfile(model.Slug)
			if !ok {
				prof = profile.Profile{ID: model.Slug, Name: model.DisplayName, Capabilities: profile.Capabilities{Completion: true}}
			}
			result = append(result, prof)
		}
	}
	return result, nil
}

// GetModel looks up a visible model in the selected ChatGPT account's catalog.
func (p *Provider) GetModel(ctx context.Context, id string) (profile.Profile, error) {
	models, err := p.ListModels(ctx)
	if err != nil {
		return profile.Profile{}, err
	}
	for _, model := range models {
		if model.ID == id {
			return model, nil
		}
	}
	return profile.Profile{}, fmt.Errorf("codex: model %q is not in the account catalog", id)
}
