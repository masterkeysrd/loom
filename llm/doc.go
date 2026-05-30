// Package llm provides provider-agnostic abstractions for interacting with
// large language models.
//
// # Overview
//
// The central type is [Model], which wraps a [Provider] and a model name.
// It exposes two execution modes:
//
//   - [Model.Invoke]: blocks until the full response is available and returns
//     a [message.Assistant].
//   - [Model.Stream]: returns an iterator over [message.AssistantChunk] values,
//     forwarding each chunk to a [StreamWriter] stored in the context so that
//     upstream code (e.g. a graph node) can surface output in real time.
//
// # Providers
//
// [Provider] is the interface that concrete backends implement:
//
//	type Provider interface {
//	    Name() string
//	    Stream(context.Context, *Request) (StreamResponse, error)
//	}
//
// Add a new backend by implementing [Provider] and registering it with
// [Registry.Register]. The [ollama] sub-package ships a ready-to-use
// implementation.
//
// # Registry
//
// [Registry] is a thread-safe map from provider name to [Provider]. It allows
// different parts of the application to resolve a provider by name without
// creating build-time dependencies on backend packages.
//
// # Streaming Integration
//
// [WithStreamWriter] and [StreamWriterFromContext] store and retrieve a
// [StreamWriter] in the context. The graph package wraps its own stream adapter
// so that both graph-level events and LLM token chunks flow through the same
// context-carried interface.
package llm
