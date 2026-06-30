package context

import (
	"context"
)

// Keys for injection
type stateKey struct{}

// WithState injects the graph state into the context.
func WithState(ctx context.Context, state any) context.Context {
	return context.WithValue(ctx, stateKey{}, state)
}

// State extracts the graph state from the context.
func State(ctx context.Context) any {
	return ctx.Value(stateKey{})
}
