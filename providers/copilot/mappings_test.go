package loomcopilot

import (
	"slices"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/tool"
)

func TestToTokenMetricsNormalization(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if m := toTokenMetrics(nil); m != nil {
			t.Fatalf("expected nil metrics, got %+v", m)
		}
	})

	t.Run("openai includes cache_read in input", func(t *testing.T) {
		in := int64(1200)
		out := int64(150)
		cr := int64(800)
		dur := int64(320)
		ep := copilot.AssistantUsageAPIEndpointResponses

		m := toTokenMetrics(&copilot.AssistantUsageData{
			InputTokens:     &in,
			OutputTokens:    &out,
			CacheReadTokens: &cr,
			Duration:        &dur,
			APIEndpoint:     &ep,
		})
		if m.Tokens.Input != 1200 || m.Tokens.CacheRead != 800 || m.Tokens.Output != 150 {
			t.Fatalf("unexpected token breakdown: %+v", m.Tokens)
		}
		if m.TotalTokens != 1350 {
			t.Fatalf("expected TotalTokens=1350, got %d", m.TotalTokens)
		}
		if m.Timing.Total != 320*time.Millisecond {
			t.Fatalf("expected duration 320ms, got %v", m.Timing.Total)
		}
	})

	t.Run("credits from copilotUsage and multiplier", func(t *testing.T) {
		cost := 1.5
		status := copilot.AiCreditsStatusComplete
		m := toTokenMetrics(&copilot.AssistantUsageData{
			Cost:            &cost,
			AiCreditsStatus: &status,
			CopilotUsage: &copilot.AssistantUsageCopilotUsage{
				TotalNanoAiu: 250_000_000,
			},
		})
		if m.Credits.Amount != 0.25 || m.Credits.Unit != "ai_credits" || m.Credits.Multiplier != 1.5 || m.Credits.Status != "complete" || m.Credits.Estimated {
			t.Fatalf("unexpected credits: %+v", m.Credits)
		}
	})

	t.Run("credits fallback to multiplier as premium_requests", func(t *testing.T) {
		cost := 0.33
		m := toTokenMetrics(&copilot.AssistantUsageData{
			Cost: &cost,
		})
		if m.Credits.Amount != 0.33 || m.Credits.Unit != "premium_requests" || m.Credits.Multiplier != 0.33 {
			t.Fatalf("unexpected fallback credits: %+v", m.Credits)
		}
	})

	t.Run("anthropic v1_messages sums cache into input", func(t *testing.T) {
		in := int64(100)
		out := int64(50)
		cr := int64(500)
		cw := int64(200)
		reasoning := int64(40)
		ep := copilot.AssistantUsageAPIEndpointV1Messages

		m := toTokenMetrics(&copilot.AssistantUsageData{
			InputTokens:      &in,
			OutputTokens:     &out,
			CacheReadTokens:  &cr,
			CacheWriteTokens: &cw,
			ReasoningTokens:  &reasoning,
			APIEndpoint:      &ep,
		})
		if m.Tokens.Input != 800 {
			t.Fatalf("expected Input=800 (100+500+200), got %d", m.Tokens.Input)
		}
		if m.TotalTokens != 850 {
			t.Fatalf("expected TotalTokens=850, got %d", m.TotalTokens)
		}
		if m.Tokens.Reasoning != 40 {
			t.Fatalf("expected Reasoning=40, got %d", m.Tokens.Reasoning)
		}
	})
}

func TestSplitSystemAndConversation(t *testing.T) {
	msgs := []message.Message{
		message.NewSystem(&message.TextBlock{Text: "Sys 1"}),
		message.NewSystem(&message.TextBlock{Text: "Sys 2"}),
		message.NewUser(&message.TextBlock{Text: "Hi"}),
		message.NewSystem(&message.TextBlock{Text: "Mid system note"}),
	}

	sys, conv := splitSystemAndConversation(msgs)
	if sys != "Sys 1\n\nSys 2" {
		t.Fatalf("unexpected system prompt: %q", sys)
	}
	if len(conv) != 2 {
		t.Fatalf("expected 2 conversation messages, got %d", len(conv))
	}
	if conv[1].Role() != message.RoleSystem {
		t.Fatalf("expected mid-conversation system message preserved, got %s", conv[1].Role())
	}
}

func TestBuildToolFilterLists(t *testing.T) {
	avail, excl := buildToolFilterLists(nil, nil)
	if avail == nil || len(avail) != 0 {
		t.Fatalf("expected non-nil empty AvailableTools, got %#v", avail)
	}
	if len(excl) != 1 || excl[0] != "builtin:*" {
		t.Fatalf("expected ExcludedTools=[builtin:*], got %#v", excl)
	}

	tools := []tool.Definition{{Name: "shell_command"}}
	avail, excl = buildToolFilterLists(tools, nil)
	if len(avail) != 1 || avail[0] != "custom:*" {
		t.Fatalf("expected AvailableTools=[custom:*], got %#v", avail)
	}
	if len(excl) != 1 || excl[0] != "builtin:*" {
		t.Fatalf("expected ExcludedTools=[builtin:*], got %#v", excl)
	}

	avail, excl = buildToolFilterLists(tools, []string{"view", "grep"})
	if len(avail) != 3 || avail[0] != "builtin:view" || avail[1] != "builtin:grep" || avail[2] != "custom:*" {
		t.Fatalf("unexpected AvailableTools with native tools: %#v", avail)
	}
	if len(excl) != 0 {
		t.Fatalf("expected nil ExcludedTools, got %#v", excl)
	}
}

func TestPrefixDigestChangesOnMutations(t *testing.T) {
	req := &llm.Request{
		Model: "gpt-5",
		Tools: []tool.Definition{{
			Name:        "shell_command",
			Description: "run shell",
			InputSchema: &jsonschema.Schema{Type: "object"},
		}},
	}
	prefix := []message.Message{
		message.NewUser(&message.TextBlock{Text: "hello"}),
	}

	base := computePrefixDigest(req, "sys", prefix, Session{}, nil, "/repo")
	if same := computePrefixDigest(req, "sys", prefix, Session{}, nil, "/repo"); same != base {
		t.Fatalf("expected deterministic digest, got %q != %q", same, base)
	}

	edited := []message.Message{
		message.NewUser(&message.TextBlock{Text: "hello edited"}),
	}
	if changed := computePrefixDigest(req, "sys", edited, Session{}, nil, "/repo"); changed == base {
		t.Fatal("expected digest to change when message text changes")
	}

	req2 := *req
	req2.Model = "claude-sonnet-4.6"
	if changed := computePrefixDigest(&req2, "sys", prefix, Session{}, nil, "/repo"); changed == base {
		t.Fatal("expected digest to change when model changes")
	}
}

func TestMapModelInfoAndQuota(t *testing.T) {
	maxWin := 200000
	maxOut := 16384
	prof := mapModelInfoToProfile(copilot.ModelInfo{
		ID:   "gpt-5",
		Name: "GPT-5",
		Capabilities: copilot.ModelCapabilities{
			Supports: copilot.ModelSupports{Vision: true, ReasoningEffort: true},
			Limits:   copilot.ModelLimits{MaxContextWindowTokens: &maxWin, MaxOutputTokens: &maxOut},
		},
		SupportedReasoningEfforts: []string{"low", "medium", "high"},
		DefaultReasoningEffort:    "medium",
	})

	if prof.Capabilities.Temperature {
		t.Fatal("expected Temperature capability false for Copilot SDK")
	}
	if !prof.Capabilities.Reasoning || len(prof.Capabilities.ReasoningOptions) != 1 {
		t.Fatalf("unexpected reasoning capabilities: %+v", prof.Capabilities)
	}
	if prof.Limits.Context != 200000 || prof.Limits.Output != 16384 {
		t.Fatalf("unexpected limits: %+v", prof.Limits)
	}

	reset := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	b := toQuotaBucket("premium_interactions", rpc.AccountQuotaSnapshot{
		EntitlementRequests: 300,
		UsedRequests:        52,
		RemainingPercentage: 82.5,
		Overage:             1.5,
		ResetDate:           &reset,
	})
	if b.ID != "premium_interactions" || b.Scope != "account" || b.Unit != llm.QuotaUnitRequests {
		t.Fatalf("unexpected bucket identity/unit: %+v", b)
	}
	if b.Limit != 300 || b.Used != 52 || b.Overage != 1.5 {
		t.Fatalf("unexpected bucket counters: %+v", b)
	}
	if b.RemainingFraction != 0.825 {
		t.Fatalf("expected RemainingFraction=0.825, got %v", b.RemainingFraction)
	}
	if b.ResetAt == nil || !b.ResetAt.Equal(reset) {
		t.Fatalf("unexpected ResetAt: %v", b.ResetAt)
	}
}

func TestMapModelInfoStaticEnrichment(t *testing.T) {
	t.Run("prefers live limits and forces temperature false", func(t *testing.T) {
		maxWin := 500000
		maxOut := 32000
		prof := mapModelInfoToProfile(copilot.ModelInfo{
			ID:   "gpt-5.4",
			Name: "GPT 5.4 Live",
			Capabilities: copilot.ModelCapabilities{
				Supports: copilot.ModelSupports{Vision: false, ReasoningEffort: true},
				Limits:   copilot.ModelLimits{MaxContextWindowTokens: &maxWin, MaxOutputTokens: &maxOut},
			},
			SupportedReasoningEfforts: []string{"low", "high"},
			DefaultReasoningEffort:    "low",
		})

		if prof.Name != "GPT 5.4 Live" {
			t.Fatalf("expected live Name preserved, got %q", prof.Name)
		}
		if prof.Capabilities.Temperature {
			t.Fatal("expected Temperature capability forced false even when static profile enables it")
		}
		if !prof.Capabilities.Attachment {
			t.Fatal("expected Attachment enriched from static profile")
		}
		if prof.Limits.Context != 500000 || prof.Limits.Output != 32000 {
			t.Fatalf("expected live limits preserved, got %+v", prof.Limits)
		}
		if len(prof.Capabilities.ReasoningOptions) != 1 || !slices.Equal(prof.Capabilities.ReasoningOptions[0].Values, []string{"low", "high"}) || prof.Capabilities.ReasoningOptions[0].Default != "low" {
			t.Fatalf("expected live ReasoningOptions preserved, got %+v", prof.Capabilities.ReasoningOptions)
		}
		if prof.Family != "gpt" || prof.Knowledge != "2025-08-31" || prof.ReleaseDate != "2026-03-05" || prof.Description == "" {
			t.Fatalf("expected static metadata enriched, got %+v", prof)
		}
		if prof.Pricing.Input != 2.5 || prof.Pricing.Output != 15 || len(prof.Pricing.TieredLimits) != 1 {
			t.Fatalf("expected static pricing and tiers enriched, got %+v", prof.Pricing)
		}
		if !slices.Contains(prof.Modalities.Inputs, profile.ModalityImage) || !slices.Contains(prof.Modalities.Inputs, profile.ModalityPDF) {
			t.Fatalf("expected static image and PDF modalities merged, got %+v", prof.Modalities.Inputs)
		}
	})

	t.Run("fills missing limits and reasoning from static profile", func(t *testing.T) {
		prof := mapModelInfoToProfile(copilot.ModelInfo{
			ID: "gpt-5.4",
		})
		if prof.Name != "GPT-5.4" {
			t.Fatalf("expected static friendly Name, got %q", prof.Name)
		}
		if prof.Limits.Context != 1050000 || prof.Limits.Output != 128000 {
			t.Fatalf("expected static limits fallback, got %+v", prof.Limits)
		}
		if !prof.Capabilities.Reasoning || len(prof.Capabilities.ReasoningOptions) != 1 {
			t.Fatalf("expected static reasoning options fallback, got %+v", prof.Capabilities)
		}
		if prof.Capabilities.Temperature {
			t.Fatal("expected Temperature forced false")
		}
	})
}

func TestMapThinkingToReasoningEffort(t *testing.T) {
	if got := mapThinkingToReasoningEffort(nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := mapThinkingToReasoningEffort(&llm.ThinkingConfig{Effort: "HIGH"}); got != "high" {
		t.Fatalf("expected high, got %q", got)
	}
	if got := mapThinkingToReasoningEffort(&llm.ThinkingConfig{Budget: 8192}); got != "medium" {
		t.Fatalf("expected medium, got %q", got)
	}
	if got := mapThinkingToReasoningEffort(&llm.ThinkingConfig{Enabled: true}); got != "medium" {
		t.Fatalf("expected medium, got %q", got)
	}
}
