package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/semconv/v1.41.0/genaiconv"
	"go.opentelemetry.io/otel/trace"
)

// TelemetryMiddleware returns a [Middleware] that instrumentations LLM requests
// using the loom/telemetry facade. It records standard GenAI spans and metrics.
func TelemetryMiddleware(provider Provider) Middleware {
	return func(next Streamer) Streamer {
		return func(ctx context.Context, req *Request) (StreamResponse, error) {
			opName := telemetry.OpChat
			providerName := getProviderName(provider.Name())

			spanName := fmt.Sprintf("%s %s", opName, req.Model)
			ctx, span := telemetry.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindClient))

			// Base attributes
			attrs := []attribute.KeyValue{
				telemetry.WithSystem(provider.Name()),
				telemetry.WithModel(req.Model),
			}
			span.SetAttributes(attrs...)

			// Record content if opt-in
			if telemetry.ShouldRecordContent(ctx) {
				if payload, err := json.Marshal(req.Messages); err == nil {
					telemetry.RecordContent(ctx, span, telemetry.KeyContentInputMessages, payload)
				}
			}

			startTime := time.Now()
			resp, err := next(ctx, req)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				span.End()
				return nil, err
			}

			return func(yield func(message.AssistantChunk, error) bool) {
				var firstChunkReceived bool
				var lastChunkTime time.Time
				var totalTokens message.TokenMetrics

				defer func() {
					duration := time.Since(startTime)
					// Final attributes
					finalAttrs := []attribute.KeyValue{
						telemetry.WithModel(req.Model), // Re-affirm model just in case it changed in response
					}

					// Record final metrics
					telemetry.RecordDuration(ctx, duration, opName, providerName, finalAttrs...)
					if totalTokens.TotalTokens > 0 {
						telemetry.RecordTokenUsage(ctx, int64(totalTokens.Tokens.Input), opName, providerName, telemetry.TokenTypeInput, finalAttrs...)
						telemetry.RecordTokenUsage(ctx, int64(totalTokens.Tokens.Output), opName, providerName, telemetry.TokenTypeOutput, finalAttrs...)
					}

					span.End()
				}()

				for chunk, err := range resp {
					now := time.Now()
					if err != nil {
						span.RecordError(err)
						span.SetStatus(codes.Error, err.Error())
						if !yield(chunk, err) {
							return
						}
						continue
					}

					if !firstChunkReceived {
						firstChunkReceived = true
						telemetry.RecordTimeToFirstChunk(ctx, now.Sub(startTime), opName, providerName, telemetry.WithModel(req.Model))
					} else {
						telemetry.RecordTimePerOutputChunk(ctx, now.Sub(lastChunkTime), opName, providerName, telemetry.WithModel(req.Model))
					}
					lastChunkTime = now

					if chunk.Metrics != nil {
						totalTokens.TotalTokens = chunk.Metrics.TotalTokens
						totalTokens.Tokens.Input = chunk.Metrics.Tokens.Input
						totalTokens.Tokens.Output = chunk.Metrics.Tokens.Output
						totalTokens.Tokens.CacheRead = chunk.Metrics.Tokens.CacheRead
						totalTokens.Tokens.CacheWrite = chunk.Metrics.Tokens.CacheWrite
						totalTokens.Tokens.Reasoning = chunk.Metrics.Tokens.Reasoning
					}

					if !yield(chunk, err) {
						return
					}
				}
			}, nil
		}
	}
}

func getProviderName(name string) genaiconv.ProviderNameAttr {
	switch name {
	case "openai":
		return telemetry.ProviderOpenAI
	case "anthropic":
		return telemetry.ProviderAnthropic
	case "google-genai", "google", "genai":
		return telemetry.ProviderGCPGemini
	default:
		return genaiconv.ProviderNameAttr(name)
	}
}
