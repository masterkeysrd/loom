package llm

import (
	"context"

	"github.com/masterkeysrd/loom/profile"
)

// Runtime provides runtime context containing execution details.
type Runtime struct {
	RunID    string
	Model    string
	Provider string
	Profile  *profile.Profile
	Config   *ModelConfig
	provider Provider
}

// SupportsDeferredTools reports whether the underlying provider supports deferred tool loading for model.
func (r *Runtime) SupportsDeferredTools(model string) bool {
	if r == nil || r.provider == nil {
		return false
	}
	if dts, ok := r.provider.(DeferredToolsSupport); ok {
		return dts.SupportsDeferredTools(model)
	}
	return false
}

// SupportsInlineTools reports whether the underlying provider natively supports mid-conversation inline tool definitions for model.
func (r *Runtime) SupportsInlineTools(model string) bool {
	if r == nil || r.provider == nil {
		return false
	}
	if its, ok := r.provider.(InlineToolsSupport); ok {
		return its.SupportsInlineTools(model)
	}
	return false
}

type runtimeKey struct{}

// WithRuntime injects the llm runtime into the context.
func WithRuntime(ctx context.Context, rt *Runtime) context.Context {
	return context.WithValue(ctx, runtimeKey{}, rt)
}

// RuntimeFromContext extracts the llm runtime from the context.
func RuntimeFromContext(ctx context.Context) (*Runtime, bool) {
	rt, ok := ctx.Value(runtimeKey{}).(*Runtime)
	return rt, ok
}
