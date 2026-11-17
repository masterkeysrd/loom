package loomantigravity

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/providers/antigravity/sdk"
)

type (
	// QuotaBucket describes a single quota bucket or rate-limit window for a model.
	// Type alias to [llm.QuotaBucket].
	QuotaBucket = llm.QuotaBucket

	// QuotaBuckets is an ordered list of quota buckets constraining a model.
	// Type alias to [llm.QuotaBuckets].
	QuotaBuckets = llm.QuotaBuckets

	// QuotaSnapshot represents a point-in-time quota snapshot.
	// Type alias to [llm.QuotaSnapshot].
	QuotaSnapshot = llm.QuotaSnapshot

	// QuotaProvider is an optional interface implemented by providers that support
	// querying model quotas and rate-limit replenishment windows.
	// Type alias to [llm.QuotaProvider].
	QuotaProvider = llm.QuotaProvider
)

// GetQuotaSnapshot returns the current quota snapshot for all models and shared quota pools
// exposed by the backend. If QuotaCacheTTL is positive, results are cached up to that duration.
// If QuotaCacheTTL is negative (e.g. -1), it always queries the backend directly.
func (p *Provider) GetQuotaSnapshot(ctx context.Context) (QuotaSnapshot, error) {
	hasCache := func(s QuotaSnapshot) bool {
		return len(s.Buckets) > 0 || len(s.Models) > 0
	}

	p.cachedQuotasMu.RLock()
	if p.quotaTTL > 0 && time.Since(p.lastQuotaFetched) <= p.quotaTTL && hasCache(p.cachedQuotas) {
		res := cloneQuotaSnapshot(p.cachedQuotas)
		p.cachedQuotasMu.RUnlock()
		return res, nil
	}
	p.cachedQuotasMu.RUnlock()

	p.cachedQuotasMu.Lock()
	defer p.cachedQuotasMu.Unlock()

	if p.quotaTTL > 0 && time.Since(p.lastQuotaFetched) <= p.quotaTTL && hasCache(p.cachedQuotas) {
		return cloneQuotaSnapshot(p.cachedQuotas), nil
	}

	snap, err := p.fetchQuotaSnapshot(ctx)
	if err != nil {
		if hasCache(p.cachedQuotas) {
			return cloneQuotaSnapshot(p.cachedQuotas), nil
		}
		return QuotaSnapshot{}, err
	}

	p.cachedQuotas = snap
	p.lastQuotaFetched = time.Now()
	return cloneQuotaSnapshot(snap), nil
}

// GetQuota returns the quota buckets constraining a specific model name or alias.
// If the model name is a virtual alias (such as "gemini-3.8-flash" or "gemini-3.8-flash-high"),
// it resolves the alias to its underlying backend endpoint (e.g. "gemini-3.8-flash-tiered")
// and returns the buckets linked to that endpoint.
func (p *Provider) GetQuota(ctx context.Context, model string) (QuotaBuckets, bool, error) {
	snap, err := p.GetQuotaSnapshot(ctx)
	if err != nil {
		return nil, false, err
	}

	// 1. Direct match
	if buckets, ok := snap.ForModel(model); ok {
		return buckets, true, nil
	}

	// 2. Resolve target model via provider heuristic / alias resolver
	target := p.ResolveTargetModel(model)
	if target != model {
		if buckets, ok := snap.ForModel(target); ok {
			return buckets, true, nil
		}
	}

	// 3. Fallbacks for base name, tiered, and per-effort suffixes
	base, _ := parseEffortSuffix(model)
	for _, candidate := range []string{
		base,
		model + "-tiered",
		base + "-tiered",
		base + "-high",
		base + "-medium",
		base + "-low",
	} {
		if buckets, ok := snap.ForModel(candidate); ok {
			return buckets, true, nil
		}
	}

	return nil, false, nil
}

func (p *Provider) fetchQuotaSnapshot(ctx context.Context) (QuotaSnapshot, error) {
	rawModels, err := p.client.FetchAvailableModels(ctx)
	if err != nil {
		return QuotaSnapshot{}, err
	}
	p.recordAvailableModels(rawModels, p.currentAccessToken())

	// Best-effort enrichment with grouped 5h + weekly quota buckets.
	var groups []summaryGroupBuckets
	if summary, sumErr := p.client.RetrieveUserQuotaSummary(ctx); sumErr == nil {
		groups = convertSummaryGroups(summary)
	}

	snap := QuotaSnapshot{
		CollectedAt: time.Now(),
		Buckets:     make(map[string]QuotaBucket),
		Models:      make(map[string][]string),
	}
	for _, g := range groups {
		for _, b := range g.buckets {
			snap.Buckets[b.ID] = b
		}
	}

	for _, raw := range rawModels {
		switch raw.ID {
		case "chat_20706", "chat_23310", "tab_flash_lite_preview", "tab_jump_flash_lite_preview":
			continue
		}
		if raw.Quota == nil {
			continue
		}

		var bucketIDs []string
		if g, ok := matchSummaryGroup(raw, groups); ok {
			bucketIDs = g.bucketIDs
		} else {
			bucket := toModelQuotaBucket(raw.ID, raw.Quota)
			if _, exists := snap.Buckets[bucket.ID]; !exists {
				snap.Buckets[bucket.ID] = *bucket
			}
			bucketIDs = []string{bucket.ID}
		}

		snap.Models[raw.ID] = slices.Clone(bucketIDs)
		if baseID, ok := strings.CutSuffix(raw.ID, "-tiered"); ok {
			for _, suffix := range []string{"-high", "-medium", "-low", ""} {
				snap.Models[baseID+suffix] = slices.Clone(bucketIDs)
			}
		}
	}
	return snap, nil
}

type summaryGroupBuckets struct {
	family    string // "gemini", "3p", or ""
	bucketIDs []string
	buckets   []QuotaBucket
}

func convertSummaryGroups(summary *sdk.UserQuotaSummaryResponse) []summaryGroupBuckets {
	if summary == nil || len(summary.Groups) == 0 {
		return nil
	}
	out := make([]summaryGroupBuckets, 0, len(summary.Groups))
	for _, g := range summary.Groups {
		if len(g.Buckets) == 0 {
			continue
		}
		converted := make([]QuotaBucket, 0, len(g.Buckets))
		var family string
		lowerTitle := strings.ToLower(g.DisplayName)
		if strings.Contains(lowerTitle, "gemini") {
			family = "gemini"
		} else if strings.Contains(lowerTitle, "claude") || strings.Contains(lowerTitle, "gpt") {
			family = "3p"
		}

		for _, b := range g.Buckets {
			if b.BucketID == "" {
				continue
			}
			if family == "" {
				if strings.HasPrefix(b.BucketID, "gemini-") {
					family = "gemini"
				} else if strings.HasPrefix(b.BucketID, "3p-") {
					family = "3p"
				}
			}
			converted = append(converted, QuotaBucket{
				ID:                b.BucketID,
				Scope:             "account",
				Window:            parseQuotaWindow(b.Window),
				Unit:              llm.QuotaUnitPercent,
				RemainingFraction: b.RemainingFraction,
				ResetAt:           parseRFC3339Ptr(b.ResetTime),
			})
		}
		if len(converted) == 0 {
			continue
		}
		slices.SortStableFunc(converted, func(a, b QuotaBucket) int {
			switch {
			case a.Window == b.Window:
				return 0
			case a.Window == 0:
				return 1
			case b.Window == 0:
				return -1
			case a.Window < b.Window:
				return -1
			default:
				return 1
			}
		})
		ids := make([]string, len(converted))
		for i, b := range converted {
			ids[i] = b.ID
		}
		out = append(out, summaryGroupBuckets{
			family:    family,
			bucketIDs: ids,
			buckets:   converted,
		})
	}
	return out
}

func matchSummaryGroup(m sdk.AvailableModel, groups []summaryGroupBuckets) (summaryGroupBuckets, bool) {
	if len(groups) == 0 {
		return summaryGroupBuckets{}, false
	}
	wantFamily := ""
	switch m.ModelProvider {
	case "MODEL_PROVIDER_GOOGLE":
		wantFamily = "gemini"
	case "":
		lower := strings.ToLower(m.ID)
		switch {
		case strings.HasPrefix(lower, "gemini-"):
			wantFamily = "gemini"
		case strings.HasPrefix(lower, "claude-"), strings.HasPrefix(lower, "gpt-"):
			wantFamily = "3p"
		}
	default:
		wantFamily = "3p"
	}

	if wantFamily != "" {
		for _, g := range groups {
			if g.family == wantFamily {
				return g, true
			}
		}
	}

	if m.Quota != nil {
		if resetAt := parseRFC3339Ptr(m.Quota.ResetTime); resetAt != nil {
			for _, g := range groups {
				for _, b := range g.buckets {
					if b.ResetAt != nil && b.ResetAt.Equal(*resetAt) && b.RemainingFraction == m.Quota.RemainingFraction {
						return g, true
					}
				}
			}
		}
	}
	return summaryGroupBuckets{}, false
}

func toModelQuotaBucket(id string, raw *sdk.QuotaInfo) *QuotaBucket {
	if raw == nil {
		return nil
	}
	return &QuotaBucket{
		ID:                id,
		Scope:             "model",
		Unit:              llm.QuotaUnitPercent,
		RemainingFraction: raw.RemainingFraction,
		ResetAt:           parseRFC3339Ptr(raw.ResetTime),
	}
}

func parseRFC3339Ptr(raw string) *time.Time {
	if raw == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil
	}
	return &t
}

func parseQuotaWindow(window string) time.Duration {
	w := strings.ToLower(strings.TrimSpace(window))
	if w == "" {
		return 0
	}
	if w == "weekly" {
		return 7 * 24 * time.Hour
	}
	d, err := time.ParseDuration(w)
	if err != nil {
		return 0
	}
	return d
}

func cloneQuotaSnapshot(snap QuotaSnapshot) QuotaSnapshot {
	out := QuotaSnapshot{
		CollectedAt: snap.CollectedAt,
	}
	if len(snap.Buckets) > 0 {
		out.Buckets = make(map[string]QuotaBucket, len(snap.Buckets))
		for k, v := range snap.Buckets {
			if v.ResetAt != nil {
				t := *v.ResetAt
				v.ResetAt = &t
			}
			out.Buckets[k] = v
		}
	}
	if len(snap.Models) > 0 {
		out.Models = make(map[string][]string, len(snap.Models))
		for k, ids := range snap.Models {
			out.Models[k] = slices.Clone(ids)
		}
	}
	return out
}
