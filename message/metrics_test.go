package message_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/masterkeysrd/loom/message"
)

func TestTokenMetricsAddTier(t *testing.T) {
	a := message.TokenMetrics{CostTier: "x", Priced: true}
	if got := a.Add(a); got.CostTier != "x" || !got.Priced {
		t.Fatalf("got %+v", got)
	}
	if got := a.Add(message.TokenMetrics{CostTier: "y", Priced: false}); got.CostTier != "" || got.Priced {
		t.Fatalf("got %+v", got)
	}
}

func TestTokenMetricsAddCredits(t *testing.T) {
	a := message.TokenMetrics{
		Credits: message.CreditMetrics{
			Amount:     0.25,
			Unit:       "ai_credits",
			Multiplier: 1.0,
			Status:     "available",
		},
	}
	b := message.TokenMetrics{
		Credits: message.CreditMetrics{
			Amount:     0.50,
			Unit:       "ai_credits",
			Multiplier: 1.0,
			Status:     "available",
		},
	}
	got := a.Add(b)
	if got.Credits.Amount != 0.75 || got.Credits.Unit != "ai_credits" || got.Credits.Multiplier != 1.0 || got.Credits.Status != "available" || got.Credits.Estimated {
		t.Fatalf("unexpected Credits sum: %+v", got.Credits)
	}

	// Incompatible units clear Amount/Unit, different multipliers clear Multiplier, unavailable wins Status.
	c := message.TokenMetrics{
		Credits: message.CreditMetrics{
			Amount:     0.12,
			Unit:       "usd",
			Multiplier: 0.33,
			Status:     "unavailable",
			Estimated:  true,
		},
	}
	mixed := a.Add(c)
	if mixed.Credits.Amount != 0 || mixed.Credits.Unit != "" || mixed.Credits.Multiplier != 0 || mixed.Credits.Status != "unavailable" || !mixed.Credits.Estimated {
		t.Fatalf("unexpected mixed Credits sum: %+v", mixed.Credits)
	}

	// Zero Credits should be omitted via omitzero in JSON marshaling.
	raw, err := json.Marshal(message.TokenMetrics{TotalTokens: 10})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(raw), `"credits"`) {
		t.Fatalf("expected zero Credits to be omitted, got %s", raw)
	}
}
