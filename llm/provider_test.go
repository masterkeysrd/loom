package llm_test

import (
	"testing"
	"time"

	"github.com/masterkeysrd/loom/llm"
)

func TestQuotaBucketHelpers(t *testing.T) {
	reset := time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC)
	b := llm.QuotaBucket{
		ID:                "5h",
		Scope:             "account",
		Window:            5 * time.Hour,
		Unit:              llm.QuotaUnitRequests,
		Limit:             100,
		Used:              25,
		RemainingFraction: 0.75,
		ResetAt:           &reset,
	}
	if b.Percentage() != 75 {
		t.Fatalf("Percentage() = %v, want 75", b.Percentage())
	}
	if b.Remaining() != 75 {
		t.Fatalf("Remaining() = %v, want 75", b.Remaining())
	}
	if b.Exhausted() {
		t.Fatalf("expected Exhausted() == false")
	}

	over := llm.QuotaBucket{Limit: 10, Used: 12, RemainingFraction: 0}
	if over.Remaining() != 0 {
		t.Fatalf("Remaining() clamped = %v, want 0", over.Remaining())
	}
	if !over.Exhausted() {
		t.Fatalf("expected Exhausted() == true")
	}

	unlimited := llm.QuotaBucket{Unlimited: true, RemainingFraction: 0}
	if unlimited.Exhausted() {
		t.Fatalf("unlimited bucket must not be Exhausted()")
	}
}

func TestQuotaBucketsBottleneckAndGet(t *testing.T) {
	if _, ok := (llm.QuotaBuckets{}).Bottleneck(); ok {
		t.Fatalf("expected false for empty QuotaBuckets")
	}

	bs := llm.QuotaBuckets{
		{ID: "chat", Unlimited: true, RemainingFraction: 1.0},
		{ID: "7d", RemainingFraction: 0.60},
		{ID: "5h", RemainingFraction: 0.20},
	}
	b, ok := bs.Bottleneck()
	if !ok || b.ID != "5h" {
		t.Fatalf("Bottleneck() = %+v, %v; want id=5h", b, ok)
	}

	got, ok := bs.Get("7d")
	if !ok || got.RemainingFraction != 0.60 {
		t.Fatalf("Get(7d) = %+v, %v", got, ok)
	}
	if _, ok := bs.Get("missing"); ok {
		t.Fatalf("expected Get(missing) == false")
	}

	// Metered bucket always beats an unlimited bucket even if the unlimited one reports 0.
	mixed := llm.QuotaBuckets{
		{ID: "unlimited", Unlimited: true, RemainingFraction: 0},
		{ID: "metered", RemainingFraction: 0.9},
	}
	b, ok = mixed.Bottleneck()
	if !ok || b.ID != "metered" {
		t.Fatalf("Bottleneck() with unlimited = %+v, %v; want metered", b, ok)
	}
}

func TestQuotaSnapshotForModel(t *testing.T) {
	snap := llm.QuotaSnapshot{
		Buckets: map[string]llm.QuotaBucket{
			"5h": {ID: "5h", Window: 5 * time.Hour, RemainingFraction: 0.8},
			"7d": {ID: "7d", Window: 7 * 24 * time.Hour, RemainingFraction: 0.5},
		},
		Models: map[string][]string{
			"claude-sonnet": {"5h", "7d"},
			"dangling":      {"missing"},
		},
	}

	buckets, ok := snap.ForModel("claude-sonnet")
	if !ok || len(buckets) != 2 || buckets[0].ID != "5h" || buckets[1].ID != "7d" {
		t.Fatalf("ForModel(claude-sonnet) = %+v, %v", buckets, ok)
	}
	b, ok := buckets.Bottleneck()
	if !ok || b.ID != "7d" {
		t.Fatalf("Bottleneck = %+v, %v; want 7d", b, ok)
	}

	if _, ok := snap.ForModel("unknown"); ok {
		t.Fatalf("expected ForModel(unknown) == false")
	}
	if _, ok := snap.ForModel("dangling"); ok {
		t.Fatalf("expected ForModel(dangling) == false")
	}
}
