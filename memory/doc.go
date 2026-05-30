// Package memory provides utilities for managing long-running conversation
// history inside the Loom framework.
//
// The central primitive is [Summarizer], which compresses a slice of messages
// into a concise [Summary] by invoking an LLM. Callers control the behaviour
// through [Config] (system prompt, messages to retain, etc.), while the
// Summarizer itself depends only on the [llm.Invoker] interface so any
// compliant model backend can be substituted.
package memory
