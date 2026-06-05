package stream

import (
	"context"
)

// Writer is the unified interface for emitting any data object into an
// execution stream. Implementations typically use type switches to route
// data to the correct event types.
type Writer interface {
	Write(ctx context.Context, data any) error
}

type (
	writerKey   struct{}
	metadataKey struct{}
)

var globalWriter Writer

// SetGlobalWriter sets a fallback writer that will be used when no writer is found in the context.
func SetGlobalWriter(w Writer) {
	globalWriter = w
}

// Metadata carries information about the source of a stream write.
type Metadata struct {
	// Source identifies the component emitting the data (e.g. "tool:calculator", "llm:gpt-4").
	Source string
}

// Event is a named data object that can be emitted into a stream.
type Event struct {
	Name string
	Data any
}

// WithWriter stores w in ctx.
func WithWriter(ctx context.Context, w Writer) context.Context {
	return context.WithValue(ctx, writerKey{}, w)
}

// WriterFromContext retrieves the [Writer] from ctx.
func WriterFromContext(ctx context.Context) (Writer, bool) {
	w, ok := ctx.Value(writerKey{}).(Writer)
	if !ok && globalWriter != nil {
		return globalWriter, true
	}
	return w, ok
}

// WithMetadata stores md in ctx.
func WithMetadata(ctx context.Context, md Metadata) context.Context {
	return context.WithValue(ctx, metadataKey{}, md)
}

// MetadataFromContext retrieves the [Metadata] from ctx.
func MetadataFromContext(ctx context.Context) (Metadata, bool) {
	md, ok := ctx.Value(metadataKey{}).(Metadata)
	return md, ok
}
