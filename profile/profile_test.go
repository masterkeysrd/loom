package profile_test

import (
	"testing"

	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
)

func TestSearch(t *testing.T) {
	catalog := []profile.Profile{
		{ID: "gpt-4o", Name: "GPT-4o"},
		{ID: "text-embedding-3-small", Name: "Text Embedding 3 Small"},
		{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
	}

	// Empty query returns all profiles
	if got := profile.Search(catalog, ""); len(got) != 3 {
		t.Fatalf("expected 3 profiles for empty query, got %d", len(got))
	}

	// Case-insensitive match on ID or Name
	got := profile.Search(catalog, "EMBEDDING")
	if len(got) != 1 || got[0].ID != "text-embedding-3-small" {
		t.Fatalf("unexpected search results: %+v", got)
	}
}

func TestEstimateCost(t *testing.T) {
	p := profile.Profile{
		ID: "test-model",
		Pricing: profile.Pricing{
			Input:  2.0, // $2 per 1M => 2000 nano-dollars per token
			Output: 8.0, // $8 per 1M => 8000 nano-dollars per token
			TieredLimits: []profile.TierPricing{
				{
					Condition: profile.TierCondition{Type: profile.TierConditionContext, Size: 200000},
					Input:     4.0,
					Output:    16.0,
				},
			},
		},
	}

	est := p.Estimate(message.TokenDetails{
		Input:  1000,
		Output: 500,
	})
	details, total := est.Details, est.Total
	wantTotal := message.Cost(1000*2000 + 500*8000)
	if total != wantTotal || details.Input != 1000*2000 || details.Output != 500*8000 {
		t.Fatalf("expected total=%v, got total=%v, details=%+v", wantTotal, total, details)
	}

	// Over 200k tier pricing
	totalTier := p.Estimate(message.TokenDetails{
		Input:  250000,
		Output: 1000,
	}).Total
	wantTier := message.Cost(250000*4000 + 1000*16000)
	if totalTier != wantTier {
		t.Fatalf("expected tiered total=%v, got %v", wantTier, totalTier)
	}
}

func ctxTier(label string, size int, in, out float64) profile.TierPricing {
	return profile.TierPricing{
		Label:     label,
		Condition: profile.TierCondition{Type: profile.TierConditionContext, Size: size},
		Input:     in,
		Output:    out,
	}
}

func TestEstimateTierSelection(t *testing.T) {
	base := profile.Pricing{Input: 1, Output: 2, CacheRead: 0.1}
	asc := []profile.TierPricing{ctxTier("a", 100, 2, 4), ctxTier("", 200, 3, 6)}
	desc := []profile.TierPricing{asc[1], asc[0]}

	tests := []struct {
		name    string
		details message.TokenDetails
		tier    string
	}{
		{"below", message.TokenDetails{Input: 50}, ""},
		{"boundary is exclusive", message.TokenDetails{Input: 100}, ""},
		{"first tier", message.TokenDetails{Input: 101}, "a"},
		{"highest wins", message.TokenDetails{Input: 201}, "context>200"},
		{"cache counts toward context", message.TokenDetails{Input: 205, CacheRead: 195}, "context>200"},
		{"output excluded", message.TokenDetails{Input: 10, Output: 5000}, ""},
	}
	for _, tt := range tests {
		for name, tiers := range map[string][]profile.TierPricing{"asc": asc, "desc": desc} {
			pr := base
			pr.TieredLimits = tiers
			got := profile.Profile{Pricing: pr}.Estimate(tt.details)
			if got.Tier != tt.tier {
				t.Errorf("%s/%s: tier = %q, want %q", tt.name, name, got.Tier, tt.tier)
			}
			if !got.Priced {
				t.Errorf("%s/%s: expected Priced", tt.name, name)
			}
		}
	}
}

func TestEstimateUnknownCondition(t *testing.T) {
	p := profile.Profile{Pricing: profile.Pricing{Input: 1, TieredLimits: []profile.TierPricing{
		{Condition: profile.TierCondition{Type: "weird", Size: 1}, Input: 9},
	}}}
	if got := p.Estimate(message.TokenDetails{Input: 5}); got.Tier != "" || got.Details.Input != 5*1000 {
		t.Fatalf("unknown type must fall back to base: %+v", got)
	}
}

func TestEstimatePriced(t *testing.T) {
	if (profile.Profile{}).Estimate(message.TokenDetails{Input: 1}).Priced {
		t.Fatal("empty pricing must be unpriced")
	}
	free := profile.Profile{Pricing: profile.Pricing{TieredLimits: []profile.TierPricing{ctxTier("x", 1, 0, 0)}}}
	if !free.Estimate(message.TokenDetails{}).Priced {
		t.Fatal("pricing with tiers must be priced")
	}
}
