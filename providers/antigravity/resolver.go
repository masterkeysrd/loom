package loomantigravity

import (
	"context"
	"strings"

	"github.com/masterkeysrd/loom/providers/antigravity/sdk"
)

// ModelResolver translates a requested model ID and wire request into a concrete
// backend model ID and modified request (for example, injecting thinking levels).
type ModelResolver func(requestedModel string, req sdk.GenerateContentRequest, availableModels map[string]bool) (string, sdk.GenerateContentRequest)

// ResolveTargetModel resolves a requested model ID to its concrete backend endpoint ID,
// applying configured aliases, custom resolvers, and dynamic heuristic suffix mappings.
func (p *Provider) ResolveTargetModel(model string) string {
	target, _ := p.resolveModel(model, sdk.GenerateContentRequest{})
	return target
}

func (p *Provider) ensureAvailableModels(ctx context.Context) {
	token := ""
	if p.tokenSource != nil {
		if tok, err := p.tokenSource.Token(); err == nil {
			token = tok.AccessToken
		}
	}

	p.availableMu.RLock()
	ready := len(p.availableModels) > 0 && (token == "" || p.availableToken == token)
	p.availableMu.RUnlock()
	if ready {
		return
	}

	p.availableInitMu.Lock()
	defer p.availableInitMu.Unlock()

	p.availableMu.RLock()
	ready = len(p.availableModels) > 0 && (token == "" || p.availableToken == token)
	p.availableMu.RUnlock()
	if ready {
		return
	}

	models, err := p.client.FetchAvailableModels(ctx)
	if err != nil {
		return
	}
	p.recordAvailableModels(models, token)
}

func (p *Provider) recordAvailableModels(models []sdk.AvailableModel, token string) {
	if len(models) == 0 {
		return
	}
	avail := make(map[string]bool, len(models))
	for _, m := range models {
		avail[m.ID] = true
	}
	p.availableMu.Lock()
	p.availableModels = avail
	p.availableToken = token
	p.availableMu.Unlock()
}

func (p *Provider) resolveModel(model string, req sdk.GenerateContentRequest) (string, sdk.GenerateContentRequest) {
	p.availableMu.RLock()
	avail := p.availableModels
	p.availableMu.RUnlock()

	if req.GenerationConfig != nil && req.GenerationConfig.ThinkingConfig != nil && req.GenerationConfig.ThinkingConfig.ThinkingLevel != "" {
		req = withThinkingLevel(req, req.GenerationConfig.ThinkingConfig.ThinkingLevel)
	}

	// 1. Custom user resolver
	if p.resolver != nil {
		return p.resolver(model, req, avail)
	}

	// 2. Custom user aliases
	if target, ok := p.aliases[model]; ok {
		model = target
	}

	// 3. Exact server match passthrough
	if len(avail) > 0 && avail[model] {
		return model, req
	}

	// 4. Generic heuristic pattern resolver
	effort := ""
	if req.GenerationConfig != nil && req.GenerationConfig.ThinkingConfig != nil {
		effort = strings.ToUpper(req.GenerationConfig.ThinkingConfig.ThinkingLevel)
	}

	base, suffixEffort := parseEffortSuffix(model)
	if suffixEffort != "" {
		effort = suffixEffort
	}

	// If no effort was requested and the model had no effort suffix:
	if effort == "" && suffixEffort == "" {
		if len(avail) == 0 || avail[base] {
			return base, req
		}
		if avail[base+"-tiered"] {
			return base + "-tiered", req
		}
		for _, suffix := range []string{"-medium", "-low", "-high"} {
			if candidate := base + suffix; avail[candidate] {
				return candidate, req
			}
		}
		return model, req
	}

	// An effort was specified (via suffix or request config):
	// 1. If base-tiered is available (or cache not yet initialized), route to base-tiered
	tieredID := base + "-tiered"
	if len(avail) == 0 || avail[tieredID] {
		return tieredID, withThinkingLevel(req, effort)
	}

	// 2. If separate endpoint base-[effort] exists (e.g. gemini-4-carbon-high), use it
	effortID := base + "-" + strings.ToLower(effort)
	if avail[effortID] {
		return effortID, req
	}

	// 3. Fallback to base if available
	if avail[base] {
		return base, withThinkingLevel(req, effort)
	}

	// 4. Fallback to another available effort variant in the same family
	for _, suffix := range []string{"-medium", "-low", "-high"} {
		if candidate := base + suffix; avail[candidate] {
			return candidate, req
		}
	}

	return model, req
}

// expandAvailableModels filters internal completion-preview endpoints and expands
// "-tiered" gateway models into canonical virtual variants for profiles and quotas.
func expandAvailableModels(raw []sdk.AvailableModel) []sdk.AvailableModel {
	models := make([]sdk.AvailableModel, 0, len(raw))
	for _, m := range raw {
		switch m.ID {
		case "chat_20706", "chat_23310", "tab_flash_lite_preview", "tab_jump_flash_lite_preview":
			continue
		}

		models = append(models, m)

		if baseID, ok := strings.CutSuffix(m.ID, "-tiered"); ok {
			baseName := cleanDisplayName(m.DisplayName, m.ID, baseID)
			for _, eff := range []struct {
				suffix string
				label  string
				level  int
			}{
				{"-high", " (High)", 3},
				{"-medium", " (Medium)", 2},
				{"-low", " (Low)", 1},
				{"", "", 0},
			} {
				am := m
				am.ID = baseID + eff.suffix
				am.DisplayName = strings.TrimSpace(baseName + eff.label)
				am.ThinkingLevel = eff.level
				// Remember underlying tiered ID on expanded entries via DisplayName/Quota?
				// QuotaBucket uses backend ID directly in toQuotaSnapshot.
				models = append(models, am)
			}
		}
	}
	return models
}

func parseEffortSuffix(model string) (base string, effort string) {
	switch {
	case strings.HasSuffix(model, "-extra-low"):
		return strings.TrimSuffix(model, "-extra-low"), "LOW"
	case strings.HasSuffix(model, "-high"):
		return strings.TrimSuffix(model, "-high"), "HIGH"
	case strings.HasSuffix(model, "-medium"):
		return strings.TrimSuffix(model, "-medium"), "MEDIUM"
	case strings.HasSuffix(model, "-low"):
		return strings.TrimSuffix(model, "-low"), "LOW"
	default:
		return model, ""
	}
}

func cleanDisplayName(displayName, id, base string) string {
	if displayName == "" || displayName == id {
		return base
	}
	for _, s := range []string{
		" (Extra Low)", " (High)", " (Medium)", " (Low)", " (Tiered)",
		"-extra-low", "-high", "-medium", "-low", "-tiered",
	} {
		displayName = strings.TrimSuffix(displayName, s)
	}
	return strings.TrimSpace(displayName)
}

func withThinkingLevel(req sdk.GenerateContentRequest, level string) sdk.GenerateContentRequest {
	if req.GenerationConfig == nil {
		req.GenerationConfig = &sdk.GenerationConfig{}
	} else {
		cfgCopy := *req.GenerationConfig
		req.GenerationConfig = &cfgCopy
	}
	if req.GenerationConfig.ThinkingConfig == nil {
		req.GenerationConfig.ThinkingConfig = &sdk.ThinkingConfig{
			IncludeThoughts: true,
		}
	} else {
		tcCopy := *req.GenerationConfig.ThinkingConfig
		req.GenerationConfig.ThinkingConfig = &tcCopy
	}
	if req.GenerationConfig.ThinkingConfig.ThinkingLevel == "" {
		req.GenerationConfig.ThinkingConfig.ThinkingLevel = strings.ToUpper(level)
	} else {
		req.GenerationConfig.ThinkingConfig.ThinkingLevel = strings.ToUpper(req.GenerationConfig.ThinkingConfig.ThinkingLevel)
	}
	return req
}
