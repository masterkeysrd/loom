package loomopenai

import "github.com/masterkeysrd/loom/llm"

var staticProfileOverrides = map[string]func(llm.ModelProfile) llm.ModelProfile{}
