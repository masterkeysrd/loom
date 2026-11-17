package loomclaudecode

import (
	"context"
	"fmt"
	"sync"

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
	_ llm.IdleMessageNotifier = (*Provider)(nil)
)

// Provider implements [llm.Provider] by wrapping the official Claude Code CLI
// (`claude`) via an in-process mini-proxy.
type Provider struct {
	cfg       Config
	proxy     *sessionManager
	overrides sync.Map
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
		cfg:   norm,
		proxy: mgr,
	}, nil
}

// Name returns "claudecode", uniquely identifying this provider in an [llm.Registry].
func (p *Provider) Name() string {
	return "claudecode"
}

// OnIdleMessage registers fn to receive unsolicited [message.Message]
// notifications emitted by idle CLI sessions (such as background compaction or
// unexpected process exits) and returns an unsubscribe function.
func (p *Provider) OnIdleMessage(fn llm.IdleMessageHandler) func() {
	if p.proxy == nil {
		return func() {}
	}
	return p.proxy.addIdleListener(fn)
}

// Close terminates all warm/parked Claude Code CLI subprocesses and cleans up
// any temporary isolated CLAUDE_CONFIG_DIR owned by this provider.
func (p *Provider) Close() error {
	if p.proxy == nil {
		return nil
	}
	return p.proxy.close()
}

// Stream routes request through the in-process mini-proxy (Path A live/parked
// match, Path B snapshot fork, or Path C fresh CLI session) and yields
// [message.AssistantChunk] values as the CLI streams NDJSON output.
func (p *Provider) Stream(ctx context.Context, request *llm.Request) (llm.StreamResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("claudecode: request cannot be nil")
	}

	plan, err := p.proxy.prepareAndDispatch(ctx, request)
	if err != nil {
		return nil, err
	}

	if sw, ok := stream.WriterFromContext(ctx); ok {
		_ = sw.Write(ctx, stream.Event{
			Name: "on_llm_request",
			Data: map[string]any{
				"provider": "claudecode",
				"route":    plan.route,
				"model":    request.Model,
			},
		})
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("claudecode.route", plan.route),
		attribute.String("claudecode.model", request.Model),
	)
	if plan.session != nil && plan.session.id != "" {
		span.SetAttributes(attribute.String("claudecode.session_id", plan.session.id))
	}

	return func(yield func(message.AssistantChunk, error) bool) {
		finished := false
		defer func() {
			if !finished {
				p.proxy.retireSession(plan.session)
			}
		}()

		plan.session.streamTurn(
			ctx,
			plan.turnCh,
			request,
			plan.systemPrompt,
			plan.priorMessages,
			plan.ext,
			plan.compact,
			func(res turnResult) {
				finished = true
				p.proxy.completeTurn(plan.session, res)
			},
			yield,
		)
	}, nil
}

// ListModels returns the known Claude Code model profiles (with runtime
// overrides applied), satisfying [llm.ModelLister].
func (p *Provider) ListModels(ctx context.Context) ([]profile.Profile, error) {
	return p.ListProfiles(), nil
}

// GetModel returns a single Claude Code model profile by ID, satisfying
// [llm.ModelLister].
func (p *Provider) GetModel(ctx context.Context, id string) (profile.Profile, error) {
	if prof, ok := p.GetProfile(id); ok {
		return prof, nil
	}
	return profile.Profile{}, fmt.Errorf("claudecode: model %q not found", id)
}
