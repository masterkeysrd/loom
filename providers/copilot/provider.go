package loomcopilot

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/stream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	_ llm.Provider            = (*Provider)(nil)
	_ profile.Provider        = (*Provider)(nil)
	_ llm.ModelLister         = (*Provider)(nil)
	_ llm.QuotaProvider       = (*Provider)(nil)
	_ llm.IdleMessageNotifier = (*Provider)(nil)
)

// Provider implements [llm.Provider] using the official GitHub Copilot Go SDK.
type Provider struct {
	cfg        Config
	mgr        *sessionManager
	discovered sync.Map
	overrides  sync.Map
}

// NewDefaultProvider creates a [Provider] configured with defaults and optional
// functional [Option] overrides.
func NewDefaultProvider(opts ...Option) (*Provider, error) {
	var cfg Config
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return NewProvider(&cfg)
}

// NewProvider creates a [Provider] from cfg (or defaults if cfg is nil).
func NewProvider(cfg *Config) (*Provider, error) {
	norm := normalizeConfig(cfg)
	mgr, err := newSessionManager(norm)
	if err != nil {
		return nil, err
	}
	return &Provider{
		cfg: norm,
		mgr: mgr,
	}, nil
}

// Name returns "copilot", uniquely identifying this provider in an [llm.Registry].
func (p *Provider) Name() string {
	return "copilot"
}

// OnIdleMessage registers fn to receive unsolicited [message.Message]
// notifications emitted by idle Copilot sessions (such as background compaction,
// truncation, or unexpected runtime shutdowns) and returns an unsubscribe func.
func (p *Provider) OnIdleMessage(fn llm.IdleMessageHandler) func() {
	if p.mgr == nil {
		return func() {}
	}
	return p.mgr.addIdleListener(fn)
}

// Close disconnects all active/parked Copilot sessions and stops the SDK client.
func (p *Provider) Close() error {
	if p.mgr == nil {
		return nil
	}
	return p.mgr.close()
}

// Stream routes request through the Copilot SDK session manager (Path A
// live/parked session match or Path C fresh session) and yields
// [message.AssistantChunk] values as the SDK emits streaming session events.
func (p *Provider) Stream(ctx context.Context, request *llm.Request) (llm.StreamResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("copilot: request cannot be nil")
	}
	if p.mgr == nil {
		return nil, fmt.Errorf("copilot: provider has nil session manager")
	}

	plan, err := p.mgr.prepareAndDispatch(ctx, request)
	if err != nil {
		return nil, err
	}

	if sw, ok := stream.WriterFromContext(ctx); ok {
		_ = sw.Write(ctx, stream.Event{
			Name: "on_llm_request",
			Data: map[string]any{
				"provider": "copilot",
				"route":    plan.route,
				"model":    request.Model,
			},
		})
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("copilot.route", plan.route),
		attribute.String("copilot.model", request.Model),
	)
	if plan.session != nil && plan.session.id != "" {
		span.SetAttributes(attribute.String("copilot.session_id", plan.session.id))
	}

	if plan.compactOnly {
		return p.streamCompactOnly(ctx, plan, request), nil
	}

	return func(yield func(message.AssistantChunk, error) bool) {
		finished := false
		defer func() {
			if !finished {
				p.mgr.retireSession(plan.session)
			}
		}()

		plan.session.streamTurn(
			ctx,
			plan.turnQueue,
			request,
			plan.systemPrompt,
			plan.priorMessages,
			plan.ext,
			plan.workingDir,
			plan.compact,
			plan.sendFn,
			func(res turnResult) {
				finished = true
				p.mgr.completeTurn(plan.session, res)
			},
			yield,
		)
	}, nil
}

func (p *Provider) streamCompactOnly(ctx context.Context, plan *routePlan, req *llm.Request) llm.StreamResponse {
	return func(yield func(message.AssistantChunk, error) bool) {
		defer plan.session.endTurn(plan.turnQueue)

		cs, err := plan.session.runCompactTurn(ctx, plan.ext.CompactInstructions)
		if err != nil {
			p.mgr.retireSession(plan.session)
			yield(message.AssistantChunk{}, err)
			return
		}

		summary := cs.summary
		if summary == "" {
			summary = fmt.Sprintf("Compacted Copilot session (%d messages, %d tokens removed).", cs.messagesRemoved, cs.tokensRemoved)
		}

		agg := message.NewAssistantAggregator()
		txtChunk := message.AssistantChunk{
			BaseChunk: message.BaseChunk{ID: "copilot-compact-" + plan.session.id},
			Model:     req.Model,
			Content:   []message.Block{&message.TextBlock{Text: summary}},
		}
		agg.Add(&txtChunk)
		if !yield(txtChunk, nil) {
			p.mgr.retireSession(plan.session)
			return
		}

		prefixMsgs := slices.Clone(plan.priorMessages)
		if built, berr := agg.Build(); berr == nil && built != nil {
			prefixMsgs = append(prefixMsgs, built)
		}
		digest := computePrefixDigest(req, plan.systemPrompt, prefixMsgs, plan.ext, plan.session.allowedNativeTools, plan.workingDir)
		nonce := fmt.Sprintf("c%d", plan.session.reqSeq.Add(1))
		tok := computeTurnToken(plan.session.id, nonce, digest)

		meta := map[string]any{
			SessionIDKey:              plan.session.id,
			TurnTokenKey:              tok,
			PrefixDigestKey:           digest,
			CompactedKey:              true,
			CompactTriggerKey:         cs.trigger,
			CompactTokensRemovedKey:   cs.tokensRemoved,
			CompactMessagesRemovedKey: cs.messagesRemoved,
		}

		plan.session.mu.Lock()
		plan.session.expectedToken = tok
		plan.session.prefixDigest = digest
		plan.session.lastUsed = time.Now()
		plan.session.mu.Unlock()

		if !yield(message.AssistantChunk{
			BaseChunk:  message.BaseChunk{ID: "copilot-compact-" + plan.session.id},
			Model:      req.Model,
			Metadata:   meta,
			Done:       true,
			DoneReason: "compacted",
		}, nil) {
			p.mgr.retireSession(plan.session)
			return
		}

		p.mgr.completeTurn(plan.session, turnResult{
			sessionID:    plan.session.id,
			turnToken:    tok,
			prefixDigest: digest,
		})
	}
}

// GetQuotaSnapshot queries Copilot's `account.getQuota` RPC and maps quota
// pools (such as "chat", "completions", "premium_interactions") into [llm.QuotaSnapshot].
func (p *Provider) GetQuotaSnapshot(ctx context.Context) (llm.QuotaSnapshot, error) {
	if p.mgr == nil {
		return llm.QuotaSnapshot{}, fmt.Errorf("copilot: provider has nil session manager")
	}
	res, err := p.mgr.getQuotas(ctx)
	if err != nil {
		return llm.QuotaSnapshot{}, err
	}
	snap := llm.QuotaSnapshot{
		CollectedAt: time.Now(),
		Buckets:     make(map[string]llm.QuotaBucket, len(res.QuotaSnapshots)),
	}
	for k, raw := range res.QuotaSnapshots {
		snap.Buckets[k] = toQuotaBucket(k, raw)
	}
	return snap, nil
}

// GetQuota returns quota buckets for a bucket key (e.g., "premium_interactions",
// "chat") or falls back to Copilot's default account pool ("premium_interactions",
// "chat", "completions") when queried by model name.
func (p *Provider) GetQuota(ctx context.Context, model string) (llm.QuotaBuckets, bool, error) {
	snap, err := p.GetQuotaSnapshot(ctx)
	if err != nil {
		return nil, false, err
	}
	if buckets, ok := snap.ForModel(model); ok {
		return buckets, true, nil
	}
	if b, ok := snap.Buckets[model]; ok {
		return llm.QuotaBuckets{b}, true, nil
	}
	for _, fallback := range []string{"premium_interactions", "chat", "completions"} {
		if b, ok := snap.Buckets[fallback]; ok {
			return llm.QuotaBuckets{b}, true, nil
		}
	}
	return nil, false, nil
}
