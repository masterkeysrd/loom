package llm

import (
	"strings"

	"github.com/masterkeysrd/loom/message"
)

// ModelProfile describes the capabilities and metadata of a specific LLM model.
// Providers embed a map of ModelProfile values (generated via go:generate) and
// expose them through the catalog methods on [Provider].
type ModelProfile struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Family       string        `json:"family,omitempty"`
	Knowledge    string        `json:"knowledge,omitempty"`
	ReleaseDate  string        `json:"release_date,omitempty"`
	LastUpdated  string        `json:"last_updated,omitempty"`
	Capabilities Capabilities  `json:"capabilities"`
	Limits       ProfileLimits `json:"limits"`
	Modalities   Modalities    `json:"modalities"`
	OpenWeights  bool          `json:"open_weights,omitempty"`
	Pricing      Pricing       `json:"pricing,omitempty"`
}

// EstimateCost calculates the estimated financial cost of a request based on
// the provided token details. It automatically handles tiered pricing logic
// if the model's profile defines it.
func (p ModelProfile) EstimateCost(details message.TokenDetails) (message.CostDetails, message.Cost) {
	// 1. Select the appropriate pricing tier.
	// Some models (like Anthropic/Gemini) charge more if the total context window is large.
	pricing := p.Pricing
	totalTokens := details.Input + details.Output

	for _, tier := range p.Pricing.TieredLimits {
		if totalTokens >= tier.TierLimit {
			pricing.Input = tier.Input
			pricing.Output = tier.Output
			pricing.CacheRead = tier.CacheRead
			pricing.CacheWrite = tier.CacheWrite
			pricing.Reasoning = tier.Reasoning
		}
	}

	// 2. Helper to convert "price per million" to "nano-dollars per token"
	// $1.00 = 1,000,000,000 nano-dollars.
	// (price / 1,000,000) * 1,000,000,000 = price * 1,000.
	toNano := func(price float64, tokens int) message.Cost {
		return message.Cost(price * 1000 * float64(tokens))
	}

	costs := message.CostDetails{
		Input:      toNano(pricing.Input, details.Input),
		Output:     toNano(pricing.Output, details.Output),
		CacheRead:  toNano(pricing.CacheRead, details.CacheRead),
		CacheWrite: toNano(pricing.CacheWrite, details.CacheWrite),
		Reasoning:  toNano(pricing.Reasoning, details.Reasoning),
	}

	total := costs.Input + costs.Output + costs.CacheRead + costs.CacheWrite + costs.Reasoning

	return costs, total
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
	Attachment       bool              `json:"attachment"`
	Reasoning        bool              `json:"reasoning"`
	ToolCall         bool              `json:"tool_call"`
	Temperature      bool              `json:"temperature"`
	ReasoningOptions []ReasoningOption `json:"reasoning_options,omitempty"`
}

type ReasoningOption struct {
	Type   string   `json:"type"`
	Values []string `json:"values,omitempty"`
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
