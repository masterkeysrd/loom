package loomclaudecode

import (
	"cmp"
	"slices"
	"strings"

	"github.com/masterkeysrd/loom/profile"
	loomanthropic "github.com/masterkeysrd/loom/providers/anthropic"
	"github.com/masterkeysrd/loom/providers/claudecode/internal/protocol"
)

// claudeCodeCatalog is used strictly to enrich models discovered dynamically
// from the Claude Code CLI with Anthropic metadata (context/output limits,
// pricing, modalities). Unadvertised catalog models are never exposed.
var claudeCodeCatalog = loomanthropic.NewProvider(nil)

var defaultEffortLevels = []string{"low", "medium", "high", "xhigh", "max"}

func adaptProfileForCLI(prof profile.Profile) profile.Profile {
	// The Claude Code CLI does not expose a temperature parameter.
	prof.Capabilities.Temperature = false
	if prof.Capabilities.Reasoning && len(prof.Capabilities.ReasoningOptions) == 0 {
		prof.Capabilities.ReasoningOptions = []profile.ReasoningOption{
			{Type: "effort", Values: slices.Clone(defaultEffortLevels)},
		}
	}
	return prof
}

// ListProfiles returns cached dynamically discovered Claude Code CLI model
// profiles merged with explicit [Provider.OverrideProfile] entries. Before
// [Provider.ListModels] or a CLI session initializes (and when no overrides
// exist), nil is returned so [llm.NewModel] allows any model or alias without
// spawning an extra subprocess.
func (p *Provider) ListProfiles() []profile.Profile {
	byID := p.discoveredProfileMap()
	if len(byID) == 0 {
		return nil
	}
	return p.sortedProfiles(byID)
}

// GetProfile looks up a model profile by ID, checking runtime overrides first
// and then cached live CLI-discovered models.
func (p *Provider) GetProfile(id string) (profile.Profile, bool) {
	if v, ok := p.overrides.Load(id); ok {
		return v.(profile.Profile), true
	}
	if prof, ok := lookupCaseInsensitive(p.discoveredProfileMap(), id); ok {
		return prof, true
	}
	return profile.Profile{}, false
}

// SearchProfiles returns discovered/overridden profiles whose ID or Name
// contains query (case-insensitive).
func (p *Provider) SearchProfiles(query string) []profile.Profile {
	return profile.Search(p.ListProfiles(), query)
}

// OverrideProfile registers a custom profile for id.
func (p *Provider) OverrideProfile(id string, prof profile.Profile) {
	p.overrides.Store(id, prof)
}

func (p *Provider) sortedProfiles(byID map[string]profile.Profile) []profile.Profile {
	result := make([]profile.Profile, 0, len(byID))
	for _, prof := range byID {
		result = append(result, prof)
	}
	slices.SortFunc(result, func(a, b profile.Profile) int {
		return cmp.Compare(a.ID, b.ID)
	})
	return result
}

// cachedProfileIDs returns all currently known discovered + overridden profile IDs
// without spawning a CLI probe.
func (p *Provider) cachedProfileIDs() []string {
	byID := p.discoveredProfileMap()
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (p *Provider) discoveredProfileMap() map[string]profile.Profile {
	byID := make(map[string]profile.Profile)
	if p.proxy != nil {
		mergeLiveModels(byID, p.proxy.snapshotLiveModels())
	}
	p.overrides.Range(func(k, v any) bool {
		byID[k.(string)] = v.(profile.Profile)
		return true
	})
	return byID
}

func mergeLiveModels(byID map[string]profile.Profile, live []protocol.InitModelInfo) {
	for _, m := range live {
		id := strings.TrimSpace(m.Value)
		if id == "" {
			continue
		}

		base := enrichFromAnthropicCatalog(m)
		if len(m.SupportedEffortLevels) > 0 {
			base.Capabilities.Reasoning = true
			base.Capabilities.ReasoningOptions = []profile.ReasoningOption{
				{Type: "effort", Values: slices.Clone(m.SupportedEffortLevels)},
			}
		} else if m.SupportsEffort || m.SupportsAdaptiveThinking {
			base.Capabilities.Reasoning = true
		}

		aliasProf := base
		aliasProf.ID = id
		if m.DisplayName != "" {
			aliasProf.Name = m.DisplayName
		} else if aliasProf.Name == "" {
			aliasProf.Name = id
		}
		if m.Description != "" {
			aliasProf.Description = m.Description
		}
		byID[id] = adaptProfileForCLI(aliasProf)

		if resolved := strings.TrimSpace(m.ResolvedModel); resolved != "" && resolved != id {
			resolvedProf := base
			resolvedProf.ID = resolved
			if cat, ok := claudeCodeCatalog.GetProfile(resolved); ok {
				resolvedProf = cat
				if len(m.SupportedEffortLevels) > 0 {
					resolvedProf.Capabilities.Reasoning = true
					resolvedProf.Capabilities.ReasoningOptions = []profile.ReasoningOption{
						{Type: "effort", Values: slices.Clone(m.SupportedEffortLevels)},
					}
				} else if m.SupportsEffort || m.SupportsAdaptiveThinking {
					resolvedProf.Capabilities.Reasoning = true
				}
			} else {
				if m.DisplayName != "" && id != "default" {
					resolvedProf.Name = m.DisplayName
				} else if resolvedProf.Name == "" {
					resolvedProf.Name = resolved
				}
				if m.Description != "" && resolvedProf.Description == "" {
					resolvedProf.Description = m.Description
				}
			}
			if resolvedProf.Family == "" {
				resolvedProf.Family = inferClaudeFamily(resolved)
			}
			byID[resolved] = adaptProfileForCLI(resolvedProf)
		}
	}
}

func enrichFromAnthropicCatalog(m protocol.InitModelInfo) profile.Profile {
	if m.ResolvedModel != "" {
		if prof, ok := claudeCodeCatalog.GetProfile(m.ResolvedModel); ok {
			return prof
		}
	}
	if prof, ok := claudeCodeCatalog.GetProfile(m.Value); ok {
		return prof
	}
	return profile.Profile{
		Family: inferClaudeFamily(firstNonEmpty(m.ResolvedModel, m.Value)),
		Capabilities: profile.Capabilities{
			Completion: true,
			ToolCall:   true,
			Reasoning:  m.SupportsEffort || m.SupportsAdaptiveThinking,
		},
		Modalities: profile.Modalities{
			Inputs:  []profile.Modality{profile.ModalityText},
			Outputs: []profile.Modality{profile.ModalityText},
		},
	}
}

func lookupCaseInsensitive(byID map[string]profile.Profile, id string) (profile.Profile, bool) {
	if prof, ok := byID[id]; ok {
		return prof, true
	}
	if prof, ok := byID[strings.ToLower(id)]; ok {
		return prof, true
	}
	return profile.Profile{}, false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func inferClaudeFamily(id string) string {
	lower := strings.ToLower(id)
	switch {
	case strings.Contains(lower, "opus"):
		return "claude-opus"
	case strings.Contains(lower, "sonnet"):
		return "claude-sonnet"
	case strings.Contains(lower, "haiku"):
		return "claude-haiku"
	default:
		return "claude"
	}
}
