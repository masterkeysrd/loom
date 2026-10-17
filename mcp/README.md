# mcp

The `mcp` package provides a high-performance, production-ready client for the **Model Context Protocol (MCP)** in Go.

It features native support for **MCP v2 (`2026-07-28`)** with automatic fallback to legacy **MCP v1 (`2025-11-25`)**, transparent connection pooling with idle timeouts, multi-server orchestration, rich authentication flows, and interactive human-in-the-loop elicitation.

---

## Core Abstractions

| Type | Description |
|---|---|
| `Client` | Pooled client for an individual MCP server. Manages session pooling, idle cleanup (5-min default), and auto-reconnection. |
| `MultiClient` | Multi-server coordinator managing a fleet of named MCP server clients under a unified API. |
| `Config` | Configuration parameters for stdio subprocesses or Streamable HTTP servers. |
| `AuthProvider` | Interface for generating authentication handlers (`OAuth2Provider`, `ClientCredentialsProvider`, `EnterpriseProvider`). |
| `ElicitationProvider` | Interface for handling server-initiated input requests (`Form` and `URL` modes) during tool calls. |

---

## MCP v2 (`2026-07-28`) Capabilities

- **Stateless `server/discover` Negotiation**: Automatically negotiates protocol version `2026-07-28` via stateless discovery (`server/discover`) with transparent fallback to legacy `initialize` (`2025-11-25`).
- **Stateless Subscriptions (`subscriptions/listen`, SEP-2575)**: Replaces standalone SSE streams with request-scoped streamable HTTP and stateless subscription listeners.
- **Client-Side TTL Caching (SEP-2549)**: Honors server-provided cache controls and expiration headers to eliminate unnecessary roundtrips.
- **Multi Round-Trip Request Handling (SEP-2322)**: Server `InputRequest` payloads (elicitation, confirmations) are handled transparently, retrying with `InputResponse` and preserved `RequestState`.
- **Tool Safety Annotations & Output Schemas**: Preserves tool `OutputSchema` and maps MCP annotations to Loom `tool.Annotation`:
  - `IsReadOnly` (from `ReadOnlyHint`)
  - `IsIdempotent` (from `IdempotentHint`)
  - `IsDangerous` (derived from `DestructiveHint` or `!ReadOnlyHint`)
  - `IsOpenWorld` (from `OpenWorldHint`, defaults to `true`)
- **`ResourceLink` Support**: MCP resource links are converted into `message.TextBlock` with metadata extras (`uri`, `name`, `title`, `description`, `mime_type`).
- **MCP Logging Deprecation (SEP-2577)**: Protocol-level logging notifications (`OnLogMessage`, `SetLoggingLevel`) are deprecated in protocol version `2026-07-28` in favor of OpenTelemetry and standard `stderr`.

---

## Usage Examples

### 1. Connecting and Extracting Tools
The `Client` manages connection pooling internally. Call `client.Tools(ctx)` directly:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/masterkeysrd/loom/mcp"
    "github.com/masterkeysrd/loom/tool"
)

func main() {
    ctx := context.Background()

    // 1. Configure client (stdio subprocess or HTTP)
    client := mcp.NewClient(mcp.Config{
        Transport: "stdio",
        Command:   "python",
        Args:      []string{"weather_mcp.py"},
    })
    defer client.Close()

    // 2. Extract tools (adapted to native Loom tools)
    tools, err := client.Tools(ctx)
    if err != nil {
        log.Fatalf("Failed to fetch tools: %v", err)
    }

    for _, t := range tools {
        fmt.Printf("Loaded tool: %s (ReadOnly: %v, Dangerous: %v)\n",
            t.Definition.Name,
            t.Definition.Annotation.IsReadOnly,
            t.Definition.Annotation.IsDangerous,
        )
    }

    // 3. Register with Loom tool container
    container := tool.NewContainer(tools...)
    _ = container
}
```

### 2. Calling Tools Directly
Tools can be invoked directly through the client using `CallTool`:

```go
stream, err := client.CallTool(ctx, "get_weather", map[string]any{"city": "San Francisco"})
if err != nil {
    log.Fatal(err)
}

for chunk, err := range stream {
    if err != nil {
        log.Fatal(err)
    }
    if chunk.Content != nil {
        fmt.Print(chunk.Content.Text())
    }
    if chunk.StructuredContent != nil {
        fmt.Printf("JSON: %+v\n", chunk.StructuredContent)
    }
}
```

### 3. Resources and Prompt Templates

```go
// List and read resources
resources, _ := client.Resources(ctx)
content, _ := client.GetResources(ctx, []string{"postgres://public/schema"})

// List and fetch prompt templates
prompts, _ := client.Prompts(ctx)
messages, _ := client.GetPrompt(ctx, "sql_assistant", map[string]string{
    "table": "users",
})
```

### 4. Multi-Server Management (`MultiClient`)

```go
configs := map[string]mcp.Config{
    "weather": {Transport: "http", URL: "https://weather.mcp.example.com"},
    "database": {Transport: "stdio", Command: "mcp-db-server"},
}

mc := mcp.NewMultiClient(configs)
defer mc.Close()

// Query tools from specific servers
weatherTools, _ := mc.Tools(ctx, "weather")
dbTools, _ := mc.Tools(ctx, "database")

// Query prompts from specific servers
prompts, _ := mc.Prompts(ctx, "weather")

// Check server status
status, _ := mc.Status("database")
fmt.Println("DB Status:", status)
```

---

## Authentication (`mcp/auth.go`)

### Static Headers & Environment Variables
```go
config := mcp.Config{
    Transport: "http",
    URL:       "https://mcp.internal.example.com",
    Headers:   map[string]string{"Authorization": "Bearer static-token"},
}
```

### Interactive OAuth2 (`mcp.OAuth2Provider`)
Spins up a local loopback server on `RedirectPort` (default `3142`) and opens the browser:
```go
config := mcp.Config{
    Transport: "http",
    URL:       "https://mcp.service.com",
    Auth: &mcp.OAuth2Provider{
        ClientID:     "client-id-123",
        ClientSecret: "client-secret", // Optional for public clients
        RedirectPort: 3142,
    },
}
```

### Headless M2M OAuth2 (`mcp.ClientCredentialsProvider`)
For backend services and daemon processes:
```go
config := mcp.Config{
    Transport: "http",
    URL:       "https://mcp.service.com",
    Auth: &mcp.ClientCredentialsProvider{
        ClientID:     os.Getenv("MCP_CLIENT_ID"),
        ClientSecret: os.Getenv("MCP_CLIENT_SECRET"),
    },
}
```

### Enterprise Managed Authorization (`mcp.EnterpriseProvider`)
For enterprise single sign-on (OIDC) with Okta, Azure AD, etc.:
```go
config := mcp.Config{
    Transport: "http",
    URL:       "https://enterprise-mcp.internal",
    Auth: &mcp.EnterpriseProvider{
        ClientID:         "enterprise-client",
        ClientSecret:     os.Getenv("OIDC_SECRET"),
        RedirectPort:     3142,
        IdPIssuerURL:     "https://login.okta.com/oauth2/default",
        MCPAuthServerURL: "https://auth.internal.corp",
        MCPResourceURI:   "urn:mcp:enterprise:data",
    },
}
```

---

## Interactive Elicitation (SEP-2322)

Handle server requests for user input or approval during tool runs:

```go
type MyElicitationHandler struct{}

func (h *MyElicitationHandler) HandleElicit(ctx context.Context, params *mcp.ElicitParams) (*mcp.ElicitResult, error) {
    fmt.Println("Server request:", params.Message)
    // Return user input or confirmation
    return &mcp.ElicitResult{
        Action:  "accept",
        Content: map[string]any{"confirmed": true},
    }, nil
}

func (h *MyElicitationHandler) HandleElicitComplete(ctx context.Context, params *mcp.ElicitationCompleteParams) {
    fmt.Println("Elicitation complete:", params.ElicitationID)
}

config := mcp.Config{
    Transport:   "http",
    URL:         "https://mcp.service.com",
    Elicitation: &MyElicitationHandler{},
}
```

---

## Observability

All tool calls via `client.CallTool` record OpenTelemetry spans:
- Span Name: `tools/call`
- Attributes: `rpc.system.name = "jsonrpc"`, `rpc.method = "tools/call"`, `gen_ai.tool.name = "<name>"`, `loom.tool.type = "mcp"`.
- Distributed Tracing: Active trace context is injected into MCP JSON-RPC call metadata (`params.Meta`).
