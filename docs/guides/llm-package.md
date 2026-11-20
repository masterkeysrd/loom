---
title: Call a language model
description: Send a message to a model using Loom’s provider-independent Go API.
---

Use `llm.Model` directly in a Go application or call it inside a graph node. A provider connects the model to a particular service. The `message` package carries the same conversation types across providers.

## Send your first message

This example uses OpenAI. Set `OPENAI_API_KEY`, choose a model available to your account, and install the imports with `go mod tidy` after saving the program as `main.go` in a Go module. For another backend, follow [Choose a provider](../providers.md).

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/masterkeysrd/loom/llm"
    "github.com/masterkeysrd/loom/message"
    loomopenai "github.com/masterkeysrd/loom/providers/openai"
)

func main() {
    provider, err := loomopenai.NewDefaultProvider()
    if err != nil {
        log.Fatal(err)
    }
    model, err := llm.NewModel(provider, "gpt-4o", nil)
    if err != nil {
        log.Fatal(err)
    }
    messages := []message.Message{message.NewUserText("Say hello in Spanish.")}
    reply, err := model.Invoke(context.Background(), messages)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(reply.GetContent().Text())
}
```

Run `go run .` to print the model’s reply. To continue the conversation, append the assistant reply and the next user message to your history. See [Conversations and messages](./conversations.md) for roles and multimodal input.

## Explore the model API

- [Stream responses and configure generation](./llm/generation.md) for incremental output, JSON schemas, reasoning, and provider-specific hints.
- [Apply middleware](./llm/middleware.md) to wrap model calls and adapt tool definitions.
- [Manage caches and stateful sessions](./llm/sessions.md) to reuse context and reconcile history.
- <a id="5-live-model-discovery-llmmodellister"></a>[Discover models and register providers](./llm/discovery.md) to select a backend at runtime and check supported inputs.
- <a id="6-quota--rate-limit-inspection-llmquotaprovider"></a>[Inspect quotas and costs](./llm/usage.md) to see remaining capacity and interpret usage metrics.
