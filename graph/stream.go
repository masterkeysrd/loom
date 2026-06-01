package graph

import (
	"context"

	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/stream"
)

// EventLLMChunk is the event name emitted for every token chunk produced by an
// LLM call within a node. Consumers can use this to stream output in real time.
const (
	EventLLMChunk     = "on_llm_chunk"
	EventToolChunk    = "on_tool_chunk"
	EventToolProgress = "on_tool_progress"
	EventCompleted    = "completed"
	EventInterrupted  = "interrupted"
)

// StreamEvent is the payload type produced by [Graph.Stream].
// Every event carries the graph name, the node that produced it (if any),
// a source identifier (e.g. "tool:search"), an event-type string, and arbitrary Data.
type StreamEvent struct {
	Graph  string
	Node   string
	Source string
	Event  string
	Data   any
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

// streamAdapter implements [stream.Writer] so that a single iterator yield
// function can receive generic graph events, LLM token chunks, and tool
// streaming updates without additional bridging logic.
type streamAdapter struct {
	eventYield func(StreamEvent, error) bool
}

func (s *streamAdapter) Write(ctx context.Context, data any) error {
	execCtx, _ := ExecutionCtxFromContext(ctx)
	metadata, _ := stream.MetadataFromContext(ctx)

	event := "event"
	switch v := data.(type) {
	case message.AssistantChunk:
		event = EventLLMChunk
	case message.ToolChunk:
		event = EventToolChunk
		if v.Progress != "" || v.ProgressCurrent != nil || v.ProgressTotal != nil {
			event = EventToolProgress
		}
	case stream.Event:
		event = v.Name
		data = v.Data
	}

	if !s.eventYield(StreamEvent{
		Graph:  execCtx.GraphName,
		Node:   execCtx.NodeName,
		Source: metadata.Source,
		Event:  event,
		Data:   data,
	}, nil) {
		return context.Canceled
	}
	return nil
}

// WriteEvent is a helper for internal graph events.
func (s *streamAdapter) WriteEvent(ctx context.Context, event string, data any) error {
	return s.Write(ctx, stream.Event{Name: event, Data: data})
}
