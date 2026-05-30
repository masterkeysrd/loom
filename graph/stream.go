package graph

import (
	"context"

	"github.com/masterkeysrd/loom/message"
)

// EventLLMChunk is the event name emitted for every token chunk produced by an
// LLM call within a node. Consumers can use this to stream output in real time.
const (
	EventLLMChunk    = "on_llm_chunk"
	EventCompleted   = "completed"
	EventInterrupted = "interrupted"
)

// StreamEvent is the payload type produced by [Graph.Stream].
// Every event carries the graph name, the node that produced it (if any),
// an event-type string (e.g. [EventLLMChunk], "completed", "interrupted",
// "error"), and arbitrary Data.
type StreamEvent struct {
	Graph string
	Node  string
	Event string
	Data  any
}

// StreamWriter is the graph-level interface for emitting arbitrary events
// during node execution. It is stored in the context via [WithStreamWriter]
// and read back with [StreamWriterFromContext].
type StreamWriter interface {
	WriteEvent(ctx context.Context, event string, data any) error
}

type streamWriterKey struct{}

// WithStreamWriter stores w in ctx so that nodes can emit [StreamEvent] values
// without receiving the writer as an explicit parameter.
func WithStreamWriter(ctx context.Context, w StreamWriter) context.Context {
	return context.WithValue(ctx, streamWriterKey{}, w)
}

// StreamWriterFromContext retrieves the [StreamWriter] previously stored by
// [WithStreamWriter]. The second return value is false if no writer is set.
func StreamWriterFromContext(ctx context.Context) (StreamWriter, bool) {
	w, ok := ctx.Value(streamWriterKey{}).(StreamWriter)
	return w, ok
}

// ExecutionCtx carries per-execution metadata (graph name and current node
// name) that is threaded through the context during a graph run. It lets
// node code and stream adapters tag their output with the correct identifiers
// without needing explicit parameters.
type ExecutionCtx struct {
	GraphName string
	NodeName  string
	Location  Location
}

type executionCtxKey struct{}

// WithExecutionCtx stores execCtx in ctx so it can be recovered inside nodes
// with [ExecutionCtxFromContext].
func WithExecutionCtx(ctx context.Context, execCtx ExecutionCtx) context.Context {
	return context.WithValue(ctx, executionCtxKey{}, execCtx)
}

// ExecutionCtxFromContext retrieves the [ExecutionCtx] previously stored by
// [WithExecutionCtx]. The second return value is false if no context is set.
func ExecutionCtxFromContext(ctx context.Context) (ExecutionCtx, bool) {
	val, ok := ctx.Value(executionCtxKey{}).(ExecutionCtx)
	return val, ok
}

// streamAdapter implements both [StreamWriter] and [llm.StreamWriter] so that
// a single iterator yield function can receive both generic graph events and
// LLM token chunks without any additional bridging logic.
type streamAdapter struct {
	eventYield func(StreamEvent, error) bool
}

func (s *streamAdapter) WriteEvent(ctx context.Context, event string, data any) error {
	execCtx, _ := ExecutionCtxFromContext(ctx)
	if !s.eventYield(StreamEvent{
		Graph: execCtx.GraphName,
		Node:  execCtx.NodeName,
		Event: event,
		Data:  data,
	}, nil) {
		return context.Canceled
	}
	return nil
}

func (s *streamAdapter) WriteChunk(ctx context.Context, chunk message.AssistantChunk) error {
	execCtx, _ := ExecutionCtxFromContext(ctx)
	if !s.eventYield(StreamEvent{
		Graph: execCtx.GraphName,
		Node:  execCtx.NodeName,
		Event: EventLLMChunk,
		Data:  chunk,
	}, nil) {
		return context.Canceled
	}
	return nil
}
