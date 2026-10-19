package loomgenai

import "github.com/masterkeysrd/loom/llm"

var staticProfileOverrides = map[string]func(llm.ModelProfile) llm.ModelProfile{
	"gemini-embedding-001": func(p llm.ModelProfile) llm.ModelProfile {
		p.Limits.Dimensions = 3072
		return p
	},
	"gemini-embedding-2": func(p llm.ModelProfile) llm.ModelProfile {
		p.Limits.Dimensions = 3072
		return p
	},
}
