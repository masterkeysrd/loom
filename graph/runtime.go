package graph

import (
	"context"
	"maps"
	"sync"

	"github.com/masterkeysrd/loom/store"
	"github.com/masterkeysrd/loom/stream"
)

// Runtime provides runtime context and utilities to executing nodes.
type Runtime struct {
	RunID    string
	NodeName string
	Step     int
	Location Location

	// Shared Resources
	State  any           // The untyped graph state
	Store  store.Store   // For persisting data across nodes
	Stream stream.Writer // For emitting UI progress events

	// responseMeta holds metadata to be appended to the node's result
	mu           sync.RWMutex
	responseMeta map[string]any
}

// SetMeta stores a metadata key-value pair to be included in the node's result.
func (r *Runtime) SetMeta(key string, value any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.responseMeta == nil {
		r.responseMeta = make(map[string]any)
	}
	r.responseMeta[key] = value
}

// GetMeta retrieves a copy of the current response metadata.
func (r *Runtime) GetMeta() map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()

	m := make(map[string]any, len(r.responseMeta))
	maps.Copy(m, r.responseMeta)
	return m
}

type runtimeKey struct{}

// WithRuntime injects the node runtime into the context.
func WithRuntime(ctx context.Context, rt *Runtime) context.Context {
	return context.WithValue(ctx, runtimeKey{}, rt)
}

// RuntimeFromContext extracts the node runtime from the context.
func RuntimeFromContext(ctx context.Context) (*Runtime, bool) {
	rt, ok := ctx.Value(runtimeKey{}).(*Runtime)
	return rt, ok
}
