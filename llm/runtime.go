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
