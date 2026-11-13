package loomanthropic

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/masterkeysrd/loom/profile"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ListModels queries the Anthropic /v1/models endpoint (auto-paging across all
// pages) and returns available model profiles enriched with static catalog
// metadata (such as pricing) when available. It does not mutate ListProfiles.
func (p *Provider) ListModels(ctx context.Context) ([]profile.Profile, error) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("anthropic.api.type", "models.list"))

	pager := p.client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{})
	var models []profile.Profile
	for pager.Next() {
		models = append(models, p.resolveLiveModel(pager.Current()))
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("anthropic: list models: %w", err)
	}
	return models, nil
}

// GetModel queries the Anthropic /v1/models/{model_id} endpoint for a single
// model and enriches the returned profile with static catalog or override
// metadata when available.
func (p *Provider) GetModel(ctx context.Context, id string) (profile.Profile, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return profile.Profile{}, errors.New("anthropic: model id is required")
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("anthropic.api.type", "models.get"),
		attribute.String("anthropic.model", id),
	)

	info, err := p.client.Models.Get(ctx, id, anthropic.ModelGetParams{})
	if err != nil {
		return profile.Profile{}, fmt.Errorf("anthropic: get model %q: %w", id, err)
	}
	return p.resolveLiveModel(*info), nil
}

func (p *Provider) resolveLiveModel(info anthropic.ModelInfo) profile.Profile {
	if v, ok := p.overrides.Load(info.ID); ok {
		return v.(profile.Profile)
	}

	live := toModelProfile(info)
	if base, ok := staticProfiles[info.ID]; ok {
		if override, hasOverride := staticProfileOverrides[info.ID]; hasOverride {
			base = override(base)
		}
		return profile.Merge(base, live)
	}
	return live
}

func toModelProfile(info anthropic.ModelInfo) profile.Profile {
	name := info.DisplayName
	if name == "" {
		name = info.ID
	}

	var family string
	if info.Line != "" {
		family = "claude-" + string(info.Line)
	} else if strings.HasPrefix(strings.ToLower(info.ID), "claude") {
		family = "claude"
	}

	var releaseDate string
	if !info.CreatedAt.IsZero() && info.CreatedAt.Year() > 1970 {
		releaseDate = info.CreatedAt.UTC().Format("2006-01-02")
	}

	inputs := []profile.Modality{profile.ModalityText}
	if info.Capabilities.ImageInput.Supported {
		inputs = append(inputs, profile.ModalityImage)
	}
	if info.Capabilities.PDFInput.Supported {
		inputs = append(inputs, profile.ModalityPDF)
	}

	var reasoningOpts []profile.ReasoningOption
	if info.Capabilities.Effort.Supported {
		var levels []string
		if info.Capabilities.Effort.Low.Supported {
			levels = append(levels, "low")
		}
		if info.Capabilities.Effort.Medium.Supported {
			levels = append(levels, "medium")
		}
		if info.Capabilities.Effort.High.Supported {
			levels = append(levels, "high")
		}
		if info.Capabilities.Effort.Xhigh.Supported {
			levels = append(levels, "xhigh")
		}
		if info.Capabilities.Effort.Max.Supported {
			levels = append(levels, "max")
		}
		if len(levels) > 0 {
			reasoningOpts = append(reasoningOpts, profile.ReasoningOption{
				Type:   "effort",
				Values: levels,
			})
		}
	}

	retired := info.Lifecycle == anthropic.ModelInfoLifecycleRetired

	return profile.Profile{
		ID:          info.ID,
		Name:        name,
		Family:      family,
		ReleaseDate: releaseDate,
		Capabilities: profile.Capabilities{
			Completion:       !retired,
			Attachment:       info.Capabilities.ImageInput.Supported || info.Capabilities.PDFInput.Supported,
			Reasoning:        info.Capabilities.Thinking.Supported || info.Capabilities.Effort.Supported,
			ToolCall:         !retired,
			Temperature:      !retired,
			ReasoningOptions: reasoningOpts,
		},
		Limits: profile.Limits{
			Context: int(info.MaxInputTokens),
			Output:  int(info.MaxTokens),
		},
		Modalities: profile.Modalities{
			Inputs:  inputs,
			Outputs: []profile.Modality{profile.ModalityText},
		},
	}
}
