// Package loomollama provides an [llm.Provider] implementation backed by the Ollama API.
//
// Ollama is a local LLM runtime that exposes an HTTP API compatible with the
// official Ollama Go SDK. This package translates the generic [llm.Request] /
// [llm.StreamResponse] types into Ollama-specific wire types, hides all
// provider-specific details from the rest of the loom framework, and supports
// any model available in the local Ollama instance.
//
// # Configuration
//
// [NewDefaultProvider] reads connection settings (host, TLS, etc.) from the
// environment using the official Ollama SDK defaults (OLLAMA_HOST, etc.).
//
// # Usage
//
//	p, err := ollama.NewDefaultProvider()
//	if err != nil { ... }
//
//	model := llm.NewModel(p, "qwen3-coder:30b")
//	resp, err := model.Invoke(ctx, messages)
package loomollama
