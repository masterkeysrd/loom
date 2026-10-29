package profile

import (
	"fmt"
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
	Pricing      Pricing      `json:"pricing"`
}

// CostEstimate is the result of estimating a request's cost.
type CostEstimate struct {
	Details message.CostDetails
	Total   message.Cost
	// Tier is the label of the applied tier; empty when base pricing applied.
	Tier string
	// Priced is false when the profile carries no pricing data.
	Priced bool
}

// Estimate computes the cost of a request and reports the applied tier.
//
// message.TokenDetails.Input is the total prompt size and includes cache
// read/write tokens (see that type's documentation); the uncached part is
// billed at the input price. Of all tiers whose condition matches, the
// one with the highest Size wins, regardless of slice order. Conditions are
// strict: a tier applies when measured tokens are > Size ("over N tokens").
// Unknown condition types never match.
func (p Profile) Estimate(details message.TokenDetails) CostEstimate {
	pricing := p.Pricing
	var tier string

	var best *TierPricing
	bestSize := 0
	for i := range p.Pricing.TieredLimits {
		t := &p.Pricing.TieredLimits[i]
		cond := t.Condition
		if !cond.matches(details) {
			continue
		}
		if best == nil || cond.Size > bestSize {
			best, bestSize = t, cond.Size
		}
	}
	if best != nil {
		pricing.Input = best.Input
		pricing.Output = best.Output
		pricing.CacheRead = best.CacheRead
		pricing.CacheWrite = best.CacheWrite
		pricing.Reasoning = best.Reasoning
		tier = best.EffectiveLabel()
	}

	// "price per million" -> nano-dollars per token: price * 1,000.
	toNano := func(price float64, tokens int) message.Cost {
		return message.Cost(price * 1000 * float64(tokens))
	}

	uncached := max(0, details.Input-details.CacheRead-details.CacheWrite)
	costs := message.CostDetails{
		Input:      toNano(pricing.Input, uncached),
		Output:     toNano(pricing.Output, details.Output),
		CacheRead:  toNano(pricing.CacheRead, details.CacheRead),
		CacheWrite: toNano(pricing.CacheWrite, details.CacheWrite),
		Reasoning:  toNano(pricing.Reasoning, details.Reasoning),
	}

	return CostEstimate{
		Details: costs,
		Total:   costs.Input + costs.Output + costs.CacheRead + costs.CacheWrite + costs.Reasoning,
		Tier:    tier,
		Priced:  !p.Pricing.IsZero(),
	}
}

// Pricing defines per-million-token pricing and optional conditional tiers.
type Pricing struct {
	Input        float64       `json:"input"`
	Output       float64       `json:"output"`
	CacheRead    float64       `json:"cache_read,omitempty"`
	CacheWrite   float64       `json:"cache_write,omitempty"`
	Reasoning    float64       `json:"reasoning,omitempty"`
	TieredLimits []TierPricing `json:"tiers,omitempty"`
}

// IsZero reports whether the pricing carries no data: all prices are zero and
// there are no tiers.
func (p Pricing) IsZero() bool {
	return p.Input == 0 && p.Output == 0 && p.CacheRead == 0 &&
		p.CacheWrite == 0 && p.Reasoning == 0 && len(p.TieredLimits) == 0
}

// TierConditionType identifies what a pricing tier condition measures.
type TierConditionType string

const (
	// TierConditionContext compares the prompt/context size of the request:
	// TokenDetails.Input, which includes cache read/write tokens; output excluded.
	TierConditionContext TierConditionType = "context"
)

// TierCondition describes when a pricing tier applies.
type TierCondition struct {
	Type TierConditionType `json:"type"`
	// Size is the threshold: the tier applies when measured tokens > Size.
	Size int `json:"size"`
}

func (c TierCondition) matches(d message.TokenDetails) bool {
	switch c.Type {
	case TierConditionContext:
		return d.Input > c.Size
	default:
		return false
	}
}

// TierPricing defines pricing overrides for a conditional tier.
type TierPricing struct {
	Label      string        `json:"label,omitempty"` // e.g. "Context over 200k"
	Condition  TierCondition `json:"condition"`
	Input      float64       `json:"input"`
	Output     float64       `json:"output"`
	CacheRead  float64       `json:"cache_read,omitempty"`
	CacheWrite float64       `json:"cache_write,omitempty"`
	Reasoning  float64       `json:"reasoning,omitempty"`
}

// EffectiveLabel returns Label, or a stable generated label such as
// "context>200000" when Label is empty.
func (t TierPricing) EffectiveLabel() string {
	if t.Label != "" {
		return t.Label
	}
	c := t.Condition
	return fmt.Sprintf("%s>%d", c.Type, c.Size)
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
