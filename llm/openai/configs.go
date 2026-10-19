package loomopenai

import "github.com/masterkeysrd/loom/llm"

var staticProfileOverrides = map[string]func(llm.ModelProfile) llm.ModelProfile{
	"gpt-6-luna": func(p llm.ModelProfile) llm.ModelProfile {
		p.Capabilities.Decision = true
		return p
	},
}
