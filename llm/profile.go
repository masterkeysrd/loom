package llm

import "strings"

// ModelProfile describes the capabilities and metadata of a specific LLM model.
// Providers embed a map of ModelProfile values (generated via go:generate) and
// expose them through the catalog methods on [Provider].
type ModelProfile struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Family       string        `json:"family,omitempty"`
	Capabilities Capabilities  `json:"capabilities"`
	Limits       ProfileLimits `json:"limits"`
	Modalities   Modalities    `json:"modalities"`
	OpenWeights  bool          `json:"open_weights,omitempty"`
	Pricing      Pricing       `json:"pricing,omitempty"`
}

type Pricing struct {
	Input        float64       `json:"input"`
	Output       float64       `json:"output"`
	CacheRead    float64       `json:"cache_read,omitempty"`
	CacheWrite   float64       `json:"cache_write,omitempty"`
	Reasoning    float64       `json:"reasoning,omitempty"`
	TieredLimits []TierPricing `json:"tiers,omitempty"`
}

type TierPricing struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
	Reasoning  float64 `json:"reasoning,omitempty"`
	TierLimit  int     `json:"limit"`
}

type Capabilities struct {
	Attachment  bool `json:"attachment"`
	Reasoning   bool `json:"reasoning"`
	ToolCall    bool `json:"tool_call"`
	Temperature bool `json:"temperature"`
}

type Modalities struct {
	Inputs  []Modality `json:"inputs"`
	Outputs []Modality `json:"outputs"`
}

type Modality string

const (
	ModalityText  Modality = "text"
	ModalityImage Modality = "image"
	ModalityAudio Modality = "audio"
	ModalityVideo Modality = "video"
	ModalityPDF   Modality = "pdf"
)

type ProfileLimits struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

// SearchProfiles returns all entries from profiles whose ID or DisplayName
// contains query (case-insensitive). It is a shared helper intended to be
// called by each provider's SearchProfiles implementation.
func SearchProfiles(profiles []ModelProfile, query string) []ModelProfile {
	query = strings.ToLower(query)
	var result []ModelProfile
	for _, p := range profiles {
		if strings.Contains(strings.ToLower(p.ID), query) ||
			strings.Contains(strings.ToLower(p.Name), query) {
			result = append(result, p)
		}
	}
	return result
}
