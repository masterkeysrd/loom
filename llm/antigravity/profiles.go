package loomantigravity

import "github.com/masterkeysrd/loom/llm"

// staticProfiles is an empty fallback map. Antigravity model profiles and capabilities
// are dynamically discovered from Google's /v1internal:fetchAvailableModels endpoint.
var staticProfiles = map[string]llm.ModelProfile{}
