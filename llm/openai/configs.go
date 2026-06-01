package loomopenai

import "github.com/masterkeysrd/loom/llm"

var staticProfiles = map[string]llm.ModelProfile{}

var staticProfileOverrides = map[string]func(llm.ModelProfile) llm.ModelProfile{}
