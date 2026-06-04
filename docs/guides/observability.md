# Observability

Loom provides first-class support for observability using OpenTelemetry (OTel). This allows you to trace the execution of your agents, monitor performance, and debug complex multi-step workflows.

## Quick Start

The easiest way to get started is to use the built-in `telemetry` package and Loom Studio.

```go
import "github.com/masterkeysrd/loom/telemetry"

// Initialize telemetry (usually in main)
shutdown, _ := telemetry.Init(ctx, telemetry.Config{ServiceName: "my-agent"})
defer shutdown(ctx)

// Start a span
ctx, span := telemetry.Start(ctx, "my-operation")
defer span.End()

// Log a custom attribute
span.SetAttributes(telemetry.WithLoomThread("thread-123"))
```

## Loom Studio

Loom Studio is a built-in visualization and debugging tool for your agents. It provides a real-time dashboard for metrics and a detailed waterfall viewer for execution traces.

### Running Loom Studio

To start Loom Studio, run the following command in your terminal:

```bash
loom studio
```

By default, it will:
- Open a web dashboard on `http://localhost:8080`.
- Listen for OTLP gRPC telemetry on `localhost:4317`.
- Listen for OTLP HTTP telemetry on `localhost:4318`.
- Store data in a local SQLite database at `.loom/telemetry.db`.

### Automatic Tracing

Loom's `Model` and `Graph` packages automatically emit OpenTelemetry spans and metrics for:
- LLM requests (using GenAI semantic conventions).
- Node entry and exit.
- Graph execution durations and node invocations.

### Capturing Sensitive Content

By default, Loom does **not** record sensitive content (prompts, completion text, tool results) to protect your privacy. You can opt-in to content recording using `telemetry.WithContentRecording`:

```go
ctx = telemetry.WithContentRecording(ctx)
// Subsequent LLM and tool calls in this context will record their payloads
```

## Production Observability

Since Loom uses standard OpenTelemetry under the hood, you can easily point it to any OTel-compliant backend (Datadog, Honeycomb, Jaeger, etc.) by setting the standard environment variables:

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT="https://api.honeycomb.io"
export OTEL_EXPORTER_OTLP_HEADERS="x-honeycomb-team=your-api-key"
```

If your application already has OpenTelemetry configured, Loom will automatically use your existing `TracerProvider` and `MeterProvider` without any additional setup.

## Best Practices

- **Trace IDs**: Always propagate `context.Context` through your application to ensure spans are correctly parented.
- **Attributes**: Use standard semantic conventions for custom attributes when possible to ensure compatibility with various observability tools.
- **Sampling**: In high-throughput production environments, consider configuring a sampler to reduce the volume of telemetry data.
