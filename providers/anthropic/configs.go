package loomanthropic

import "github.com/masterkeysrd/loom/profile"

var staticProfileOverrides = map[string]func(profile.Profile) profile.Profile{
	"claude-sonnet-4-6": func(p profile.Profile) profile.Profile {
		p.Limits.Output = 128000
		return p
	},
	"claude-opus-4-6": func(p profile.Profile) profile.Profile {
		p.Limits.Output = 128000
		return p
	},
}
