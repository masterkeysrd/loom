package profile

import (
	"strings"

	"github.com/masterkeysrd/loom/message"
)

// Provider is an optional interface that model backends may implement to expose
// model catalog metadata, limits, capabilities, and pricing information.
type Provider interface {
	GetProfile(id string) (Profile, bool)
}

// Profile describes the capabilities, limits, modalities, and pricing metadata
// of a specific AI model across completion, decision, and embedding workloads.
type Profile struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Description  string       `json:"description,omitempty"`
	Family       string       `json:"family,omitempty"`
	Knowledge    string       `json:"knowledge,omitempty"`
	ReleaseDate  string       `json:"release_date,omitempty"`
	LastUpdated  string       `json:"last_updated,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
	Limits       Limits       `json:"limits"`
	Modalities   Modalities   `json:"modalities"`
	OpenWeights  bool         `json:"open_weights,omitempty"`
	Pricing      Pricing      `json:"pricing,omitempty"`
}

// EstimateCost calculates the estimated financial cost of a request based on
// the provided token details. It automatically handles tiered pricing logic
// if the model's profile defines it.
func (p Profile) EstimateCost(details message.TokenDetails) (message.CostDetails, message.Cost) {
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

// Pricing defines per-million-token pricing and optional context-window tiers.
type Pricing struct {
	Input        float64       `json:"input"`
	Output       float64       `json:"output"`
	CacheRead    float64       `json:"cache_read,omitempty"`
	CacheWrite   float64       `json:"cache_write,omitempty"`
	Reasoning    float64       `json:"reasoning,omitempty"`
	TieredLimits []TierPricing `json:"tiers,omitempty"`
}

// TierPricing defines pricing overrides when total token usage exceeds TierLimit.
type TierPricing struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
	Reasoning  float64 `json:"reasoning,omitempty"`
	TierLimit  int     `json:"limit"`
}

// Capabilities describes the supported operations and features of a model.
type Capabilities struct {
	Completion       bool              `json:"completion"`
	Decision         bool              `json:"decision"`
	Embedding        bool              `json:"embedding"`
	Attachment       bool              `json:"attachment"`
	Reasoning        bool              `json:"reasoning"`
	ToolCall         bool              `json:"tool_call"`
	Temperature      bool              `json:"temperature"`
	ReasoningOptions []ReasoningOption `json:"reasoning_options,omitempty"`
}

// ReasoningOption describes a supported reasoning parameter configuration.
type ReasoningOption struct {
	Type    string   `json:"type"`
	Values  []string `json:"values,omitempty"`
	Default string   `json:"default,omitempty"`
	Min     int      `json:"min,omitempty"`
	Max     int      `json:"max,omitempty"`
}

// Modalities lists the supported input and output modalities of a model.
type Modalities struct {
	Inputs  []Modality `json:"inputs"`
	Outputs []Modality `json:"outputs"`
}

// Modality represents a data modality supported by a model.
type Modality string

const (
	ModalityText      Modality = "text"
	ModalityImage     Modality = "image"
	ModalityAudio     Modality = "audio"
	ModalityVideo     Modality = "video"
	ModalityPDF       Modality = "pdf"
	ModalityEmbedding Modality = "embedding"
)

// Limits defines token and vector dimensionality limits for a model.
type Limits struct {
	Context    int `json:"context"`
	Output     int `json:"output"`
	Dimensions int `json:"dimensions,omitempty"`
}

// Search returns all entries from profiles whose ID or Name contains query
// (case-insensitive).
func Search(profiles []Profile, query string) []Profile {
	query = strings.ToLower(query)
	var result []Profile
	for _, p := range profiles {
		if strings.Contains(strings.ToLower(p.ID), query) ||
			strings.Contains(strings.ToLower(p.Name), query) {
			result = append(result, p)
		}
	}
	return result
}
