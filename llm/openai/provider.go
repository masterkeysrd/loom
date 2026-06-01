package loomopenai

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"sync"
	"time"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/openai/openai-go/v3"
)

var _ llm.Provider = (*Provider)(nil)

// PromptCache is an extension that configures OpenAI's prompt caching behavior.
type PromptCache struct {
	// Key is a string hint used to optimize cache bucketing
	// for similar requests (e.g. per-user or per-session).
	Key string

	// Retention specifies the retention policy for the prompt
	// cache. Use "24h" to enable extended caching.
	Retention string
}

func (p PromptCache) ExtensionID() string {
	return "openai.prompt_cache"
}

// Provider represents the OpenAI backend client for
// handling chat requests for the Loom application.
type Provider struct {
	client    *openai.Client
	overrides sync.Map
}

// NewDefaultProvider creates a [Provider] using the OpenAI client configured
// from the OPENAI_API_KEY environment variable.
func NewDefaultProvider() (*Provider, error) {
	client := openai.NewClient()
	return &Provider{
		client: &client,
	}, nil
}

// NewProvider creates a [Provider] using the provided OpenAI client.
func NewProvider(client *openai.Client) *Provider {
	return &Provider{
		client: client,
	}
}

// Name returns "openai", uniquely identifying this provider in a [llm.Registry].
func (p *Provider) Name() string {
	return "openai"
}

// Stream converts request into an OpenAI chat request, initiates the streaming
// call, and returns an iterator that yields [message.AssistantChunk] values as
// tokens arrive from the OpenAI server.
func (p *Provider) Stream(ctx context.Context, request *llm.Request) (llm.StreamResponse, error) {
	params, err := toChatCompletionNewParams(request)
	if err != nil {
		return nil, fmt.Errorf("failed to convert chat request: %w", err)
	}

	{
		file, err := os.Create(fmt.Sprintf("./logs/openai_request_%d.json", time.Now().Unix()))
		if err == nil {
			encoder := json.NewEncoder(file)
			encoder.SetIndent("", "  ")
			_ = encoder.Encode(params)
			file.Close()
		}
	}

	return func(yield func(message.AssistantChunk, error) bool) {
		stream := p.client.Chat.Completions.NewStreaming(ctx, params)

		for stream.Next() {
			chunk := stream.Current()
			assistantChunk, err := toAssistantChunk(&chunk)
			if err != nil {
				yield(message.AssistantChunk{}, fmt.Errorf("failed to convert assistant chunk: %w", err))
				return
			}
			if !yield(assistantChunk, nil) {
				return
			}
		}

		if err := stream.Err(); err != nil {
			yield(message.AssistantChunk{}, fmt.Errorf("stream error: %w", err))
		}
	}, nil
}

// ListProfiles returns all known OpenAI model profiles.
func (p *Provider) ListProfiles() []llm.ModelProfile {
	merged := make(map[string]llm.ModelProfile, len(staticProfiles))
	maps.Copy(merged, staticProfiles)
	p.overrides.Range(func(k, v any) bool {
		merged[k.(string)] = v.(llm.ModelProfile)
		return true
	})
	result := make([]llm.ModelProfile, 0, len(merged))
	for _, m := range merged {
		result = append(result, m)
	}
	return result
}

// GetProfile returns the profile for the given model ID.
func (p *Provider) GetProfile(id string) (llm.ModelProfile, bool) {
	if v, ok := p.overrides.Load(id); ok {
		return v.(llm.ModelProfile), true
	}
	m, ok := staticProfiles[id]
	return m, ok
}

// SearchProfiles returns profiles whose ID or DisplayName contains query.
func (p *Provider) SearchProfiles(query string) []llm.ModelProfile {
	return llm.SearchProfiles(p.ListProfiles(), query)
}

// OverrideProfile stores a custom profile for id.
func (p *Provider) OverrideProfile(id string, profile llm.ModelProfile) {
	p.overrides.Store(id, profile)
}

func (p *Provider) GetConfig(modelID string) (llm.ModelConfig, bool) {
	if config, ok := staticConfigs[modelID]; ok {
		return config, ok
	}
	profile, ok := p.GetProfile(modelID)
	if !ok {
		return llm.ModelConfig{}, false
	}
	return llm.ModelConfig{
		MaxTokens: profile.Limits.Context,
	}, true
}
