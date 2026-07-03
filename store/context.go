package store

import "context"

type storeKey struct{}

// WithStore attaches a [Store] to the context so that nodes and tools
// can retrieve it via [FromContext].
func WithStore(ctx context.Context, s Store) context.Context {
	return context.WithValue(ctx, storeKey{}, s)
}

// FromContext retrieves the [Store] previously attached with [WithStore].
// Returns nil if no store was set in the context.
func FromContext(ctx context.Context) Store {
	s, _ := ctx.Value(storeKey{}).(Store)
	return s
}
