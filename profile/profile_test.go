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
					TierLimit: 200000,
					Input:     4.0,
					Output:    16.0,
				},
			},
		},
	}

	details, total := p.EstimateCost(message.TokenDetails{
		Input:  1000,
		Output: 500,
	})
	wantTotal := message.Cost(1000*2000 + 500*8000)
	if total != wantTotal || details.Input != 1000*2000 || details.Output != 500*8000 {
		t.Fatalf("expected total=%v, got total=%v, details=%+v", wantTotal, total, details)
	}

	// Over 200k tier pricing
	_, totalTier := p.EstimateCost(message.TokenDetails{
		Input:  250000,
		Output: 1000,
	})
	wantTier := message.Cost(250000*4000 + 1000*16000)
	if totalTier != wantTier {
		t.Fatalf("expected tiered total=%v, got %v", wantTier, totalTier)
	}
}
