---
title: Connect to an MCP server
description: Discover tools on an MCP server and make them available to your model.
---

Use Loom’s Model Context Protocol (MCP) client to discover tools on a server and call them from Go. You can try discovery before connecting a language model.

## List the server’s tools

You need a running MCP server that exposes a Streamable HTTP endpoint. In a Go module, save the following as `main.go`. The program accepts the endpoint as its only argument and prints one tool name per line.

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"

    "github.com/masterkeysrd/loom/mcp"
)

func main() {
    if len(os.Args) != 2 {
        log.Fatal("usage: go run . MCP_ENDPOINT")
    }
    client := mcp.NewClient(mcp.Config{Transport: "http", URL: os.Args[1]})
    defer client.Close()

    tools, err := client.Tools(context.Background())
    if err != nil {
        log.Fatal(err)
    }
    for _, t := range tools {
        fmt.Println(t.Definition.Name)
    }
}
```

Replace the example URL with your server’s MCP endpoint and run the program.

```sh
go get github.com/masterkeysrd/loom/mcp
go run . http://localhost:8080/mcp
```

A server exposing a tool named `echo` prints `echo`. An empty list means the server advertised no tools. For a local subprocess, configure `Transport: "stdio"` with its executable and arguments. For credentials and transport examples, see [Configure MCP connections](./mcp/configuration.md).

## Call a tool directly

Use a name from the listing and arguments matching that tool’s schema. This example assumes the server’s `echo` tool takes a `message` string. Add this call after the listing loop, while the client is still open.

```go
stream, err := client.CallTool(context.Background(), "echo", map[string]any{"message": "Hello MCP!"})
if err != nil {
    log.Fatal(err)
}

for chunk, err := range stream {
    if err != nil {
        log.Fatal(err)
    }
    // Access streamed text content
    if chunk.Content != nil {
        fmt.Print(chunk.Content.Text())
    }
    // Access structured JSON output (if provided by server)
    if chunk.StructuredContent != nil {
        fmt.Printf("Structured: %+v\n", chunk.StructuredContent)
    }
}
```

## Give the tools to a model

With an initialized `llm.Model`, bind the tools you discovered before invoking the model. The returned assistant message can include tool calls. Use a [tool container](./tools.md#2-using-the-tool-container) to execute those calls and pass the results back in the conversation.

```go
model = model.BindTools(tools...)
```

## Read resources and use prompts

In addition to tools, MCP servers can expose structured data resources and prompt templates.

### Resources
Query resources directly using the pooled client:

```go
// 1. List available resources
resources, err := client.Resources(ctx)
for _, r := range resources {
    fmt.Printf("Resource: %s (%s, %d bytes)\n", r.URI, r.MIMEType, r.Size)
}

// 2. Read specific resources (returns Loom message.Content blocks)
content, err := client.GetResources(ctx, []string{"file:///logs/app.log"})
if err != nil {
    return err
}
fmt.Println(content.Text())

// 3. Subscribe to real-time resource updates
err = client.SubscribeResource(ctx, "file:///logs/app.log")
```

### Prompt Templates
Retrieve prompt templates ready for injection into conversation histories:

```go
// 1. List available prompts
prompts, err := client.Prompts(ctx)
for _, p := range prompts {
    fmt.Printf("Prompt: %s - %s\n", p.Name, p.Description)
}

// 2. Fetch prompt with argument substitution (returns []message.Message)
messages, err := client.GetPrompt(ctx, "code_review", map[string]string{
    "language": "Go",
    "diff":     gitDiffOutput,
})

// 3. Append to conversation history
conversation = append(conversation, messages...)
```

## Connect multiple servers

When your application integrates multiple MCP servers (e.g., GitHub, Postgres, Slack), manage them through a `MultiClient`:

```go
configs := map[string]mcp.Config{
    "postgres": {
        Transport: "stdio",
        Command:   "mcp-server-postgres",
        Args:      []string{"--conn", "postgres://localhost/mydb"},
    },
    "weather": {
        Transport: "http",
        URL:       "https://weather.mcp.example.com",
    },
}

mc := mcp.NewMultiClient(configs)
defer mc.Close()

// Query tools from the postgres server
dbTools, err := mc.Tools(ctx, "postgres")

// Query prompts from the weather server
prompts, err := mc.Prompts(ctx, "weather")

// Check individual server health
status, err := mc.Status("postgres")
fmt.Println("Postgres server status:", status)

// Restart an individual server connection if needed
err = mc.Restart(ctx, "postgres")
```

For authentication, pooling, protocol negotiation, subscriptions, and tracing, see the [MCP configuration and protocol reference](./mcp/configuration.md).
