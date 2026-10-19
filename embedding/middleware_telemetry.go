package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/masterkeysrd/loom/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/semconv/v1.41.0/genaiconv"
	"go.opentelemetry.io/otel/trace"
)

// TelemetryMiddleware returns a [Middleware] that instruments embedding generation
// using the loom/telemetry facade. It records standard GenAI spans and metrics.
func TelemetryMiddleware(provider Provider) Middleware {
	return func(next Embedder) Embedder {
		return func(ctx context.Context, req *Request) (*Response, error) {
			opName := telemetry.OpEmbeddings
			providerName := getProviderName(provider.Name())

			// Span name MUST be "embed {model}"
			spanName := fmt.Sprintf("embed %s", req.Model)
			ctx, span := telemetry.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindClient))

			// Base attributes
			attrs := []attribute.KeyValue{
				telemetry.WithOperation(opName),
				telemetry.WithProvider(string(providerName)),
				telemetry.WithModel(req.Model),
				attribute.Int("embedding.input_count", len(req.Inputs)),
			}
			if req.Dimensions != nil && *req.Dimensions > 0 {
				attrs = append(attrs, attribute.Int("gen_ai.embeddings.dimension.count", *req.Dimensions))
			}
			if req.TaskType != TaskUnspecified {
				attrs = append(attrs, attribute.String("embedding.task_type", string(req.TaskType)))
			}

			// Map conversation ID from loom thread ID if available
			if threadID, ok := ctx.Value(telemetry.KeyLoomThreadID).(string); ok {
				attrs = append(attrs, telemetry.WithConversationID(threadID))
			} else if threadID := span.SpanContext().TraceID().String(); threadID != "" {
				attrs = append(attrs, telemetry.WithConversationID(threadID))
			}

			span.SetAttributes(attrs...)

			// Record input content if opt-in
			if telemetry.ShouldRecordContent(ctx) {
				if payload, err := json.Marshal(req.Inputs); err == nil {
					telemetry.RecordContent(ctx, span, attribute.Key("embedding.inputs"), payload)
				}
			}

			startTime := time.Now()
			resp, err := next(ctx, req)
			duration := time.Since(startTime)

			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				span.SetAttributes(telemetry.WithErrorType(fmt.Sprintf("%T", err)))
				span.End()
				return nil, err
			}

			// Final attributes
			finalAttrs := []attribute.KeyValue{
				telemetry.WithModel(req.Model),
			}
			if resp.Model != "" {
				finalAttrs = append(finalAttrs, telemetry.WithResponseModel(resp.Model))
				span.SetAttributes(telemetry.WithResponseModel(resp.Model))
			}

			// Record final metrics
			telemetry.RecordDuration(ctx, duration, opName, providerName, finalAttrs...)
			if resp.Metrics != nil && (resp.Metrics.TotalTokens > 0 || resp.Metrics.Tokens.Input > 0) {
				telemetry.RecordTokenUsage(ctx, int64(resp.Metrics.Tokens.Input), opName, providerName, telemetry.TokenTypeInput, finalAttrs...)
				span.SetAttributes(telemetry.WithInputTokens(resp.Metrics.Tokens.Input))

				if resp.Metrics.Tokens.Output > 0 {
					telemetry.RecordTokenUsage(ctx, int64(resp.Metrics.Tokens.Output), opName, providerName, telemetry.TokenTypeOutput, finalAttrs...)
					span.SetAttributes(telemetry.WithOutputTokens(resp.Metrics.Tokens.Output))
				}
			}

			span.End()
			return resp, nil
		}
	}
}

func getProviderName(name string) genaiconv.ProviderNameAttr {
	switch name {
	case "openai":
		return telemetry.ProviderOpenAI
	case "anthropic":
		return telemetry.ProviderAnthropic
	case "google-genai", "google", "genai", "gemini":
		return telemetry.ProviderGCPGemini
	default:
		return genaiconv.ProviderNameAttr(name)
	}
}
