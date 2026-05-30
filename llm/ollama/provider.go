package loomollama

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
	"github.com/ollama/ollama/api"
)

var _ llm.Provider = (*Provider)(nil)

// Provider represents the Ollama backend client for
// handling chat requests for the TaskSmith application.
type Provider struct {
	client    *api.Client
	overrides sync.Map
}

// NewDefaultProvider creates a [Provider] using the Ollama client configured
// from environment variables (OLLAMA_HOST, etc.).
func NewDefaultProvider() (*Provider, error) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, fmt.Errorf("failed to create Ollama client: %w", err)
	}

	return &Provider{client: client}, nil
}

// Name returns "ollama", uniquely identifying this provider in a [llm.Registry].
func (p *Provider) Name() string {
	return "ollama"
}

// Stream converts request into an Ollama chat request, initiates the streaming
// call, and returns an iterator that yields [message.AssistantChunk] values as
// tokens arrive from the Ollama server.
func (p *Provider) Stream(ctx context.Context, request *llm.Request) (llm.StreamResponse, error) {
	ollamaRequest, err := toChatRequest(request)
	if err != nil {
		return nil, fmt.Errorf("failed to convert chat request: %w", err)
	}

	{
		file, err := os.Create(fmt.Sprintf("./logs/ollama_request_%d.json", time.Now().Unix()))
		if err != nil {
			return nil, fmt.Errorf("failed to create debug file: %w", err)
		}
		defer file.Close()

		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(ollamaRequest); err != nil {
			return nil, fmt.Errorf("failed to write debug file: %w", err)
		}
	}

	return func(yield func(message.AssistantChunk, error) bool) {
		callback := func(resp api.ChatResponse) error {
			chunk, err := toAssistantChunk(resp)
			if err != nil {
				return fmt.Errorf("failed to convert chat response: %w", err)
			}
			if !yield(chunk, nil) {
				return fmt.Errorf("streaming stopped by consumer")
			}
			return nil
		}

		if err := p.client.Chat(ctx, ollamaRequest, callback); err != nil {
			yield(message.AssistantChunk{}, fmt.Errorf("failed to start chat stream: %w", err))
		}
	}, nil
}

// ListProfiles returns all known Ollama model profiles. The static catalog is
// empty by default (models are user-installed), so only runtime overrides are
// returned. NewModel skips validation when the slice is empty.
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

// GetProfile returns the profile for the given model ID, checking overrides
// before falling back to the generated static catalog.
func (p *Provider) GetProfile(id string) (llm.ModelProfile, bool) {
	if v, ok := p.overrides.Load(id); ok {
		return v.(llm.ModelProfile), true
	}
	m, ok := staticProfiles[id]
	return m, ok
}

// SearchProfiles returns profiles whose ID or DisplayName contains query
// (case-insensitive).
func (p *Provider) SearchProfiles(query string) []llm.ModelProfile {
	return llm.SearchProfiles(p.ListProfiles(), query)
}

// OverrideProfile stores a custom profile for id, shadowing any static entry.
// This is also the mechanism for registering locally-pulled Ollama models so
// that profile metadata (context window, tool support) is available at runtime.
func (p *Provider) OverrideProfile(id string, profile llm.ModelProfile) {
	p.overrides.Store(id, profile)
}

func (p *Provider) GetConfig(id string) (llm.ModelConfig, bool) {
	profile, ok := p.GetProfile(id)
	if !ok {
		return llm.ModelConfig{}, false
	}
	return llm.ModelConfig{
		MaxTokens: profile.Limits.Context,
	}, true
}
