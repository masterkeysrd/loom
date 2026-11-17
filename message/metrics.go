package message

import (
	"fmt"
	"time"
)

// Cost represents a monetary value in nano-dollars (1e-9 USD).
// Using an integer base avoids floating-point precision errors during aggregation.
type Cost int64

// AsUSD converts the cost to a float64 representing USD.
func (c Cost) AsUSD() float64 {
	return float64(c) / 1e9
}

// String returns a formatted string representation of the cost in USD.
func (c Cost) String() string {
	return fmt.Sprintf("$%.6f", c.AsUSD())
}

// TokenMetrics holds token usage, timing, and estimated cost statistics reported by an LLM.
// Note: Cost calculations are estimates based on static profile data and may differ slightly
// from official billing dashboards due to rounding or pricing changes.
type TokenMetrics struct {
	// TotalTokens is the sum of all tokens consumed in this generation.
	TotalTokens int `json:"total_tokens,omitempty"`

	// TotalCost is the estimated total cost of this generation in USD.
	TotalCost Cost `json:"total_cost,omitempty"`

	// Tokens provides a detailed breakdown of token counts.
	Tokens TokenDetails `json:"tokens"`

	// Cost provides a detailed breakdown of estimated costs.
	Cost CostDetails `json:"cost"`

	// Timing provides a detailed breakdown of durations.
	Timing TimingMetrics `json:"timing"`

	// Credits provides provider-reported credit, billing unit, or quota consumption.
	Credits CreditMetrics `json:"credits,omitzero"`

	// CostTier is the label of the applied pricing tier; empty means base pricing.
	CostTier string `json:"cost_tier,omitempty"`

	// Priced reports whether pricing data was available for the estimate.
	Priced bool `json:"priced,omitempty"`
}

// CreditMetrics holds provider-reported billing, credit, or quota-unit consumption
// for a single generation. Unlike TotalCost/Cost (which are estimated locally from
// static model profiles), Credits captures the server-reported charge, multiplier,
// or billing unit when supplied by the provider.
type CreditMetrics struct {
	// Amount is the consumed quantity in Unit (e.g. AI credits, premium requests, or USD).
	Amount float64 `json:"amount,omitempty"`

	// Unit identifies what Amount measures (e.g. "ai_credits", "premium_requests", "usd").
	Unit string `json:"unit,omitempty"`

	// Multiplier is the billing multiplier applied to the request when reported
	// separately by the provider (e.g. Copilot model cost multiplier such as 1.0 or 0.33).
	Multiplier float64 `json:"multiplier,omitempty"`

	// Status reports provider-specific billing status when available
	// (e.g. "available", "partial", "unavailable").
	Status string `json:"status,omitempty"`

	// Estimated reports whether Amount is an estimate rather than an authoritative
	// post-settlement charge.
	Estimated bool `json:"estimated,omitempty"`
}

// Add sums two CreditMetrics values. Amounts are summed when both operands share
// the same Unit or one side has an empty Unit; incompatible non-empty units clear
// Unit and Amount. Multiplier is kept when equal or when one side is zero.
func (c CreditMetrics) Add(other CreditMetrics) CreditMetrics {
	var unit string
	var amount float64
	switch {
	case c.Unit == other.Unit:
		unit = c.Unit
		amount = c.Amount + other.Amount
	case c.Unit == "":
		unit = other.Unit
		amount = c.Amount + other.Amount
	case other.Unit == "":
		unit = c.Unit
		amount = c.Amount + other.Amount
	}

	var multiplier float64
	switch {
	case c.Multiplier == other.Multiplier:
		multiplier = c.Multiplier
	case c.Multiplier == 0:
		multiplier = other.Multiplier
	case other.Multiplier == 0:
		multiplier = c.Multiplier
	}

	var status string
	switch {
	case c.Status == other.Status:
		status = c.Status
	case c.Status == "":
		status = other.Status
	case other.Status == "":
		status = c.Status
	case c.Status == "unavailable" || other.Status == "unavailable":
		status = "unavailable"
	default:
		status = "partial"
	}

	return CreditMetrics{
		Amount:     amount,
		Unit:       unit,
		Multiplier: multiplier,
		Status:     status,
		Estimated:  c.Estimated || other.Estimated,
	}
}

// TokenDetails is a breakdown of token counts.
//
// Normalization rule (every provider mapping MUST follow it): Input is the
// TOTAL prompt size and INCLUDES cached tokens. CacheRead and CacheWrite are
// subsets of Input, not additions to it. Providers whose usage reports already
// include cached tokens in the prompt count (OpenAI, Gemini, Ollama) pass them
// through; providers that report them separately (Anthropic) sum them in. This
// matches LangChain's UsageMetadata. The uncached part of the prompt is
// Input - CacheRead - CacheWrite.
type TokenDetails struct {
	Input      int `json:"input,omitempty"`
	Output     int `json:"output,omitempty"`
	CacheRead  int `json:"cache_read,omitempty"`
	CacheWrite int `json:"cache_write,omitempty"`
	Reasoning  int `json:"reasoning,omitempty"`
}

type CostDetails struct {
	Input      Cost `json:"input,omitempty"`
	Output     Cost `json:"output,omitempty"`
	CacheRead  Cost `json:"cache_read,omitempty"`
	CacheWrite Cost `json:"cache_write,omitempty"`
	Reasoning  Cost `json:"reasoning,omitempty"`
}

type TimingMetrics struct {
	Total      time.Duration `json:"total,omitempty"`
	Processing time.Duration `json:"processing,omitempty"`
	Generation time.Duration `json:"generation,omitempty"`
}

// Add sums two metrics. CostTier is kept only when both operands share the
// same tier (otherwise cleared); Priced is the logical AND of both operands.
func (m TokenMetrics) Add(other TokenMetrics) TokenMetrics {
	tier := ""
	if m.CostTier == other.CostTier {
		tier = m.CostTier
	}
	return TokenMetrics{
		CostTier:    tier,
		Priced:      m.Priced && other.Priced,
		TotalTokens: m.TotalTokens + other.TotalTokens,
		TotalCost:   m.TotalCost + other.TotalCost,
		Tokens: TokenDetails{
			Input:      m.Tokens.Input + other.Tokens.Input,
			Output:     m.Tokens.Output + other.Tokens.Output,
			CacheRead:  m.Tokens.CacheRead + other.Tokens.CacheRead,
			CacheWrite: m.Tokens.CacheWrite + other.Tokens.CacheWrite,
			Reasoning:  m.Tokens.Reasoning + other.Tokens.Reasoning,
		},
		Cost: CostDetails{
			Input:      m.Cost.Input + other.Cost.Input,
			Output:     m.Cost.Output + other.Cost.Output,
			CacheRead:  m.Cost.CacheRead + other.Cost.CacheRead,
			CacheWrite: m.Cost.CacheWrite + other.Cost.CacheWrite,
			Reasoning:  m.Cost.Reasoning + other.Cost.Reasoning,
		},
		Timing: TimingMetrics{
			Total:      m.Timing.Total + other.Timing.Total,
			Processing: m.Timing.Processing + other.Timing.Processing,
			Generation: m.Timing.Generation + other.Timing.Generation,
		},
		Credits: m.Credits.Add(other.Credits),
	}
}
