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

Loom Studio is a built-in control plane for your agents. It provides a real-time visualization layer on top of your telemetry data.

### Running Loom Studio

To start Loom Studio, run:

```bash
loom studio
```

By default, it will:
- Open a web dashboard on `http://localhost:8080`.
- Listen for OTLP gRPC telemetry on `localhost:4317`.
- Listen for OTLP HTTP telemetry on `localhost:4318`.
- Store data in a local SQLite database at `.loom/telemetry.db`.

### Core Views

1.  **Dashboard**: A high-level overview of system health, featuring token consumption, LLM call volume, P50 latency, and tool invocation counts.
2.  **Trace Explorer**: An audit log of every execution thread. You can search by Thread ID or Graph name to find specific interactions.
3.  **Trace Waterfall**: A detailed Gantt-style view of a single execution. It breaks down the timing of every LLM call, node transition, and tool execution.
4.  **Metrics Explorer**: A deep-dive into time-series data. It supports **Temporal Bucketing** (1s to 1h intervals) to smooth out data and ensure fast rendering even with millions of points.

### Semantic Conventions (v1.41.1)

Loom strictly adheres to the latest **OpenTelemetry GenAI Semantic Conventions**. This means spans and metrics are standardized:
- **Inference Spans**: Named `chat {model}` with attributes for tokens (input/output/reasoning/cache), temperature, and response IDs.
- **Tool Spans**: Named `execute_tool {name}`.
- **Histograms**: Use the official explicit bucket boundaries for precise distribution analysis.

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
