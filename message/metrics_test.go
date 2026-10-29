package message_test

import (
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
