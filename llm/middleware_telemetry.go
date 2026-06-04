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

			// Span name MUST be {gen_ai.operation.name} {gen_ai.request.model}
			spanName := fmt.Sprintf("%s %s", opName, req.Model)
			ctx, span := telemetry.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindClient))

			// Base attributes
			attrs := []attribute.KeyValue{
				telemetry.WithOperation(opName),
				telemetry.WithProvider(string(providerName)),
				telemetry.WithModel(req.Model),
				telemetry.WithStream(true), // Loom core always uses streaming internally
			}

			// Capture Tool Definitions if content recording is enabled
			if telemetry.ShouldRecordContent(ctx) {
				if defs, err := json.Marshal(req.Tools); err == nil {
					attrs = append(attrs, telemetry.KeyContentToolDefs.String(string(defs)))
				}
			}

			// Add optional request parameters
			if req.Temperature != nil {
				attrs = append(attrs, telemetry.WithTemperature(float64(*req.Temperature)))
			}
			if req.TopP != nil {
				attrs = append(attrs, telemetry.WithTopP(float64(*req.TopP)))
			}
			if req.TopK != nil {
				attrs = append(attrs, telemetry.WithTopK(*req.TopK))
			}
			if req.MaxTokens > 0 {
				attrs = append(attrs, telemetry.WithMaxTokens(req.MaxTokens))
			}
			if len(req.Stop) > 0 {
				attrs = append(attrs, telemetry.WithStopSequences(req.Stop))
			}
			if req.PresencePenalty != nil {
				attrs = append(attrs, telemetry.WithPresencePenalty(float64(*req.PresencePenalty)))
			}
			if req.FrequencyPenalty != nil {
				attrs = append(attrs, telemetry.WithFrequencyPenalty(float64(*req.FrequencyPenalty)))
			}
			if req.ResponseFormat != "" {
				attrs = append(attrs, telemetry.KeyGenAIResponseType.String(req.ResponseFormat))
			}

			// Map conversation ID from loom thread ID if available
			if threadID, ok := ctx.Value(telemetry.KeyLoomThreadID).(string); ok {
				attrs = append(attrs, telemetry.WithConversationID(threadID))
			} else if threadID, ok := span.SpanContext().TraceID().String(), true; ok {
				// Fallback to trace ID if no thread ID is explicitly set (common for isolated calls)
				attrs = append(attrs, telemetry.WithConversationID(threadID))
			}

			span.SetAttributes(attrs...)

			// Record content if opt-in
			if telemetry.ShouldRecordContent(ctx) {
				var chatHistory []message.Message
				var systemInstructions []message.Message

				for _, m := range req.Messages {
					if _, ok := m.(*message.System); ok {
						systemInstructions = append(systemInstructions, m)
					} else {
						chatHistory = append(chatHistory, m)
					}
				}

				if payload, err := json.Marshal(chatHistory); err == nil {
					telemetry.RecordContent(ctx, span, telemetry.KeyContentInputMessages, payload)
				}
				if len(systemInstructions) > 0 {
					if payload, err := json.Marshal(systemInstructions); err == nil {
						telemetry.RecordContent(ctx, span, telemetry.KeyContentSystemPrompt, payload)
					}
				}
			}

			startTime := time.Now()
			resp, err := next(ctx, req)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				span.SetAttributes(telemetry.WithErrorType(fmt.Sprintf("%T", err)))
				span.End()
				return nil, err
			}

			return func(yield func(message.AssistantChunk, error) bool) {
				var firstChunkReceived bool
				var lastChunkTime time.Time
				var totalTokens message.TokenMetrics
				var responseModel string
				var responseID string
				var finishReasons []string
				agg := message.NewAssistantAggregator()

				defer func() {
					duration := time.Since(startTime)
					// Final attributes
					finalAttrs := []attribute.KeyValue{
						telemetry.WithModel(req.Model), // Re-affirm model just in case it changed in response
					}
					if responseModel != "" {
						finalAttrs = append(finalAttrs, telemetry.WithResponseModel(responseModel))
						span.SetAttributes(telemetry.WithResponseModel(responseModel))
					}
					if responseID != "" {
						span.SetAttributes(telemetry.WithResponseID(responseID))
					}
					if len(finishReasons) > 0 {
						span.SetAttributes(telemetry.WithFinishReasons(finishReasons))
					}

					// Record assistant messages if content recording is enabled
					if telemetry.ShouldRecordContent(ctx) {
						if fullMsg, err := agg.Build(); err == nil {
							if payload, err := json.Marshal([]message.Message{fullMsg}); err == nil {
								telemetry.RecordContent(ctx, span, telemetry.KeyContentOutputMessages, payload)
							}
						}
					}

					// Record final metrics
					telemetry.RecordDuration(ctx, duration, opName, providerName, finalAttrs...)
					if totalTokens.TotalTokens > 0 {
						telemetry.RecordTokenUsage(ctx, int64(totalTokens.Tokens.Input), opName, providerName, telemetry.TokenTypeInput, finalAttrs...)
						telemetry.RecordTokenUsage(ctx, int64(totalTokens.Tokens.Output), opName, providerName, telemetry.TokenTypeOutput, finalAttrs...)

						// Add usage to span attributes for better visibility in trace explorer
						span.SetAttributes(
							telemetry.WithInputTokens(totalTokens.Tokens.Input),
							telemetry.WithOutputTokens(totalTokens.Tokens.Output),
						)

						if totalTokens.Tokens.Reasoning > 0 {
							span.SetAttributes(telemetry.WithReasoningTokens(totalTokens.Tokens.Reasoning))
						}
						if totalTokens.Tokens.CacheRead > 0 {
							span.SetAttributes(telemetry.WithCacheReadTokens(totalTokens.Tokens.CacheRead))
						}
						if totalTokens.Tokens.CacheWrite > 0 {
							span.SetAttributes(telemetry.WithCacheWriteTokens(totalTokens.Tokens.CacheWrite))
						}
					}

					span.End()
				}()

				for chunk, err := range resp {
					now := time.Now()
					if err != nil {
						span.RecordError(err)
						span.SetStatus(codes.Error, err.Error())
						span.SetAttributes(telemetry.WithErrorType(fmt.Sprintf("%T", err)))
						if !yield(chunk, err) {
							return
						}
						continue
					}

					if !firstChunkReceived {
						firstChunkReceived = true
						elapsed := now.Sub(startTime)
						telemetry.RecordTimeToFirstChunk(ctx, elapsed, opName, providerName, telemetry.WithModel(req.Model))
						span.SetAttributes(telemetry.KeyGenAIResponseTimeToFirst.Float64(elapsed.Seconds()))
					} else {
						telemetry.RecordTimePerOutputChunk(ctx, now.Sub(lastChunkTime), opName, providerName, telemetry.WithModel(req.Model))
					}
					lastChunkTime = now

					if chunk.ID != "" {
						responseID = chunk.ID
					}
					if chunk.Model != "" {
						responseModel = chunk.Model
					}
					if chunk.DoneReason != "" {
						finishReasons = append(finishReasons, chunk.DoneReason)
					}

					agg.Add(&chunk)

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
