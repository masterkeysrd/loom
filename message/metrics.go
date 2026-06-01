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
	Tokens TokenDetails `json:"tokens,omitempty"`

	// Cost provides a detailed breakdown of estimated costs.
	Cost CostDetails `json:"cost,omitempty"`

	// Timing provides a detailed breakdown of durations.
	Timing TimingMetrics `json:"timing,omitempty"`
}

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

func (m TokenMetrics) Add(other TokenMetrics) TokenMetrics {
	return TokenMetrics{
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
	}
}
