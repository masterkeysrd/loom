package loomopenai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/masterkeysrd/loom/profile"
	"github.com/openai/openai-go/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ListModels queries the OpenAI /v1/models endpoint and returns available model
// profiles enriched with static catalog metadata (such as pricing, limits, and
// modalities) when available. It does not mutate the static ListProfiles catalog.
func (p *Provider) ListModels(ctx context.Context) ([]profile.Profile, error) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("openai.api.type", "models.list"))

	pager := p.client.Models.ListAutoPaging(ctx)
	var models []profile.Profile
	for pager.Next() {
		models = append(models, p.resolveLiveModel(pager.Current()))
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("openai: list models: %w", err)
	}
	return models, nil
}

// GetModel queries the OpenAI /v1/models/{model} endpoint for a single model and
// enriches the returned profile with static catalog or override metadata when available.
func (p *Provider) GetModel(ctx context.Context, id string) (profile.Profile, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return profile.Profile{}, errors.New("openai: model id is required")
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("openai.api.type", "models.get"),
		attribute.String("openai.model", id),
	)

	m, err := p.client.Models.Get(ctx, id)
	if err != nil {
		return profile.Profile{}, fmt.Errorf("openai: get model %q: %w", id, err)
	}
	return p.resolveLiveModel(*m), nil
}

func (p *Provider) resolveLiveModel(m openai.Model) profile.Profile {
	if v, ok := p.overrides.Load(m.ID); ok {
		return v.(profile.Profile)
	}

	live := toModelProfile(m)
	if base, ok := staticProfiles[m.ID]; ok {
		if override, hasOverride := staticProfileOverrides[m.ID]; hasOverride {
			base = override(base)
		}
		return profile.Merge(base, live)
	}
	return live
}

func toModelProfile(m openai.Model) profile.Profile {
	prof := profile.Profile{
		ID:     m.ID,
		Name:   m.ID,
		Family: inferOpenAIFamily(m.ID),
	}

	if m.Created > 0 {
		prof.ReleaseDate = time.Unix(m.Created, 0).UTC().Format("2006-01-02")
	}

	lower := strings.ToLower(m.ID)
	switch {
	case strings.Contains(lower, "embedding"):
		prof.Capabilities = profile.Capabilities{
			Embedding: true,
		}
		prof.Modalities = profile.Modalities{
			Inputs:  []profile.Modality{profile.ModalityText},
			Outputs: []profile.Modality{profile.ModalityEmbedding},
		}
	case isNonChatMediaModel(lower):
		// Leave Completion/ToolCall false for dedicated audio, image, video, or moderation endpoints.
	default:
		prof.Capabilities = profile.Capabilities{
			Completion:  true,
			ToolCall:    true,
			Temperature: true,
		}
		prof.Modalities = profile.Modalities{
			Inputs:  []profile.Modality{profile.ModalityText},
			Outputs: []profile.Modality{profile.ModalityText},
		}
	}

	return prof
}

func inferOpenAIFamily(id string) string {
	lower := strings.ToLower(id)
	switch {
	case strings.Contains(lower, "embedding"):
		return "text-embedding"
	case strings.HasPrefix(lower, "gpt-image") || strings.HasPrefix(lower, "dall-e") || strings.HasPrefix(lower, "chatgpt-image"):
		return "gpt-image"
	case strings.HasPrefix(lower, "gpt-") || strings.HasPrefix(lower, "ft:gpt-") || strings.HasPrefix(lower, "chatgpt-"):
		return "gpt"
	case strings.HasPrefix(lower, "o1") || strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "o4"):
		return "o-series"
	default:
		return ""
	}
}

func isNonChatMediaModel(lower string) bool {
	return strings.HasPrefix(lower, "tts-") ||
		strings.HasPrefix(lower, "whisper-") ||
		strings.HasPrefix(lower, "dall-e") ||
		strings.HasPrefix(lower, "gpt-image") ||
		strings.HasPrefix(lower, "sora") ||
		strings.Contains(lower, "moderation")
}
