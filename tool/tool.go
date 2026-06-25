package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// ErrInvalidInput is a sentinel error for any tool input validation failure.
var ErrInvalidInput = errors.New("tool input validation failed")

// ErrToolNotFound is a sentinel error for when a tool is not found in a container.
var ErrToolNotFound = errors.New("tool not found")

// ValidationError provides context for a validation failure.
type ValidationError struct {
	ToolName string
	Err      error // The underlying jsonschema error
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("tool %q: %v: %v", e.ToolName, ErrInvalidInput, e.Err)
}

func (e *ValidationError) Unwrap() error {
	return e.Err
}

func (e *ValidationError) Is(target error) bool {
	return target == ErrInvalidInput
}

// Definition describes a callable tool that can be offered to an LLM.
// Schemas are inferred from Go types by [New] and must not be mutated after construction.
type Definition struct {
	Name         string
	Title        string
	Description  string
	Annotation   Annotation
	InputSchema  *jsonschema.Schema
	OutputSchema *jsonschema.Schema
}

// Annotation defines the safety and execution profile of a tool.
type Annotation struct {
	// IsOpenWorld indicates if the tool interacts with resources outside
	// the local machine (Internet, SaaS APIs, Remote Databases).
	IsOpenWorld bool

	// IsDangerous indicates if the tool can perform destructive actions
	// (deleting files, dropping tables, killing system processes).
	IsDangerous bool

	// IsReadOnly indicates if the tool only retrieves data without
	// modifying any state (local or remote).
	IsReadOnly bool

	// IsIdempotent indicates if the tool can be safely retried multiple
	// times without unintended side effects.
	IsIdempotent bool

	// UserHint is a short description of the tool's impact to be
	// displayed during a Human-In-The-Loop approval prompt.
	UserHint string
}

// ToolStream is an iterator over [message.ToolChunk] values produced by a tool.
type ToolStream func(yield func(message.ToolChunk, error) bool)

// ToolHandler is the type-erased handler for a [Tool].
// It receives a raw [message.ToolCall] and returns a [ToolStream] result
// that can be iterated to get chunks or aggregated into a final result.
type ToolHandler func(context.Context, *message.ToolCall) (ToolStream, error)

// HandlerFunc is the typed handler variant accepted by [New].
// The framework decodes and validates call.Args into In before invoking the
// function. The output Out is JSON-encoded and placed in the [message.Tool]
// content automatically so implementations never need to build message types.
type HandlerFunc[In, Out any] func(context.Context, In) (Out, error)

// StreamHandlerFunc is the typed handler variant for tools that natively stream results.
type StreamHandlerFunc[In any] func(context.Context, In) (ToolStream, error)

// TextContentProvider is an optional interface that tool output types can implement to
// control how their result is rendered as plain text for the LLM. When Out
// implements TextContentProvider, [AdaptHandler] calls TextContent() instead of JSON-encoding
// the value. The structured Go value is still available via [message.Tool.Structured].
type TextContentProvider interface {
	TextContent() string
}

// ContentProvider is an optional interface that tool output types can implement to
// return multiple content blocks (e.g. text and images) as the tool result.
type ContentProvider interface {
	ToolContent() message.Content
}

// Error represents a functional error that should be reported to the LLM.
// Returning this from a tool handler will result in a Tool message with IsError=true.
type Error struct {
	Content message.Content
}

func (e *Error) Error() string {
	return e.Content.Text()
}

// NewError creates a new functional tool error from a text message.
func NewError(text string) *Error {
	return &Error{
		Content: message.Content{&message.TextBlock{Text: text}},
	}
}

// Tool combines a [Definition] description with its executable [ToolHandler].
// Obtain one via [New]; do not construct directly.
type Tool struct {
	Definition Definition
	Annotation Annotation
	Handler    ToolHandler
}

// Option is a functional option applied to a [Tool] after construction.
type Option func(*Tool)

// WithAnnotation sets the [Annotation] on the [Tool].
func WithAnnotation(a Annotation) Option {
	return func(d *Tool) {
		d.Annotation = a
		d.Definition.Annotation = a
	}
}

// AdaptHandler wraps a typed [HandlerFunc] into a [ToolHandler] by:
//   - Pre-resolving the given schema once so every call uses the cached form.
//   - Validating call.Args against the resolved schema before invoking fn.
//   - Decoding the validated map into In via a JSON round-trip.
//   - JSON-encoding the Out value and yielding it as a single chunk.
func AdaptHandler[In, Out any](name, desc string, schema *jsonschema.Resolved, fn HandlerFunc[In, Out]) ToolHandler {
	return func(ctx context.Context, call *message.ToolCall) (ToolStream, error) {
		// Validate the raw argument map against the inferred JSON schema.
		// map[string]any is the canonical "JSON value" form jsonschema expects.
		if err := schema.Validate(call.Args); err != nil {
			return nil, &ValidationError{ToolName: name, Err: err}
		}

		// Decode the validated args map into the strongly-typed input struct.
		var input In
		data, err := json.Marshal(call.Args)
		if err != nil {
			return nil, fmt.Errorf("tool %q: failed to marshal args: %w", name, err)
		}
		if err := json.Unmarshal(data, &input); err != nil {
			return nil, fmt.Errorf("tool %q: failed to decode args into input type: %w", name, err)
		}

		return func(yield func(message.ToolChunk, error) bool) {
			// Span name MUST be execute_tool {gen_ai.tool.name}
			ctx, span := telemetry.Start(ctx, "execute_tool "+name, trace.WithSpanKind(trace.SpanKindInternal))
			defer span.End()

			span.SetAttributes(
				telemetry.WithOperation(telemetry.OpExecuteTool),
				telemetry.WithToolName(name),
				telemetry.WithToolCallID(call.ID),
				telemetry.WithToolDescription(desc),
				attribute.String("loom.tool.type", "local"),
			)

			// Record tool arguments if content recording is enabled
			if telemetry.ShouldRecordContent(ctx) {
				if args, err := json.Marshal(call.Args); err == nil {
					span.SetAttributes(telemetry.KeyContentToolArguments.String(string(args)))
				}
			}

			startTime := time.Now()
			out, err := fn(ctx, input)
			telemetry.RecordToolDuration(ctx, time.Since(startTime), telemetry.WithToolName(name))

			if err != nil {
				if toolErr, ok := err.(*Error); ok {
					if telemetry.ShouldRecordContent(ctx) {
						span.SetAttributes(telemetry.KeyContentToolResult.String(toolErr.Error()))
					}
					yield(message.ToolChunk{
						Content: toolErr.Content,
						IsError: true,
					}, nil)
					return
				}

				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				span.SetAttributes(telemetry.WithErrorType(fmt.Sprintf("%T", err)))
				yield(message.ToolChunk{}, err)
				return
			}

			var content message.Content
			if p, ok := any(out).(ContentProvider); ok {
				content = p.ToolContent()
			} else if p, ok := any(out).(TextContentProvider); ok {
				content = message.Content{&message.TextBlock{Text: p.TextContent()}}
			} else {
				outData, err := json.Marshal(out)
				if err != nil {
					yield(message.ToolChunk{}, fmt.Errorf("tool %q: failed to marshal output: %w", name, err))
					return
				}
				content = message.Content{&message.TextBlock{Text: string(outData)}}
			}

			if telemetry.ShouldRecordContent(ctx) {
				span.SetAttributes(telemetry.KeyContentToolResult.String(content.Text()))
			}

			yield(message.ToolChunk{
				Content:           content,
				StructuredContent: out,
			}, nil)
		}, nil
	}
}

// AdaptStreamHandler wraps a typed [StreamHandlerFunc] into a [ToolHandler].
func AdaptStreamHandler[In any](name, desc string, schema *jsonschema.Resolved, fn StreamHandlerFunc[In]) ToolHandler {
	return func(ctx context.Context, call *message.ToolCall) (ToolStream, error) {
		// Validate the raw argument map against the inferred JSON schema.
		if err := schema.Validate(call.Args); err != nil {
			return nil, &ValidationError{ToolName: name, Err: err}
		}

		// Decode the validated args map into the strongly-typed input struct.
		var input In
		data, err := json.Marshal(call.Args)
		if err != nil {
			return nil, fmt.Errorf("tool %q: failed to marshal args: %w", name, err)
		}
		if err := json.Unmarshal(data, &input); err != nil {
			return nil, fmt.Errorf("tool %q: failed to decode args into input type: %w", name, err)
		}

		return func(yield func(message.ToolChunk, error) bool) {
			// Span name MUST be execute_tool {gen_ai.tool.name}
			ctx, span := telemetry.Start(ctx, "execute_tool "+name, trace.WithSpanKind(trace.SpanKindInternal))
			defer span.End()

			span.SetAttributes(
				telemetry.WithOperation(telemetry.OpExecuteTool),
				telemetry.WithToolName(name),
				telemetry.WithToolCallID(call.ID),
				telemetry.WithToolDescription(desc),
				attribute.String("loom.tool.type", "local"),
			)

			// Record tool arguments if content recording is enabled
			if telemetry.ShouldRecordContent(ctx) {
				if args, err := json.Marshal(call.Args); err == nil {
					span.SetAttributes(telemetry.KeyContentToolArguments.String(string(args)))
				}
			}

			stream, err := fn(ctx, input)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				yield(message.ToolChunk{}, err) // FIX: Yield the error to the caller
				return
			}

			for chunk, err := range stream {
				if err != nil {
					span.RecordError(err)
					span.SetStatus(codes.Error, err.Error())
					if !yield(chunk, err) {
						return
					}
					continue
				}

				if !yield(chunk, err) {
					return
				}
			}
		}, nil
	}
}

// New creates a [Tool] from a typed [HandlerFunc] by:
//   - Inferring InputSchema from In and OutputSchema from Out via reflection.
//   - Pre-resolving the input schema at construction time for fast validation.
//   - Adapting the typed handler into a [ToolHandler] via [AdaptHandler].
//   - Applying any [Option] values (e.g. [WithAnnotation]) after construction.
func New[In, Out any](name, title, description string, handler HandlerFunc[In, Out], opts ...Option) (*Tool, error) {
	inputSchema, err := jsonschema.For[In](nil)
	if err != nil {
		return nil, fmt.Errorf("tool %q: failed to infer input schema: %w", name, err)
	}

	outputSchema, err := jsonschema.For[Out](nil)
	if err != nil {
		return nil, fmt.Errorf("tool %q: failed to infer output schema: %w", name, err)
	}

	// Resolve the input schema once at construction time so validation on every
	// call uses the pre-resolved form without re-processing the schema.
	resolvedInput, err := inputSchema.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("tool %q: failed to resolve input schema: %w", name, err)
	}

	def := &Tool{
		Definition: Definition{
			Name:         name,
			Title:        title,
			Description:  description,
			InputSchema:  inputSchema,
			OutputSchema: outputSchema,
		},
		Handler: AdaptHandler(name, description, resolvedInput, handler),
	}
	for _, opt := range opts {
		opt(def)
	}
	return def, nil
}

// NewStreaming creates a [Tool] from a typed [StreamHandlerFunc].
func NewStreaming[In any](name, title, description string, handler StreamHandlerFunc[In], opts ...Option) (*Tool, error) {
	inputSchema, err := jsonschema.For[In](nil)
	if err != nil {
		return nil, fmt.Errorf("tool %q: failed to infer input schema: %w", name, err)
	}

	// Resolve the input schema once at construction time so validation on every
	// call uses the pre-resolved form without re-processing the schema.
	resolvedInput, err := inputSchema.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("tool %q: failed to resolve input schema: %w", name, err)
	}

	def := &Tool{
		Definition: Definition{
			Name:        name,
			Title:       title,
			Description: description,
			InputSchema: inputSchema,
		},
		Handler: AdaptStreamHandler(name, description, resolvedInput, handler),
	}
	for _, opt := range opts {
		opt(def)
	}
	return def, nil
}
