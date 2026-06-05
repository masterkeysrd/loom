package graph

import (
	"context"
	"time"

	"github.com/masterkeysrd/loom/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type recorder struct {
	graphName string
	span      telemetry.Span
	startTime time.Time
}

func newRecorder(graphName string) *recorder {
	return &recorder{
		graphName: graphName,
	}
}

func (r *recorder) startGraph(ctx context.Context, loc Location) (context.Context, *recorder) {
	r.startTime = time.Now()
	ctx, r.span = telemetry.Start(ctx, "loom.graph.execute "+r.graphName, trace.WithSpanKind(trace.SpanKindInternal))
	r.span.SetAttributes(
		telemetry.WithLoomGraph(r.graphName),
		telemetry.WithLoomThread(loc.ThreadID),
		telemetry.WithLoomCheckpoint(loc.CheckpointID),
		attribute.String("loom.namespace", loc.CheckpointNS),
	)
	return ctx, r
}

func (r *recorder) endGraph(ctx context.Context, err error) {
	if err != nil {
		r.recordError(err)
	}
	telemetry.RecordGraphDuration(ctx, time.Since(r.startTime), telemetry.WithLoomGraph(r.graphName))
	r.span.End()
}

func (r *recorder) startNode(ctx context.Context, nodeName string, loc Location) (context.Context, telemetry.Span) {
	telemetry.RecordNodeInvocation(ctx, telemetry.WithLoomGraph(r.graphName), telemetry.WithLoomNode(nodeName))

	ctx, span := telemetry.Start(ctx, "loom.node.execute "+nodeName, trace.WithSpanKind(trace.SpanKindInternal))
	span.SetAttributes(
		telemetry.WithLoomGraph(r.graphName),
		telemetry.WithLoomNode(nodeName),
		telemetry.WithLoomThread(loc.ThreadID),
		telemetry.WithLoomCheckpoint(loc.CheckpointID),
		attribute.String("loom.namespace", loc.CheckpointNS),
	)
	return ctx, span
}

func (r *recorder) endNode(ctx context.Context, nodeName string, span telemetry.Span, startTime time.Time, err error) {
	telemetry.RecordNodeDuration(ctx, time.Since(startTime), telemetry.WithLoomGraph(r.graphName), telemetry.WithLoomNode(nodeName))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

func (r *recorder) recordError(err error) {
	r.span.RecordError(err)
	r.span.SetStatus(codes.Error, err.Error())
}
