package loomanthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
)

var _ llm.Provider = (*Provider)(nil)

// Provider represents the Anthropic backend client for
// handling chat requests for the Loom application.
//
//go:generate loom-gen llm-profiles -provider=anthropic -out=profiles.gen.go -pkg=loomanthropic
type Provider struct {
	client    *anthropic.Client
	overrides sync.Map
}

// NewDefaultProvider creates a [Provider] using the Anthropic client configured
// from the ANTHROPIC_API_KEY environment variable.
func NewDefaultProvider() (*Provider, error) {
	client := anthropic.NewClient()
	return &Provider{
		client: &client,
	}, nil
}

// NewProvider creates a [Provider] using the provided Anthropic client. This can be used to
// share a client across multiple providers, or to use a client with custom configuration.
func NewProvider(client *anthropic.Client) *Provider {
	return &Provider{
		client: client,
	}
}

// Name returns "anthropic", uniquely identifying this provider in a [llm.Registry].
func (p *Provider) Name() string {
	return "anthropic"
}

// Stream converts request into an Anthropic chat request, initiates the streaming
// call, and returns an iterator that yields [message.AssistantChunk] values as
// tokens arrive from the Anthropic server.
func (p *Provider) Stream(ctx context.Context, request *llm.Request) (llm.StreamResponse, error) {
	body, err := toMessageNewParams(request)
	if err != nil {
		return nil, fmt.Errorf("failed to convert chat request: %w", err)
	}

	{
		file, err := os.Create(fmt.Sprintf("./logs/anthropic_request_%d.json", time.Now().Unix()))
		if err != nil {
			return nil, fmt.Errorf("failed to create debug file: %w", err)
		}
		defer file.Close()

		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(body); err != nil {
			return nil, fmt.Errorf("failed to write debug file: %w", err)
		}
	}

	return func(yield func(message.AssistantChunk, error) bool) {
		// Create the streaming request
		stream := p.client.Messages.NewStreaming(ctx, body)

		for stream.Next() {
			event := stream.Current()

			switch e := event.AsAny().(type) {
			case anthropic.ContentBlockStartEvent:
				// Emit the tool call identity chunk so the aggregator can open a pending entry.
				if cb, ok := e.ContentBlock.AsAny().(anthropic.ToolUseBlock); ok {
					chunk := message.AssistantChunk{
						Content: []message.Block{&message.ToolCallChunk{
							Index: int(e.Index),
							ID:    cb.ID,
							Name:  cb.Name,
						}},
					}
					if !yield(chunk, nil) {
						return
					}
				}

			case anthropic.ContentBlockDeltaEvent:
				if delta, ok := e.Delta.AsAny().(anthropic.InputJSONDelta); ok {
					// Emit a partial-args chunk; the aggregator appends it to the right tool call.
					chunk := message.AssistantChunk{
						Content: []message.Block{&message.ToolCallChunk{
							Index:     int(e.Index),
							ArgsChunk: delta.PartialJSON,
						}},
					}
					if !yield(chunk, nil) {
						return
					}
				} else {
					chunk, err := toAssistantChunk(event)
					if err != nil {
						yield(message.AssistantChunk{}, fmt.Errorf("failed to convert event to chunk: %w", err))
						return
					}
					if !yield(chunk, nil) {
						return
					}
				}

			default:
				chunk, err := toAssistantChunk(event)
				if err != nil {
					yield(message.AssistantChunk{}, fmt.Errorf("failed to convert event to chunk: %w", err))
					return
				}
				if !yield(chunk, nil) {
					return
				}
			}
		}

		if stream.Err() != nil {
			yield(message.AssistantChunk{}, fmt.Errorf("failed to start chat stream: %w", stream.Err()))
			return
		}

	}, nil
}

// ListProfiles returns all known Anthropic model profiles, with runtime
// overrides merged on top of the generated static catalog.
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

// OverrideProfile stores a custom profile for id, shadowing any static entry
// with the same ID. Useful for adding unreleased or private model variants at
// runtime without regenerating the static catalog.
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
