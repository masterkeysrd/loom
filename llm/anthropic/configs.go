package loomanthropic

import "github.com/masterkeysrd/loom/llm"

var staticConfigs = map[string]llm.ModelConfig{
	"claude-sonnet-4-6": {
		MaxTokens: 128_000,
	},
	"claude-opus-4-6": {
		MaxTokens: 128_000,
	},
}
