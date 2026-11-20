package loomcodex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/masterkeysrd/loom/profile"
	loomopenai "github.com/masterkeysrd/loom/providers/openai"
)

// Option configures a Codex provider.
type Option func(*Provider)

// WithProfileCacheTTL sets how long discovery results remain available. The
// default is one hour. A nonpositive duration disables caching.
func WithProfileCacheTTL(ttl time.Duration) Option {
	return func(p *Provider) { p.cacheTTL = ttl }
}

var openAICatalog = loomopenai.NewProvider(nil)

type accountModel struct {
	Slug          string `json:"slug"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
	Visibility    string `json:"visibility"`
	ContextWindow int    `json:"context_window"`
}

type modelCatalog struct {
	models  []accountModel
	expires time.Time
}

func (p *Provider) freshCatalog() *modelCatalog {
	c := p.catalog.Load()
	if c != nil && time.Now().Before(c.expires) {
		return c
	}
	return nil
}

func routeModalities(ms []profile.Modality) []profile.Modality {
	return slices.DeleteFunc(slices.Clone(ms), func(m profile.Modality) bool {
		return m == profile.ModalityAudio || m == profile.ModalityVideo
	})
}

func (p *Provider) resolve(m accountModel) profile.Profile {
	if override, ok := p.overrides.Load(m.Slug); ok {
		return override.(profile.Profile)
	}
	prof, _ := openAICatalog.GetProfile(m.Slug)
	prof.ID = m.Slug
	if m.DisplayName != "" {
		prof.Name = m.DisplayName
	}
	if prof.Name == "" {
		prof.Name = m.Slug
	}
	if m.Description != "" {
		prof.Description = m.Description
	}
	if m.ContextWindow > 0 {
		prof.Limits.Context = m.ContextWindow
	}
	prof.Capabilities.Temperature = false
	prof.Capabilities.Embedding = false
	prof.Capabilities.Decision = false
	prof.Modalities.Inputs = routeModalities(prof.Modalities.Inputs)
	prof.Modalities.Outputs = routeModalities(prof.Modalities.Outputs)
	options := slices.Clone(prof.Capabilities.ReasoningOptions)
	for i := range options {
		options[i].Values = slices.Clone(options[i].Values)
	}
	prof.Capabilities.ReasoningOptions = options
	// The subscription route does not use public API pricing.
	prof.Pricing = profile.Pricing{}
	return prof
}

// GetProfile returns an override or an unexpired discovered profile without network I/O.
func (p *Provider) GetProfile(id string) (profile.Profile, bool) {
	if v, ok := p.overrides.Load(id); ok {
		return v.(profile.Profile), true
	}
	if c := p.freshCatalog(); c != nil {
		for _, m := range c.models {
			if m.Slug == id {
				return p.resolve(m), true
			}
		}
	}
	return profile.Profile{}, false
}

// ListProfiles lists unexpired discovered profiles and explicit overrides by ID.
func (p *Provider) ListProfiles() []profile.Profile {
	byID := map[string]profile.Profile{}
	if c := p.freshCatalog(); c != nil {
		for _, m := range c.models {
			byID[m.Slug] = p.resolve(m)
		}
	}
	p.overrides.Range(func(k, v any) bool {
		byID[k.(string)] = v.(profile.Profile)
		return true
	})
	var result []profile.Profile
	for _, v := range byID {
		result = append(result, v)
	}
	slices.SortFunc(result, func(a, b profile.Profile) int { return strings.Compare(a.ID, b.ID) })
	return result
}

// SearchProfiles filters locally available profiles by ID or display name.
func (p *Provider) SearchProfiles(query string) []profile.Profile {
	return profile.Search(p.ListProfiles(), query)
}

// OverrideProfile registers metadata for id independently of account discovery.
func (p *Provider) OverrideProfile(id string, prof profile.Profile) {
	prof.ID = id
	p.overrides.Store(id, prof)
}

// ListModels returns visible models in account order, refreshing an expired catalog.
func (p *Provider) ListModels(ctx context.Context) ([]profile.Profile, error) {
	c := p.freshCatalog()
	if c == nil {
		select {
		case p.refresh <- struct{}{}:
			defer func() { <-p.refresh }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		c = p.freshCatalog()
		if c == nil {
			models, err := p.fetchModels(ctx)
			if err != nil {
				return nil, err
			}
			c = &modelCatalog{models: models, expires: time.Now().Add(p.cacheTTL)}
			if p.cacheTTL > 0 {
				p.catalog.Store(c)
			}
		}
	}
	result := make([]profile.Profile, 0, len(c.models))
	for _, m := range c.models {
		result = append(result, p.resolve(m))
	}
	return result, nil
}

func (p *Provider) fetchModels(ctx context.Context) ([]accountModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex: list models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("codex: list models: HTTP %d", resp.StatusCode)
	}

	var catalog struct {
		Models []accountModel `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
		return nil, fmt.Errorf("codex: decode models: %w", err)
	}
	if catalog.Models == nil {
		return nil, errors.New("codex: missing models array")
	}
	return slices.DeleteFunc(catalog.Models, func(m accountModel) bool { return m.Visibility != "list" || m.Slug == "" }), nil
}

// GetModel resolves an explicit override or looks up a visible account model.
func (p *Provider) GetModel(ctx context.Context, id string) (profile.Profile, error) {
	if prof, ok := p.GetProfile(id); ok {
		return prof, nil
	}
	models, err := p.ListModels(ctx)
	if err != nil {
		return profile.Profile{}, err
	}
	for _, m := range models {
		if m.ID == id {
			return m, nil
		}
	}
	return profile.Profile{}, fmt.Errorf("codex: model %q is not in the account catalog", id)
}
