/*
Package message provides the core primitives for representing and manipulating
conversation history within the Loom framework.

At its heart, this package solves the "Polymorphic Problem" in Go—allowing
diverse roles (User, Assistant, System, Tool) and varied content types
(Text, Tool Calls, and Thinking blocks) to exist in a single,
strongly-typed list that serializes perfectly to JSON.

# Architecture Overview

The package follows a strict hierarchy to ensure state consistency across
different AI graphs (e.g., Ask, Plan, and Agent modes):

 1. MessageList: A slice of Message interfaces with custom JSON logic.
 2. Message: An interface representing a single turn in a conversation.
 3. Content: A collection of Blocks that represent the actual data in a message.
 4. Block: The smallest unit of data (e.g., a TextBlock).

# Role-Based Identity

Every message is assigned a Role (system, assistant, user, or tool). This
allows the Loom engine to filter history, manage context windows, and
properly format prompts for different LLM providers like Google GenAI or Ollama.

# JSON Serialization

A key feature of this package is its "Flattened Polymorphism." When
marshaling to JSON, the package avoids deep nesting. Instead of:

	{"role": "user", "data": {"content": "..."}}

It produces a clean, industry-standard flat structure:

	{"role": "user", "id": "uuid", "content": [{"kind": "text", "text": "..."}]}

This makes the message history compatible with Postgres JSONB columns and
frontend dashboards without requiring complex mapping logic.

# Thread Safety and Identification

Every message embeds a Base struct, providing a unique ID. This ID is
critical for tracing an execution from a simple user prompt through
multiple planning and agentic steps.
*/
package message
