package tool

import (
	"context"
	"sync"

	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/stream"
)

// Runtime provides runtime context and utilities to executing tools.
type Runtime struct {
	CallID   string
	ToolName string
	Call     *message.ToolCall

	// Shared Resources magically bridged from the graph!
	State  any
	Stream stream.Writer

	mu           sync.RWMutex
	responseMeta map[string]any
}

// SetMeta stores a metadata key-value pair to be included in the tool's result.
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
	for k, v := range r.responseMeta {
		m[k] = v
	}
	return m
}

type runtimeKey struct{}

// WithRuntime injects the tool runtime into the context.
func WithRuntime(ctx context.Context, rt *Runtime) context.Context {
	return context.WithValue(ctx, runtimeKey{}, rt)
}

// RuntimeFromContext extracts the tool runtime from the context.
func RuntimeFromContext(ctx context.Context) (*Runtime, bool) {
	rt, ok := ctx.Value(runtimeKey{}).(*Runtime)
	return rt, ok
}
