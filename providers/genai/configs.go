package loomgenai

import "github.com/masterkeysrd/loom/profile"

var staticProfileOverrides = map[string]func(profile.Profile) profile.Profile{
	"gemini-embedding-001": func(p profile.Profile) profile.Profile {
		p.Limits.Dimensions = 3072
		return p
	},
	"gemini-embedding-2": func(p profile.Profile) profile.Profile {
		p.Limits.Dimensions = 3072
		return p
	},
}
