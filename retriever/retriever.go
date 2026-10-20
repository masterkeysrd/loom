package retriever

import (
	"context"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/masterkeysrd/loom/document"
	"github.com/masterkeysrd/loom/stream"
	"github.com/masterkeysrd/loom/telemetry"
)

// Retriever retrieves documents relevant to a given query string.
type Retriever interface {
	Retrieve(ctx context.Context, query string, opts ...Option) ([]document.Document, error)
}

// RetrieverFunc adapts a plain function to the [Retriever] interface.
type RetrieverFunc func(ctx context.Context, query string, opts ...Option) ([]document.Document, error)

// Retrieve executes the underlying function.
func (f RetrieverFunc) Retrieve(ctx context.Context, query string, opts ...Option) ([]document.Document, error) {
	return f(ctx, query, opts...)
}

// Middleware wraps a [Retriever] to intercept and enhance retrieval behavior.
type Middleware func(Retriever) Retriever

// Chain combines a [Retriever] with multiple middlewares.
// Middlewares are executed in the order provided (the first middleware is outermost).
func Chain(r Retriever, mws ...Middleware) Retriever {
	for i := len(mws) - 1; i >= 0; i-- {
		if mws[i] != nil {
			r = mws[i](r)
		}
	}
	return r
}

// Config specifies options for a retrieval call.
type Config struct {
	TopK        int
	Namespace   string
	MinScore    float32
	HasMinScore bool
	Filter      map[string]any
}

// Option configures retrieval behavior.
type Option func(*Config)

// WithTopK sets the maximum number of documents to retrieve. Defaults to 4.
func WithTopK(k int) Option {
	return func(c *Config) {
		if k > 0 {
			c.TopK = k
		}
	}
}

// WithNamespace sets the namespace to retrieve documents from.
func WithNamespace(ns string) Option {
	return func(c *Config) {
		c.Namespace = ns
	}
}

// WithMinScore sets the minimum similarity or relevance score threshold.
func WithMinScore(score float32) Option {
	return func(c *Config) {
		c.MinScore = score
		c.HasMinScore = true
	}
}

// WithFilter sets metadata filters for retrieval.
func WithFilter(filter map[string]any) Option {
	return func(c *Config) {
		c.Filter = filter
	}
}

// ApplyOptions resolves a slice of [Option] into a [Config] with default TopK=4.
func ApplyOptions(opts ...Option) Config {
	cfg := Config{
		TopK: 4,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

// TelemetryMiddleware wraps a retriever with OpenTelemetry tracing and streaming events.
func TelemetryMiddleware(name string) Middleware {
	return func(next Retriever) Retriever {
		return RetrieverFunc(func(ctx context.Context, query string, opts ...Option) ([]document.Document, error) {
			cfg := ApplyOptions(opts...)

			ctx, span := telemetry.Start(ctx, "retrieve "+name, trace.WithSpanKind(trace.SpanKindClient))
			defer span.End()

			span.SetAttributes(
				telemetry.WithOperation(telemetry.OpRetrieval),
				telemetry.KeyLoomRetrieverName.String(name),
				telemetry.KeyLoomRetrieverTopK.Int(cfg.TopK),
			)

			docs, err := next.Retrieve(ctx, query, opts...)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return nil, err
			}

			span.SetAttributes(telemetry.KeyLoomRetrieverResultCount.Int(len(docs)))

			if sw, ok := stream.WriterFromContext(ctx); ok {
				writeCtx := stream.WithMetadata(ctx, stream.Metadata{Source: "retriever:" + name})
				_ = sw.Write(writeCtx, stream.Event{
					Name: "on_retrieve",
					Data: docs,
				})
			}

			return docs, nil
		})
	}
}
