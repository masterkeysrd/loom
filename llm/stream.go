package llm

import (
	"context"

	"github.com/masterkeysrd/loom/message"
)

// StreamWriter is the LLM-layer interface for forwarding token chunks to a
// consumer. The graph package bridges this interface with its own
// [graph.StreamWriter] via a shared stream adapter so that a single context
// value drives both event streams.
type StreamWriter interface {
	WriteChunk(ctx context.Context, chunk message.AssistantChunk) error
}

type streamWriterKey struct{}

// WithStreamWriter stores w in ctx so that [Model.Stream] can forward chunks
// without requiring callers to pass the writer explicitly.
func WithStreamWriter(ctx context.Context, w StreamWriter) context.Context {
	return context.WithValue(ctx, streamWriterKey{}, w)
}

// StreamWriterFromContext retrieves the [StreamWriter] previously stored by
// [WithStreamWriter]. The boolean is false when no writer is present.
func StreamWriterFromContext(ctx context.Context) (StreamWriter, bool) {
	w, ok := ctx.Value(streamWriterKey{}).(StreamWriter)
	return w, ok
}
