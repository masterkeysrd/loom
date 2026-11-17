package loomclaudecode

import (
	"cmp"
	"slices"
	"strings"

	"github.com/masterkeysrd/loom/profile"
	loomanthropic "github.com/masterkeysrd/loom/providers/anthropic"
)

// claudeCodeCatalog reuses the generated Anthropic profile catalog
// (updated via `make generate` / `loomgen llm-profiles -provider=anthropic`)
// so claudecode never drifts from Anthropic's model catalog.
var claudeCodeCatalog = loomanthropic.NewProvider(nil)

var aliasFamilies = map[string]string{
	"sonnet":  "claude-sonnet",
	"opus":    "claude-opus",
	"haiku":   "claude-haiku",
	"default": "claude-sonnet",
}

func adaptProfileForCLI(prof profile.Profile) profile.Profile {
	// The Claude Code CLI does not expose a temperature parameter.
	prof.Capabilities.Temperature = false
	return prof
}

func resolveLatestFamilyProfile(family string) (profile.Profile, bool) {
	all := claudeCodeCatalog.ListProfiles()
	var matches []profile.Profile
	for _, prof := range all {
		if prof.Family == family || strings.HasPrefix(prof.ID, family+"-") {
			matches = append(matches, prof)
		}
	}
	if len(matches) == 0 {
		return profile.Profile{}, false
	}
	// Prefer non-dated aliases with newest ReleaseDate, then lexicographically highest ID.
	slices.SortFunc(matches, func(a, b profile.Profile) int {
		if c := cmp.Compare(b.ReleaseDate, a.ReleaseDate); c != 0 {
			return c
		}
		return cmp.Compare(b.ID, a.ID)
	})
	return adaptProfileForCLI(matches[0]), true
}

// ListProfiles returns all known Claude Code model profiles (derived from the
// generated Anthropic catalog plus Claude Code CLI aliases such as "sonnet",
// "opus", "haiku", and "default"), with runtime overrides merged on top.
func (p *Provider) ListProfiles() []profile.Profile {
	byID := make(map[string]profile.Profile)
	for _, prof := range claudeCodeCatalog.ListProfiles() {
		byID[prof.ID] = adaptProfileForCLI(prof)
	}

	for alias, family := range aliasFamilies {
		if prof, ok := resolveLatestFamilyProfile(family); ok {
			prof.ID = alias
			prof.Name = prof.Name + " (Claude Code Alias)"
			byID[alias] = prof
		}
	}

	p.overrides.Range(func(k, v any) bool {
		byID[k.(string)] = v.(profile.Profile)
		return true
	})

	result := make([]profile.Profile, 0, len(byID))
	for _, prof := range byID {
		result = append(result, prof)
	}
	slices.SortFunc(result, func(a, b profile.Profile) int {
		return cmp.Compare(a.ID, b.ID)
	})
	return result
}

// GetProfile looks up a model profile by ID, checking runtime overrides first,
// then the generated Anthropic catalog, and finally Claude Code CLI aliases.
func (p *Provider) GetProfile(id string) (profile.Profile, bool) {
	if v, ok := p.overrides.Load(id); ok {
		return v.(profile.Profile), true
	}
	if prof, ok := claudeCodeCatalog.GetProfile(id); ok {
		return adaptProfileForCLI(prof), true
	}
	if family, ok := aliasFamilies[strings.ToLower(id)]; ok {
		if prof, found := resolveLatestFamilyProfile(family); found {
			prof.ID = id
			prof.Name = prof.Name + " (Claude Code Alias)"
			return prof, true
		}
	}
	return profile.Profile{}, false
}

// SearchProfiles returns profiles whose ID or Name contains query
// (case-insensitive).
func (p *Provider) SearchProfiles(query string) []profile.Profile {
	return profile.Search(p.ListProfiles(), query)
}

// OverrideProfile registers a custom profile for id, shadowing any catalog or alias entry.
func (p *Provider) OverrideProfile(id string, prof profile.Profile) {
	p.overrides.Store(id, prof)
}
