package loomtypesafe

import "github.com/masterkeysrd/loom/llm"

const (
	ModelJevLatest  = "jev-latest"
	ModelJevPreview = "jev-preview"
	ModelJev1_14_0  = "jev-1.14.0"
	ModelJev1_13_0  = "jev-1.13.0"
)

// Type aliases for model profile specifications.
type ModelLimits = llm.ProfileLimits
type ModelCost = llm.Pricing
type ModelModalities = llm.Modalities
type ModelCapabilities = llm.Capabilities

var models = map[string]llm.ModelProfile{
	ModelJevLatest: {
		ID:     ModelJevLatest,
		Name:   "Jev Latest",
		Family: "jev",
		Limits: llm.ProfileLimits{
			Context: 64000,
		},
		Pricing: llm.Pricing{
			Input:  0.042,
			Output: 0,
		},
		Modalities: llm.Modalities{
			Inputs: []llm.Modality{
				llm.ModalityText,
			},
			Outputs: []llm.Modality{
				llm.ModalityText,
			},
		},
		Capabilities: llm.Capabilities{
			Decision: true,
		},
	},
	ModelJevPreview: {
		ID:     ModelJevPreview,
		Name:   "Jev Preview",
		Family: "jev",
		Limits: llm.ProfileLimits{
			Context: 64000,
		},
		Pricing: llm.Pricing{
			Input:  0.042,
			Output: 0,
		},
		Modalities: llm.Modalities{
			Inputs: []llm.Modality{
				llm.ModalityText,
			},
			Outputs: []llm.Modality{
				llm.ModalityText,
			},
		},
		Capabilities: llm.Capabilities{
			Decision: true,
		},
	},
	ModelJev1_14_0: {
		ID:     ModelJev1_14_0,
		Name:   "Jev 1.14.0",
		Family: "jev",
		Limits: llm.ProfileLimits{
			Context: 64000,
		},
		Pricing: llm.Pricing{
			Input:  0.042,
			Output: 0,
		},
		Modalities: llm.Modalities{
			Inputs: []llm.Modality{
				llm.ModalityText,
			},
			Outputs: []llm.Modality{
				llm.ModalityText,
			},
		},
		Capabilities: llm.Capabilities{
			Decision: true,
		},
	},
	ModelJev1_13_0: {
		ID:     ModelJev1_13_0,
		Name:   "Jev 1.13.0",
		Family: "jev",
		Limits: llm.ProfileLimits{
			Context: 64000,
		},
		Pricing: llm.Pricing{
			Input:  0.042,
			Output: 0,
		},
		Modalities: llm.Modalities{
			Inputs: []llm.Modality{
				llm.ModalityText,
			},
			Outputs: []llm.Modality{
				llm.ModalityText,
			},
		},
		Capabilities: llm.Capabilities{
			Decision: true,
		},
	},
}

// GetProfile looks up and returns the model profile for the given model ID.
func (p *Provider) GetProfile(id string) (llm.ModelProfile, bool) {
	profile, ok := models[id]
	return profile, ok
}
