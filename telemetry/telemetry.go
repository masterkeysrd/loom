package telemetry

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/semconv/v1.41.0/genaiconv"
	"go.opentelemetry.io/otel/semconv/v1.41.0/mcpconv"
	"go.opentelemetry.io/otel/semconv/v1.41.0/rpcconv"
	"go.opentelemetry.io/otel/trace"
)

// Global instruments
var (
	tracer trace.Tracer
	meter  metric.Meter

	// GenAI standard instruments via genaiconv
	genaiTokenUsage   genaiconv.ClientTokenUsage
	genaiOpDuration   genaiconv.ClientOperationDuration
	genaiTimeToFirst  genaiconv.ClientOperationTimeToFirstChunk
	genaiTimePerChunk genaiconv.ClientOperationTimePerOutputChunk

	// MCP standard instruments via mcpconv
	mcpOpDuration mcpconv.ClientOperationDuration

	// RPC standard instruments via rpcconv
	rpcOpDuration rpcconv.ClientCallDuration

	// Loom-specific instruments
	graphExecutionDuration  metric.Float64Histogram
	graphNodeDuration       metric.Float64Histogram
	graphNodeInvocations    metric.Int64Counter
	toolExecutionDuration   metric.Float64Histogram
	memorySummarizeDuration metric.Float64Histogram
)

func init() {
	tracer = otel.GetTracerProvider().Tracer("loom")
	meter = otel.GetMeterProvider().Meter("loom")
	_ = initInstruments()
}

type Config struct {
	ServiceName    string
	ServiceVersion string
	OTLPEndpoint   string // default localhost:4317
	Insecure       bool
	MetricInterval time.Duration // default 5 seconds
}

// Init sets up the OpenTelemetry SDK with OTLP exporters.
// Returns a shutdown function and an error.
// If the application already has OpenTelemetry configured globally, calling this is unnecessary.
func Init(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	// Opt-in to the latest GenAI semantic conventions as per v1.41.1 spec
	if os.Getenv("OTEL_SEMCONV_STABILITY_OPT_IN") == "" {
		os.Setenv("OTEL_SEMCONV_STABILITY_OPT_IN", "gen_ai_latest_experimental")
	}

	if cfg.OTLPEndpoint == "" {
		cfg.OTLPEndpoint = "localhost:4317"
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "loom"
	}
	if cfg.MetricInterval == 0 {
		cfg.MetricInterval = 5 * time.Second
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
			semconv.ServiceVersionKey.String(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Trace Exporter
	traceOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
	}
	if cfg.Insecure {
		traceOpts = append(traceOpts, otlptracegrpc.WithInsecure())
	}
	traceExporter, err := otlptracegrpc.New(ctx, traceOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	tracer = tp.Tracer("loom")

	// Metric Exporter
	metricOpts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint),
	}
	if cfg.Insecure {
		metricOpts = append(metricOpts, otlpmetricgrpc.WithInsecure())
	}
	metricExporter, err := otlpmetricgrpc.New(ctx, metricOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric exporter: %w", err)
	}

	// Explicit Bucket Boundaries as per GenAI Semantic Conventions v1.41.1
	tokenBuckets := sdkmetric.AggregationExplicitBucketHistogram{
		Boundaries: []float64{
			1, 4, 16, 64, 256, 1024, 4096, 16384, 65536,
			262144, 1048576, 4194304, 16777216, 67108864,
		},
	}
	durationBuckets := sdkmetric.AggregationExplicitBucketHistogram{
		Boundaries: []float64{
			0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24,
			20.48, 40.96, 81.92,
		},
	}
	rpcDurationBuckets := sdkmetric.AggregationExplicitBucketHistogram{
		Boundaries: []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10},
	}

	reader := sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(cfg.MetricInterval))
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
		// GenAI Views
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: genaiconv.ClientTokenUsage{}.Name()},
			sdkmetric.Stream{Aggregation: tokenBuckets},
		)),
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: genaiconv.ClientOperationDuration{}.Name()},
			sdkmetric.Stream{Aggregation: durationBuckets},
		)),
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: genaiconv.ClientOperationTimeToFirstChunk{}.Name()},
			sdkmetric.Stream{Aggregation: durationBuckets},
		)),
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: genaiconv.ClientOperationTimePerOutputChunk{}.Name()},
			sdkmetric.Stream{Aggregation: durationBuckets},
		)),
		// MCP/RPC Views
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: mcpconv.ClientOperationDuration{}.Name()},
			sdkmetric.Stream{Aggregation: rpcDurationBuckets},
		)),
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: rpcconv.ClientCallDuration{}.Name()},
			sdkmetric.Stream{Aggregation: rpcDurationBuckets},
		)),
	)
	otel.SetMeterProvider(mp)
	meter = mp.Meter("loom")

	// Initialize instruments
	if err := initInstruments(); err != nil {
		return nil, err
	}

	return func(ctx context.Context) error {
		errTP := tp.Shutdown(ctx)
		errMP := mp.Shutdown(ctx)
		if errTP != nil {
			return errTP
		}
		return errMP
	}, nil
}

func initInstruments() error {
	var err error

	genaiTokenUsage, err = genaiconv.NewClientTokenUsage(meter)
	if err != nil {
		return err
	}

	genaiOpDuration, err = genaiconv.NewClientOperationDuration(meter)
	if err != nil {
		return err
	}

	genaiTimeToFirst, err = genaiconv.NewClientOperationTimeToFirstChunk(meter)
	if err != nil {
		return err
	}

	genaiTimePerChunk, err = genaiconv.NewClientOperationTimePerOutputChunk(meter)
	if err != nil {
		return err
	}

	mcpOpDuration, err = mcpconv.NewClientOperationDuration(meter)
	if err != nil {
		return err
	}

	rpcOpDuration, err = rpcconv.NewClientCallDuration(meter)
	if err != nil {
		return err
	}

	graphExecutionDuration, err = meter.Float64Histogram("loom.graph.execution.duration",
		metric.WithDescription("Time taken to complete an entire graph run"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return err
	}

	graphNodeDuration, err = meter.Float64Histogram("loom.graph.node.duration",
		metric.WithDescription("Time taken for a specific node execution"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return err
	}

	graphNodeInvocations, err = meter.Int64Counter("loom.graph.node.invocations",
		metric.WithDescription("Number of times a node is invoked"),
	)
	if err != nil {
		return err
	}

	toolExecutionDuration, err = meter.Float64Histogram("loom.tool.execution.duration",
		metric.WithDescription("Time taken to execute a local Loom tool"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return err
	}

	memorySummarizeDuration, err = meter.Float64Histogram("loom.memory.summarization.duration",
		metric.WithDescription("Time taken for the memory summarization phase"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return err
	}

	return nil
}

// Tracing API

type Span struct {
	trace.Span
}

func (s Span) SetAttributes(attrs ...attribute.KeyValue) {
	s.Span.SetAttributes(attrs...)
}

func Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, Span) {
	ctx, s := tracer.Start(ctx, spanName, opts...)
	return ctx, Span{s}
}

// Metrics API

func RecordTokenUsage(ctx context.Context, tokens int64, op genaiconv.OperationNameAttr, provider genaiconv.ProviderNameAttr, tokenType genaiconv.TokenTypeAttr, attrs ...attribute.KeyValue) {
	genaiTokenUsage.Record(ctx, tokens, op, provider, tokenType, attrs...)
}

func RecordDuration(ctx context.Context, duration time.Duration, op genaiconv.OperationNameAttr, provider genaiconv.ProviderNameAttr, attrs ...attribute.KeyValue) {
	genaiOpDuration.Record(ctx, duration.Seconds(), op, provider, attrs...)
}

func RecordMCPDuration(ctx context.Context, duration time.Duration, method mcpconv.MethodNameAttr, attrs ...attribute.KeyValue) {
	mcpOpDuration.Record(ctx, duration.Seconds(), method, attrs...)
}

func RecordRPCDuration(ctx context.Context, duration time.Duration, system rpcconv.SystemNameAttr, method string, attrs ...attribute.KeyValue) {
	attrs = append(attrs, rpcOpDuration.AttrMethod(method))
	rpcOpDuration.Record(ctx, duration.Seconds(), system, attrs...)
}

func RecordGraphDuration(ctx context.Context, duration time.Duration, attrs ...attribute.KeyValue) {
	graphExecutionDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attrs...))
}

func RecordNodeDuration(ctx context.Context, duration time.Duration, attrs ...attribute.KeyValue) {
	graphNodeDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attrs...))
}

func RecordNodeInvocation(ctx context.Context, attrs ...attribute.KeyValue) {
	graphNodeInvocations.Add(ctx, 1, metric.WithAttributes(attrs...))
}

func RecordToolDuration(ctx context.Context, duration time.Duration, attrs ...attribute.KeyValue) {
	toolExecutionDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attrs...))
}

func RecordMemorySummarizeDuration(ctx context.Context, duration time.Duration, attrs ...attribute.KeyValue) {
	memorySummarizeDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attrs...))
}

func RecordTimeToFirstChunk(ctx context.Context, duration time.Duration, op genaiconv.OperationNameAttr, provider genaiconv.ProviderNameAttr, attrs ...attribute.KeyValue) {
	genaiTimeToFirst.Record(ctx, duration.Seconds(), op, provider, attrs...)
}

func RecordTimePerOutputChunk(ctx context.Context, duration time.Duration, op genaiconv.OperationNameAttr, provider genaiconv.ProviderNameAttr, attrs ...attribute.KeyValue) {
	genaiTimePerChunk.Record(ctx, duration.Seconds(), op, provider, attrs...)
}

// Content Recording

type contentKey struct{}

func WithContentRecording(ctx context.Context) context.Context {
	return context.WithValue(ctx, contentKey{}, true)
}

func ShouldRecordContent(ctx context.Context) bool {
	val := ctx.Value(contentKey{})
	if val == nil {
		return false
	}
	return val.(bool)
}

type ContentUploadHook func(ctx context.Context, content []byte) (string, error)

var uploadHook ContentUploadHook

func SetContentUploadHook(hook ContentUploadHook) {
	uploadHook = hook
}

func RecordContent(ctx context.Context, span Span, key attribute.Key, content []byte) {
	if !ShouldRecordContent(ctx) {
		return
	}

	if uploadHook != nil {
		ref, err := uploadHook(ctx, content)
		if err == nil {
			span.SetAttributes(attribute.String(string(key)+".ref", ref))
			return
		}
	}

	span.SetAttributes(key.String(string(content)))
}
