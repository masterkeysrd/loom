package loomcopilot

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/masterkeysrd/loom/profile"
	copilot "github.com/microsoft/copilot-sdk/go/v2"
)

// wireSupportedInputModalities lists the input modalities the Copilot SDK can
// carry on the wire: text prompts, image attachments, and PDF document blobs
// (application/pdf). Static models.dev metadata may also advertise Audio/Video
// for some models (e.g. gemini-3.5-flash), but the SDK has no attachment path
// for those, so they are never surfaced on discovered profiles.
var wireSupportedInputModalities = map[profile.Modality]bool{
	profile.ModalityText:  true,
	profile.ModalityImage: true,
	profile.ModalityPDF:   true,
}

func mapModelInfoToProfile(m copilot.Model) profile.Profile {
	name := m.Name
	if name == "" {
		name = m.ID
	}

	var supportsVision, supportsReasoningEffort, supportsThinking bool
	toolCall := true
	if m.Capabilities.Supports != nil {
		if m.Capabilities.Supports.Vision != nil {
			supportsVision = *m.Capabilities.Supports.Vision
		}
		if m.Capabilities.Supports.ReasoningEffort != nil {
			supportsReasoningEffort = *m.Capabilities.Supports.ReasoningEffort
		}
		if m.Capabilities.Supports.Thinking != nil {
			supportsThinking = *m.Capabilities.Supports.Thinking
		}
		if m.Capabilities.Supports.ToolCalls != nil {
			toolCall = *m.Capabilities.Supports.ToolCalls
		}
	}

	inputs := []profile.Modality{profile.ModalityText}
	if supportsVision {
		inputs = append(inputs, profile.ModalityImage)
	}

	caps := profile.Capabilities{
		Completion:  true,
		Decision:    true,
		ToolCall:    toolCall,
		Temperature: false, // Copilot SDK sessions do not expose sampling temperature.
		Reasoning:   supportsReasoningEffort || supportsThinking || len(m.SupportedReasoningEfforts) > 0,
	}
	if len(m.SupportedReasoningEfforts) > 0 {
		var defaultEffort string
		if m.DefaultReasoningEffort != nil {
			defaultEffort = *m.DefaultReasoningEffort
		}
		caps.ReasoningOptions = []profile.ReasoningOption{
			{
				Type:    "effort",
				Values:  slices.Clone(m.SupportedReasoningEfforts),
				Default: defaultEffort,
			},
		}
	}

	var limits profile.Limits
	if m.Capabilities.Limits != nil {
		if m.Capabilities.Limits.MaxContextWindowTokens != nil {
			limits.Context = int(*m.Capabilities.Limits.MaxContextWindowTokens)
		} else if m.Capabilities.Limits.MaxPromptTokens != nil {
			limits.Context = int(*m.Capabilities.Limits.MaxPromptTokens)
		}
		if m.Capabilities.Limits.MaxOutputTokens != nil {
			limits.Output = int(*m.Capabilities.Limits.MaxOutputTokens)
		}
	}

	prof := profile.Profile{
		ID:           m.ID,
		Name:         name,
		Capabilities: caps,
		Limits:       limits,
		Modalities: profile.Modalities{
			Inputs:  inputs,
			Outputs: []profile.Modality{profile.ModalityText},
		},
	}

	if ref, ok := staticProfiles[m.ID]; ok {
		prof = profile.Merge(prof, ref)
		prof.OpenWeights = ref.OpenWeights
		prof.Capabilities.Attachment = prof.Capabilities.Attachment || ref.Capabilities.Attachment
		prof.Capabilities.Reasoning = prof.Capabilities.Reasoning || ref.Capabilities.Reasoning
		prof.Capabilities.Temperature = false // Copilot SDK sessions do not expose sampling temperature.
		for _, mod := range ref.Modalities.Inputs {
			if !wireSupportedInputModalities[mod] {
				continue
			}
			if !slices.Contains(prof.Modalities.Inputs, mod) {
				prof.Modalities.Inputs = append(prof.Modalities.Inputs, mod)
			}
		}
	}

	return prof
}

// ListProfiles returns cached dynamically discovered Copilot profiles merged
// with runtime [Provider.OverrideProfile] entries. Before [Provider.ListModels]
// is called (and when no overrides exist), an empty slice is returned so
// [llm.NewModel] allows any Copilot or BYOK model ID without static rejections.
func (p *Provider) ListProfiles() []profile.Profile {
	byID := make(map[string]profile.Profile)
	p.discovered.Range(func(k, v any) bool {
		byID[k.(string)] = v.(profile.Profile)
		return true
	})
	p.overrides.Range(func(k, v any) bool {
		byID[k.(string)] = v.(profile.Profile)
		return true
	})
	if len(byID) == 0 {
		return nil
	}
	out := make([]profile.Profile, 0, len(byID))
	for _, prof := range byID {
		out = append(out, prof)
	}
	slices.SortFunc(out, func(a, b profile.Profile) int {
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

// GetProfile looks up a model profile by ID, checking runtime overrides first
// and then cached discovered profiles.
func (p *Provider) GetProfile(id string) (profile.Profile, bool) {
	if v, ok := p.overrides.Load(id); ok {
		return v.(profile.Profile), true
	}
	if v, ok := p.discovered.Load(id); ok {
		return v.(profile.Profile), true
	}
	return profile.Profile{}, false
}

// SearchProfiles returns profiles whose ID or Name contains query
// (case-insensitive).
func (p *Provider) SearchProfiles(query string) []profile.Profile {
	return profile.Search(p.ListProfiles(), query)
}

// OverrideProfile registers a custom profile for id, shadowing discovered entries.
func (p *Provider) OverrideProfile(id string, prof profile.Profile) {
	p.overrides.Store(id, prof)
}

// ListModels queries the Copilot SDK runtime (`models.list` or configured
// `OnListModels`) for available models, caches their mapped profiles, and
// returns them with any [Provider.OverrideProfile] entries applied.
func (p *Provider) ListModels(ctx context.Context) ([]profile.Profile, error) {
	if p.mgr == nil {
		return nil, fmt.Errorf("copilot: provider has nil session manager")
	}
	models, err := p.mgr.listModels(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range models {
		p.discovered.Store(m.ID, mapModelInfoToProfile(m))
	}
	return p.ListProfiles(), nil
}

// GetModel retrieves a single model profile by ID, querying [Provider.ListModels]
// if it is not yet in the local override or discovered cache.
func (p *Provider) GetModel(ctx context.Context, id string) (profile.Profile, error) {
	if prof, ok := p.GetProfile(id); ok {
		return prof, nil
	}
	if _, err := p.ListModels(ctx); err != nil {
		return profile.Profile{}, err
	}
	if prof, ok := p.GetProfile(id); ok {
		return prof, nil
	}
	return profile.Profile{}, fmt.Errorf("copilot: model %q not found", id)
}
