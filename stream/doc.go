// Package stream provides a unified mechanism for emitting data objects into
// an execution stream via context-aware writer injection.
//
// The core abstraction is the [Writer] interface, which defines a single
// Write method for emitting any data object. Implementations typically use
// type switches to route data to the appropriate event types.
//
// # Context Injection
//
// Writers and metadata are stored in the context using dedicated context keys,
// allowing any layer of the call stack to emit events without direct
// dependency on the underlying implementation.
//
//	A Writer is injected via WithWriter and retrieved with WriterFromContext.
//	Metadata is injected via WithMetadata and retrieved with MetadataFromContext.
//
// # Global Fallback
//
// A global writer can be set via SetGlobalWriter as a fallback when no writer
// is present in the context. This enables emission from deeply nested call
// stacks without requiring explicit context propagation.
//
// # Usage
//
//	// Inject a writer into context
//	ctx = stream.WithWriter(ctx, myWriter)
//
//	// Emit data from anywhere in the call stack
//	w, ok := stream.WriterFromContext(ctx)
//	if ok {
//	    w.Write(ctx, someData)
//	}
package stream
