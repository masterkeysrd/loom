package decision

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

// TelemetryMiddleware returns a [Middleware] that instruments decision evaluations
// using the loom/telemetry facade. It records standard GenAI spans and metrics.
func TelemetryMiddleware(provider Provider) Middleware {
	return func(next Evaluator) Evaluator {
		return func(ctx context.Context, req *Request) (*Response, error) {
			opName := genaiconv.OperationNameAttr("decision")
			providerName := getProviderName(provider.Name())

			// Span name MUST be "decision {model}"
			spanName := fmt.Sprintf("decision %s", req.Model)
			ctx, span := telemetry.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindClient))

			// Base attributes
			attrs := []attribute.KeyValue{
				telemetry.WithOperation(opName),
				telemetry.WithProvider(string(providerName)),
				telemetry.WithModel(req.Model),
				attribute.Int("decision.question_count", len(req.Questions)),
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
				if payload, err := json.Marshal(req.Questions); err == nil {
					telemetry.RecordContent(ctx, span, attribute.Key("decision.questions"), payload)
				}
				if payload, err := json.Marshal(req.Input); err == nil {
					telemetry.RecordContent(ctx, span, attribute.Key("decision.input"), payload)
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

			// Record output content if opt-in
			if telemetry.ShouldRecordContent(ctx) {
				if payload, err := json.Marshal(resp.Answers); err == nil {
					telemetry.RecordContent(ctx, span, attribute.Key("decision.answers"), payload)
				}
			}

			// Record final metrics
			telemetry.RecordDuration(ctx, duration, opName, providerName, finalAttrs...)
			if resp.Metrics != nil && resp.Metrics.TotalTokens > 0 {
				telemetry.RecordTokenUsage(ctx, int64(resp.Metrics.Tokens.Input), opName, providerName, telemetry.TokenTypeInput, finalAttrs...)
				telemetry.RecordTokenUsage(ctx, int64(resp.Metrics.Tokens.Output), opName, providerName, telemetry.TokenTypeOutput, finalAttrs...)

				span.SetAttributes(
					telemetry.WithInputTokens(resp.Metrics.Tokens.Input),
					telemetry.WithOutputTokens(resp.Metrics.Tokens.Output),
				)

				if resp.Metrics.Tokens.Reasoning > 0 {
					span.SetAttributes(telemetry.WithReasoningTokens(resp.Metrics.Tokens.Reasoning))
				}
				if resp.Metrics.Tokens.CacheRead > 0 {
					span.SetAttributes(telemetry.WithCacheReadTokens(resp.Metrics.Tokens.CacheRead))
				}
				if resp.Metrics.Tokens.CacheWrite > 0 {
					span.SetAttributes(telemetry.WithCacheWriteTokens(resp.Metrics.Tokens.CacheWrite))
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
