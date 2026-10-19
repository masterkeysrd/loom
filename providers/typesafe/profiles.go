package loomtypesafe

import "github.com/masterkeysrd/loom/profile"

const (
	ModelJevLatest  = "jev-latest"
	ModelJevPreview = "jev-preview"
	ModelJev1_14_0  = "jev-1.14.0"
	ModelJev1_13_0  = "jev-1.13.0"
)

// Type aliases for model profile specifications.
type ModelLimits = profile.Limits
type ModelCost = profile.Pricing
type ModelModalities = profile.Modalities
type ModelCapabilities = profile.Capabilities

var models = map[string]profile.Profile{
	ModelJevLatest: {
		ID:     ModelJevLatest,
		Name:   "Jev Latest",
		Family: "jev",
		Limits: profile.Limits{
			Context: 64000,
		},
		Pricing: profile.Pricing{
			Input:  0.042,
			Output: 0,
		},
		Modalities: profile.Modalities{
			Inputs: []profile.Modality{
				profile.ModalityText,
			},
			Outputs: []profile.Modality{
				profile.ModalityText,
			},
		},
		Capabilities: profile.Capabilities{
			Decision: true,
		},
	},
	ModelJevPreview: {
		ID:     ModelJevPreview,
		Name:   "Jev Preview",
		Family: "jev",
		Limits: profile.Limits{
			Context: 64000,
		},
		Pricing: profile.Pricing{
			Input:  0.042,
			Output: 0,
		},
		Modalities: profile.Modalities{
			Inputs: []profile.Modality{
				profile.ModalityText,
			},
			Outputs: []profile.Modality{
				profile.ModalityText,
			},
		},
		Capabilities: profile.Capabilities{
			Decision: true,
		},
	},
	ModelJev1_14_0: {
		ID:     ModelJev1_14_0,
		Name:   "Jev 1.14.0",
		Family: "jev",
		Limits: profile.Limits{
			Context: 64000,
		},
		Pricing: profile.Pricing{
			Input:  0.042,
			Output: 0,
		},
		Modalities: profile.Modalities{
			Inputs: []profile.Modality{
				profile.ModalityText,
			},
			Outputs: []profile.Modality{
				profile.ModalityText,
			},
		},
		Capabilities: profile.Capabilities{
			Decision: true,
		},
	},
	ModelJev1_13_0: {
		ID:     ModelJev1_13_0,
		Name:   "Jev 1.13.0",
		Family: "jev",
		Limits: profile.Limits{
			Context: 64000,
		},
		Pricing: profile.Pricing{
			Input:  0.042,
			Output: 0,
		},
		Modalities: profile.Modalities{
			Inputs: []profile.Modality{
				profile.ModalityText,
			},
			Outputs: []profile.Modality{
				profile.ModalityText,
			},
		},
		Capabilities: profile.Capabilities{
			Decision: true,
		},
	},
}

// GetProfile looks up and returns the model profile for the given model ID.
func (p *Provider) GetProfile(id string) (profile.Profile, bool) {
	prof, ok := models[id]
	return prof, ok
}
