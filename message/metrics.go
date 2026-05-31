package message

import "time"

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

	// CachedPromptTokens is the number of tokens from the prompt that were
	// retrieved from a cache (OpenAI cached_tokens, Anthropic cache_read_input_tokens,
	// Gemini cachedContentTokenCount).
	CachedPromptTokens int `json:"cached_prompt_tokens,omitempty"`

	// CacheWriteTokens is the number of tokens from the prompt that were
	// written to a cache (Anthropic cache_creation_input_tokens).
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`

	// ReasoningTokens is the number of tokens used for internal reasoning/thinking
	// (OpenAI reasoning_tokens).
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`

	// TotalDuration is the total time spent by the LLM (including queue,
	// loading, prompt processing, and generation).
	TotalDuration time.Duration `json:"total_duration,omitempty"`

	// PromptDuration is the time spent processing the prompt.
	PromptDuration time.Duration `json:"prompt_duration,omitempty"`

	// CompletionDuration is the time spent generating the completion.
	CompletionDuration time.Duration `json:"completion_duration,omitempty"`
}

func (m TokenMetrics) Add(other TokenMetrics) TokenMetrics {
	return TokenMetrics{
		PromptTokens:       m.PromptTokens + other.PromptTokens,
		CompletionTokens:   m.CompletionTokens + other.CompletionTokens,
		TotalTokens:        m.TotalTokens + other.TotalTokens,
		CachedPromptTokens: m.CachedPromptTokens + other.CachedPromptTokens,
		CacheWriteTokens:   m.CacheWriteTokens + other.CacheWriteTokens,
		ReasoningTokens:    m.ReasoningTokens + other.ReasoningTokens,
		TotalDuration:      m.TotalDuration + other.TotalDuration,
		PromptDuration:     m.PromptDuration + other.PromptDuration,
		CompletionDuration: m.CompletionDuration + other.CompletionDuration,
	}
}
