package loomopenai

import "github.com/masterkeysrd/loom/profile"

var staticProfileOverrides = map[string]func(profile.Profile) profile.Profile{
	"gpt-6-luna": func(p profile.Profile) profile.Profile {
		p.Capabilities.Decision = true
		return p
	},
}
