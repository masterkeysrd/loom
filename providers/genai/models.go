package loomgenai

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/masterkeysrd/loom/profile"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genai"
)

// ListModels queries the Google GenAI models endpoint (automatically paging
// across all results) and returns model profiles enriched with static catalog
// metadata (such as pricing and tiered limits) when available. It does not
// mutate the static ListProfiles catalog.
func (p *Provider) ListModels(ctx context.Context) ([]profile.Profile, error) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("genai.api.type", "models.list"))

	var models []profile.Profile
	for m, err := range p.client.Models.All(ctx) {
		if err != nil {
			return nil, fmt.Errorf("genai: list models: %w", err)
		}
		if m == nil {
			continue
		}
		models = append(models, p.resolveLiveModel(m))
	}
	return models, nil
}

// GetModel queries the Google GenAI models endpoint for a single model by ID
// (accepting either bare IDs like "gemini-2.5-pro" or resource names like
// "models/gemini-2.5-pro") and enriches the result with static catalog or
// override metadata when available.
func (p *Provider) GetModel(ctx context.Context, id string) (profile.Profile, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return profile.Profile{}, errors.New("genai: model id is required")
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("genai.api.type", "models.get"),
		attribute.String("genai.model", id),
	)

	m, err := p.client.Models.Get(ctx, id, nil)
	if err != nil {
		return profile.Profile{}, fmt.Errorf("genai: get model %q: %w", id, err)
	}
	if m == nil {
		return profile.Profile{}, fmt.Errorf("genai: model %q returned empty response", id)
	}
	return p.resolveLiveModel(m), nil
}

func (p *Provider) resolveLiveModel(m *genai.Model) profile.Profile {
	id := normalizeModelID(m.Name)
	if v, ok := p.overrides.Load(id); ok {
		return v.(profile.Profile)
	}
	if m.Name != id {
		if v, ok := p.overrides.Load(m.Name); ok {
			return v.(profile.Profile)
		}
	}

	live := toModelProfile(m)
	if base, ok := staticProfiles[id]; ok {
		if override, hasOverride := staticProfileOverrides[id]; hasOverride {
			base = override(base)
		}
		return profile.Merge(base, live)
	}
	return live
}

func normalizeModelID(name string) string {
	name = strings.TrimPrefix(name, "publishers/google/models/")
	name = strings.TrimPrefix(name, "models/")
	return name
}

func toModelProfile(m *genai.Model) profile.Profile {
	id := normalizeModelID(m.Name)
	name := m.DisplayName
	if name == "" {
		name = id
	}

	lower := strings.ToLower(id)
	var supportsGenerate, supportsEmbed bool
	if len(m.SupportedActions) > 0 {
		supportsGenerate = slices.Contains(m.SupportedActions, "generateContent") ||
			slices.Contains(m.SupportedActions, "bidiGenerateContent")
		supportsEmbed = slices.Contains(m.SupportedActions, "embedContent")
	} else {
		// Vertex AI list responses omit supportedGenerationMethods; infer conservatively.
		supportsEmbed = strings.Contains(lower, "embedding")
		supportsGenerate = !supportsEmbed
	}

	isGeminiChat := supportsGenerate && strings.HasPrefix(lower, "gemini-")

	var inputs, outputs []profile.Modality
	switch {
	case supportsEmbed && !supportsGenerate:
		inputs = []profile.Modality{profile.ModalityText}
		outputs = []profile.Modality{profile.ModalityEmbedding}
	case supportsGenerate:
		inputs = []profile.Modality{profile.ModalityText}
		if isGeminiChat {
			inputs = append(inputs,
				profile.ModalityImage,
				profile.ModalityVideo,
				profile.ModalityAudio,
				profile.ModalityPDF,
			)
		}
		outputs = []profile.Modality{profile.ModalityText}
	}

	return profile.Profile{
		ID:          id,
		Name:        name,
		Description: m.Description,
		Family:      inferGenAIFamily(lower),
		OpenWeights: strings.HasPrefix(lower, "gemma-"),
		Capabilities: profile.Capabilities{
			Completion:  supportsGenerate,
			Embedding:   supportsEmbed,
			Attachment:  isGeminiChat,
			Reasoning:   m.Thinking,
			ToolCall:    supportsGenerate,
			Temperature: m.MaxTemperature > 0 || m.Temperature > 0 || supportsGenerate,
		},
		Limits: profile.Limits{
			Context: int(m.InputTokenLimit),
			Output:  int(m.OutputTokenLimit),
		},
		Modalities: profile.Modalities{
			Inputs:  inputs,
			Outputs: outputs,
		},
	}
}

func inferGenAIFamily(lower string) string {
	switch {
	case strings.Contains(lower, "embedding"):
		return "gemini-embedding"
	case strings.HasPrefix(lower, "gemma-"):
		return "gemma"
	case strings.HasPrefix(lower, "gemini-") && strings.Contains(lower, "flash"):
		return "gemini-flash"
	case strings.HasPrefix(lower, "gemini-") && strings.Contains(lower, "pro"):
		return "gemini-pro"
	case strings.HasPrefix(lower, "gemini-"):
		return "gemini"
	default:
		return ""
	}
}
