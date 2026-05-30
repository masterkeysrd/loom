package message

// TokenMetrics holds token usage statistics reported by an LLM after a
// generation completes. Fields mirror the subset of provider-specific metrics
// that are meaningful across backends.
type TokenMetrics struct {
	// PromptTokens is the number of tokens consumed by the input prompt.
	PromptTokens int `json:"prompt_tokens,omitempty"`

	// CompletionTokens is the number of tokens generated in the response.
	CompletionTokens int `json:"completion_tokens,omitempty"`

	// TotalTokens is the sum of PromptTokens and CompletionTokens.
	TotalTokens int `json:"total_tokens,omitempty"`
}

func (m TokenMetrics) Add(other TokenMetrics) TokenMetrics {
	return TokenMetrics{
		PromptTokens:     m.PromptTokens + other.PromptTokens,
		CompletionTokens: m.CompletionTokens + other.CompletionTokens,
		TotalTokens:      m.TotalTokens + other.TotalTokens,
	}
}
