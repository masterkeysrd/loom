---
title: Inspect quotas and costs
---

Inspect quota buckets before making a request. After a response, read its metrics to estimate cost or see provider-reported credits.

## Check available quota

Providers that expose quota or rolling-window capacity metrics (such as Google Antigravity and GitHub Copilot) implement the [`llm.QuotaProvider`](https://pkg.go.dev/github.com/masterkeysrd/loom/llm#QuotaProvider) interface. This allows callers to inspect multi-bucket constraints (e.g. rolling burst windows, weekly caps, monthly request pools) and reset times:

```go
// Type-assert to llm.QuotaProvider
if qp, ok := provider.(llm.QuotaProvider); ok {
    // Inspect all buckets constraining a specific model (resolving aliases like "gemini-3.8-flash")
    if buckets, ok, err := qp.GetQuota(ctx, "gemini-3.8-flash"); ok && err == nil {
        if bottleneck, ok := buckets.Bottleneck(); ok {
            fmt.Printf("Remaining: %.1f%%\n", bottleneck.Percentage())
            if bottleneck.ResetAt != nil {
                fmt.Printf("Replenishes at: %s\n", bottleneck.ResetAt.Format(time.RFC3339))
            }
        }
    }

    // Retrieve the full snapshot separating shared buckets from model bindings
    snap, _ := qp.GetQuotaSnapshot(ctx)
    for id, bucket := range snap.Buckets {
        fmt.Printf("• %s: %.1f%%\n", id, bucket.Percentage())
    }
}
```

## Interpret costs and credits

`profile.Profile.Estimate(tokens)` returns a `CostEstimate` (`Details`, `Total`, `Tier`, `Priced`). `Model.Stream` copies these onto `chunk.Metrics` (`Cost`, `TotalCost`, `CostTier`, `Priced`) for LLM, embedding, and decision models.

When a provider also reports server-side billing or quota-unit consumption on a turn (for example, Copilot's `ai_credits` / `premium_requests` and billing `Multiplier`, or Claude Code's `total_cost_usd` reported as `Unit: "usd", Estimated: true`), those values are populated on `chunk.Metrics.Credits` (`message.CreditMetrics`) independently of static profile cost estimation.

- **Token semantics**: `message.TokenDetails.Input` is the total prompt size and *includes* cache read/write tokens (they are subsets of `Input`). Provider mappings normalize to this rule. The uncached part, `Input - CacheRead - CacheWrite`, is billed at the input price.
- **Tiers**: a `TierPricing` has a `Condition{Type, Size}`. `TierConditionContext` measures `Input` (output excluded) and applies when it is **strictly greater** than `Size`. Of the matching tiers, the one with the highest `Size` wins regardless of slice order. Unknown condition types never match. A tier without a `Label` gets a generated one such as `context>200000`.
- **Priced**: `false` when the profile has no pricing data (nil profile, or `Pricing.IsZero()`: all prices zero and no tiers).
- **Middleware**: cost is attached *after* the middleware chain. Middleware that needs it should use `llm.RuntimeFromContext(ctx)` and call `rt.Profile.Estimate(...)`.
- **Injecting a profile**: `model.WithProfile(p)` returns a clone that uses `p` for cost estimation and `Runtime.Profile`. `WithProfile(nil)` clears it. `ModelConfig` defaults derived from the profile are not recomputed. `embedding.Model` and `decision.Model` have the same setter.
- `EstimateCost` and `TierPricing.TierLimit` were removed; use `Estimate` and `TierPricing.Condition`.
