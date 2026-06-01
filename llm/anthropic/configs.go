package loomanthropic

import "github.com/masterkeysrd/loom/llm"

var staticProfileOverrides = map[string]func(llm.ModelProfile) llm.ModelProfile{
	"claude-sonnet-4-6": func(p llm.ModelProfile) llm.ModelProfile {
		p.Limits.Output = 128000
		return p
	},
	"claude-opus-4-6": func(p llm.ModelProfile) llm.ModelProfile {
		p.Limits.Output = 128000
		return p
	},
}
