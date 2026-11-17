package loomclaudecode

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/providers/claudecode/internal/protocol"
)

// GetQuotaSnapshot queries the Claude CLI `/usage` command in headless
// `stream-json` mode and parses `usage_report.rate_limits` into a normalized
// [llm.QuotaSnapshot]. When authenticated with a `claude.ai` subscription
// credential, both rolling limit windows (such as 5-hour and 7-day caps) and
// `extra_usage` credit pools are populated; API-key or unauthenticated sessions
// return an empty snapshot without error.
func (p *Provider) GetQuotaSnapshot(ctx context.Context) (llm.QuotaSnapshot, error) {
	if ctx.Err() != nil {
		return llm.QuotaSnapshot{}, ctx.Err()
	}
	if p.proxy == nil {
		return llm.QuotaSnapshot{}, fmt.Errorf("claudecode: provider has nil session manager")
	}

	hasCache := func(s llm.QuotaSnapshot) bool {
		return len(s.Buckets) > 0 || len(s.Models) > 0 || !s.CollectedAt.IsZero()
	}

	p.cachedQuotasMu.RLock()
	if p.cfg.QuotaCacheTTL > 0 && time.Since(p.lastQuotaFetched) <= p.cfg.QuotaCacheTTL && hasCache(p.cachedQuotas) {
		res := cloneQuotaSnapshot(p.cachedQuotas)
		p.cachedQuotasMu.RUnlock()
		return res, nil
	}
	p.cachedQuotasMu.RUnlock()

	p.cachedQuotasMu.Lock()
	defer p.cachedQuotasMu.Unlock()

	if p.cfg.QuotaCacheTTL > 0 && time.Since(p.lastQuotaFetched) <= p.cfg.QuotaCacheTTL && hasCache(p.cachedQuotas) {
		return cloneQuotaSnapshot(p.cachedQuotas), nil
	}

	report, err := p.fetchUsageReport(ctx)
	if err != nil {
		if hasCache(p.cachedQuotas) {
			return cloneQuotaSnapshot(p.cachedQuotas), nil
		}
		return llm.QuotaSnapshot{}, err
	}

	snap := toQuotaSnapshot(report, p.cachedProfileIDs())
	p.cachedQuotas = snap
	p.lastQuotaFetched = time.Now()
	return cloneQuotaSnapshot(snap), nil
}

// GetQuota returns quota buckets constraining model (e.g. "sonnet", "opus",
// "claude-sonnet-4-6") or matching an exact bucket ID (such as "five_hour",
// "seven_day", or "extra_usage").
func (p *Provider) GetQuota(ctx context.Context, model string) (llm.QuotaBuckets, bool, error) {
	snap, err := p.GetQuotaSnapshot(ctx)
	if err != nil {
		return nil, false, err
	}

	key := strings.TrimSpace(model)
	if buckets, ok := snap.ForModel(key); ok {
		return buckets, true, nil
	}
	if b, ok := snap.Buckets[key]; ok {
		return llm.QuotaBuckets{b}, true, nil
	}

	lower := strings.ToLower(key)
	if lower != key {
		if buckets, ok := snap.ForModel(lower); ok {
			return buckets, true, nil
		}
		if b, ok := snap.Buckets[lower]; ok {
			return llm.QuotaBuckets{b}, true, nil
		}
	}

	// Fallback when snap.Models hasn't been populated from CLI initialize yet:
	// include account-wide limits plus any model-scoped buckets matching model.
	var matched llm.QuotaBuckets
	for _, b := range snap.Buckets {
		switch b.Scope {
		case "account":
			if b.ID != "extra_usage" {
				matched = append(matched, b)
			}
		case "model":
			if modelMatchesScope(lower, b.ID) {
				matched = append(matched, b)
			}
		}
	}
	if len(matched) > 0 {
		sortBuckets(matched)
		return matched, true, nil
	}
	if b, ok := snap.Buckets["extra_usage"]; ok {
		return llm.QuotaBuckets{b}, true, nil
	}
	return nil, false, nil
}

func (p *Provider) fetchUsageReport(ctx context.Context) (*protocol.UsageReport, error) {
	args := []string{
		"-p", "/usage",
		"--output-format", "stream-json",
		"--verbose",
		"--tools", "",
	}
	if len(p.cfg.ExtraArgs) > 0 {
		args = append(args, p.cfg.ExtraArgs...)
	}

	cmd := exec.CommandContext(ctx, p.cfg.CLIPath, args...)
	if p.cfg.WorkingDir != "" {
		cmd.Dir = p.cfg.WorkingDir
	}
	cmd.Env = p.proxy.buildEnv()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claudecode: usage stdout pipe: %w", err)
	}
	stderrBuf := newBoundedBuffer(8 * 1024)
	if p.cfg.TraceWriter != nil {
		cmd.Stderr = io.MultiWriter(stderrBuf, &lineTraceWriter{w: p.cfg.TraceWriter, prefix: "[stderr] "})
		_, _ = fmt.Fprintf(p.cfg.TraceWriter, "[spawn] %s %s\n", p.cfg.CLIPath, strings.Join(args, " "))
	} else {
		cmd.Stderr = stderrBuf
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claudecode: start %q for /usage: %w", p.cfg.CLIPath, err)
	}

	var (
		report    *protocol.UsageReport
		resultErr string
	)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		trimmed := bytes.TrimSpace(scanner.Bytes())
		if len(trimmed) == 0 {
			continue
		}
		if p.cfg.TraceWriter != nil {
			_, _ = fmt.Fprintf(p.cfg.TraceWriter, "[stdout] %s\n", trimmed)
		}
		var frame protocol.Frame
		if err := json.Unmarshal(trimmed, &frame); err != nil {
			continue
		}
		if frame.UsageReport != nil {
			report = frame.UsageReport
		}
		if frame.Type == "result" && frame.IsError {
			if len(frame.Errors) > 0 {
				resultErr = strings.Join(frame.Errors, "; ")
			} else if frame.Result != "" {
				resultErr = frame.Result
			} else {
				resultErr = "claude /usage returned error result"
			}
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()

	if waitErr != nil {
		if resultErr != "" {
			return nil, fmt.Errorf("claudecode: /usage failed: %s (%w)", resultErr, waitErr)
		}
		stderrStr := stderrBuf.String()
		if stderrStr != "" {
			return nil, fmt.Errorf("claudecode: /usage failed: %w (stderr=%s)", waitErr, stderrStr)
		}
		return nil, fmt.Errorf("claudecode: /usage failed: %w", waitErr)
	}
	if scanErr != nil {
		return nil, fmt.Errorf("claudecode: read /usage output: %w", scanErr)
	}
	if resultErr != "" {
		return nil, fmt.Errorf("claudecode: /usage failed: %s", resultErr)
	}
	return report, nil
}

func toQuotaSnapshot(report *protocol.UsageReport, modelIDs []string) llm.QuotaSnapshot {
	snap := llm.QuotaSnapshot{
		CollectedAt: time.Now(),
		Buckets:     make(map[string]llm.QuotaBucket),
		Models:      make(map[string][]string),
	}
	if report == nil || report.RateLimits == nil {
		return snap
	}

	var accountBucketIDs []string
	modelScoped := make(map[string][]string)

	for idx, entry := range report.RateLimits.Limits {
		id := uniqueLimitID(entry, idx, snap.Buckets)
		scopeModel := limitScopeModel(entry)
		scope := "account"
		if scopeModel != "" {
			scope = "model"
		}

		snap.Buckets[id] = llm.QuotaBucket{
			ID:                id,
			Scope:             scope,
			Window:            parseClaudeLimitWindow(entry.Kind),
			Unit:              llm.QuotaUnitPercent,
			RemainingFraction: clampFraction(1.0 - entry.Percent/100.0),
			ResetAt:           parseRFC3339Ptr(entry.ResetsAt),
		}

		if scopeModel == "" {
			accountBucketIDs = append(accountBucketIDs, id)
		} else {
			for _, mID := range modelIDs {
				if modelMatchesScope(mID, scopeModel) {
					modelScoped[mID] = append(modelScoped[mID], id)
				}
			}
		}
	}

	if len(report.RateLimits.Limits) > 0 {
		for _, mID := range modelIDs {
			ids := make([]string, 0, len(accountBucketIDs)+len(modelScoped[mID]))
			ids = append(ids, accountBucketIDs...)
			ids = append(ids, modelScoped[mID]...)
			if len(ids) > 0 {
				sortBucketIDs(ids, snap.Buckets)
				snap.Models[mID] = ids
			}
		}
	}

	if extra := report.RateLimits.ExtraUsage; extra != nil && extra.IsEnabled {
		var limit, used, usedPct float64
		if extra.MonthlyLimit != nil {
			limit = *extra.MonthlyLimit
		}
		if extra.UsedCredits != nil {
			used = *extra.UsedCredits
		}
		switch {
		case extra.Utilization != nil:
			usedPct = *extra.Utilization
		case limit > 0:
			usedPct = (used / limit) * 100.0
		}

		snap.Buckets["extra_usage"] = llm.QuotaBucket{
			ID:                "extra_usage",
			Scope:             "account",
			Unit:              llm.QuotaUnitCredits,
			Limit:             limit,
			Used:              used,
			RemainingFraction: clampFraction(1.0 - usedPct/100.0),
		}

		if len(report.RateLimits.Limits) == 0 {
			for _, mID := range modelIDs {
				snap.Models[mID] = []string{"extra_usage"}
			}
		}
	}

	return snap
}

func uniqueLimitID(entry protocol.RateLimitEntry, idx int, existing map[string]llm.QuotaBucket) string {
	base := strings.TrimSpace(entry.Kind)
	if base == "" {
		base = strings.TrimSpace(entry.Group)
	}
	if base == "" {
		base = fmt.Sprintf("limit_%d", idx)
	}
	if _, ok := existing[base]; !ok {
		return base
	}
	if entry.Scope != nil && entry.Scope.Model != nil && entry.Scope.Model.DisplayName != "" {
		slug := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(entry.Scope.Model.DisplayName), " ", "_"))
		cand := base + "_" + slug
		if _, ok := existing[cand]; !ok {
			return cand
		}
	}
	for n := 2; ; n++ {
		cand := fmt.Sprintf("%s_%d", base, n)
		if _, ok := existing[cand]; !ok {
			return cand
		}
	}
}

func limitScopeModel(entry protocol.RateLimitEntry) string {
	if entry.Scope != nil && entry.Scope.Model != nil && entry.Scope.Model.DisplayName != "" {
		return strings.ToLower(strings.TrimSpace(entry.Scope.Model.DisplayName))
	}
	lowerKind := strings.ToLower(entry.Kind)
	for _, family := range []string{"opus", "sonnet", "haiku"} {
		if strings.Contains(lowerKind, family) {
			return family
		}
	}
	return ""
}

func modelMatchesScope(modelID, scope string) bool {
	lowerID := strings.ToLower(modelID)
	lowerScope := strings.ToLower(strings.TrimSpace(scope))
	if lowerID == lowerScope || strings.Contains(lowerID, lowerScope) {
		return true
	}
	for _, family := range []string{"opus", "sonnet", "haiku"} {
		if strings.Contains(lowerScope, family) && strings.Contains(lowerID, family) {
			return true
		}
	}
	return false
}

func parseClaudeLimitWindow(kind string) time.Duration {
	lower := strings.ToLower(kind)
	switch {
	case strings.Contains(lower, "five_hour"), strings.Contains(lower, "5h"):
		return 5 * time.Hour
	case strings.Contains(lower, "seven_day"), strings.Contains(lower, "7d"), strings.Contains(lower, "weekly"):
		return 7 * 24 * time.Hour
	case strings.Contains(lower, "daily"), strings.Contains(lower, "1d"):
		return 24 * time.Hour
	default:
		return 0
	}
}

func parseRFC3339Ptr(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil
	}
	return &t
}

func clampFraction(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func sortBuckets(bs llm.QuotaBuckets) {
	slices.SortStableFunc(bs, func(a, b llm.QuotaBucket) int {
		if wa, wb := a.Window, b.Window; wa != wb {
			if wa == 0 {
				return 1
			}
			if wb == 0 {
				return -1
			}
			return cmp.Compare(wa, wb)
		}
		return cmp.Compare(a.ID, b.ID)
	})
}

func sortBucketIDs(ids []string, buckets map[string]llm.QuotaBucket) {
	slices.SortStableFunc(ids, func(a, b string) int {
		ba, bb := buckets[a], buckets[b]
		if wa, wb := ba.Window, bb.Window; wa != wb {
			if wa == 0 {
				return 1
			}
			if wb == 0 {
				return -1
			}
			return cmp.Compare(wa, wb)
		}
		return cmp.Compare(a, b)
	})
}

func cloneQuotaSnapshot(snap llm.QuotaSnapshot) llm.QuotaSnapshot {
	out := llm.QuotaSnapshot{
		CollectedAt: snap.CollectedAt,
	}
	if len(snap.Buckets) > 0 {
		out.Buckets = make(map[string]llm.QuotaBucket, len(snap.Buckets))
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
