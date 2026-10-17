# Model Context Protocol (MCP) 🔌

The **Model Context Protocol (MCP)** is an open standard that enables developers to build secure, two-way connections between AI applications and external data sources, tools, and prompts. 

Loom provides first-class support for MCP, implementing **MCP v2 (`2026-07-28`)** with automatic fallback to legacy **MCP v1 (`2025-11-25`)**. Loom manages connection pooling, transport negotiation, authentication, and multi-server orchestration through a clean, unified Go API.

---

## 1. Core Abstractions

Loom's MCP implementation centers around the following types:

- **`Client`**: A pooled client for an individual MCP server. It manages session pooling, idle timeouts (5-minute default), and automatic reconnection transparently.
- **`MultiClient`**: A registry managing multiple named MCP servers simultaneously (`mc.Tools(ctx, "serverName")`, `mc.GetPrompt(...)`).
- **`Config`**: Defines connection parameters, transport (`stdio` or `http`), protocol version negotiation, timeouts, authentication, and elicitation.
- **`AuthProvider`**: Dynamic authentication handlers for OAuth2 Authorization Code, Client Credentials (M2M), and Enterprise OIDC flows.
- **`ElicitationProvider`**: Interactive handlers for server-initiated user input requests (`Form` and `URL` modes).

---

## 2. MCP v2 (`2026-07-28`) Capabilities

Loom includes native support for the MCP v2 specification (`ProtocolVersion20260728`):

### Stateless `server/discover` Negotiation
When establishing a connection, Loom negotiates protocol version `2026-07-28` via stateless discovery (`server/discover`). If the server only supports MCP v1, Loom automatically falls back to the legacy stateful `initialize` handshake (`ProtocolVersion20251125`).

### Stateless Subscriptions (`subscriptions/listen`, SEP-2575)
Under MCP v2, persistent standalone SSE GET streams are replaced by request-scoped streamable HTTP transports and stateless subscription listening via `subscriptions/listen`.

### Client-Side TTL Caching (SEP-2549)
Loom's MCP transport honors server-provided cache controls and expiration metadata, enabling low-latency responses for static resource listings and tool definitions.

### Multi Round-Trip Request Handling (SEP-2322)
During complex tool execution, MCP servers can issue intermediate `InputRequest` payloads (such as requesting missing arguments, OAuth scopes, or interactive confirmations). Loom automatically captures these elicitation requests, passes them to your `ElicitationProvider`, and retries the call with `InputResponse` and preserved `RequestState`.

### Safety Annotations & Tool Adaptation
Tools returned by an MCP server are automatically adapted to Loom's native `tool.Tool` format:
- **`OutputSchema`**: Fully preserved and mapped for structured output validation.
- **Safety `Annotation`**: Captures behavioral hints:
  - `IsReadOnly`: Inferred from `ReadOnlyHint`.
  - `IsIdempotent`: Inferred from `IdempotentHint`.
  - `IsDangerous`: Inferred from `DestructiveHint` or `!ReadOnlyHint`.
  - `IsOpenWorld`: Inferred from `OpenWorldHint` (defaults to `true`).
- **`ResourceLink`**: Rich MCP resource links are converted to `message.TextBlock` with metadata extras (`type: "resource_link"`, `uri`, `title`, `name`, `description`, `mime_type`).

> [!NOTE]
> **MCP Logging Deprecation (SEP-2577)**: In protocol version `2026-07-28`, protocol-level logging notifications (`OnLogMessage`, `SetLoggingLevel`) are deprecated in favor of standard OpenTelemetry tracing/metrics and process `stderr`.

---

## 3. Configuration (`mcp.Config`)

Each server is configured via an `mcp.Config` struct:

```go
// 1. Local subprocess (stdio)
stdioConfig := mcp.Config{
    Transport: "stdio",
    Command:   "python",
    Args:      []string{"my_mcp_server.py"},
    Env:       map[string]string{"DEBUG": "true"},
}

// 2. Remote Streamable HTTP (MCP v2)
httpConfig := mcp.Config{
    Transport:            "http",
    URL:                  "https://mcp.internal.example.com",
    Headers:              map[string]string{"X-Api-Key": "secret-key"},
    ProtocolVersion:      mcp.ProtocolVersion20260728, // Optional: defaults to latest
    DisableStandaloneSSE: true,                       // Recommended for v2
    MaxRetries:           3,
}
```

### Config Options Reference

| Field | Type | Description |
|---|---|---|
| `Transport` | `string` | `"stdio"` or `"http"`. |
| `Command` / `Args` / `Env` | `string`, `[]string`, `map` | Command executable, arguments, and environment variables for `stdio`. |
| `URL` | `string` | Endpoint URL for remote HTTP servers. |
| `Headers` | `map[string]string` | Static headers (e.g., API keys, static Bearer tokens). |
| `ProtocolVersion` | `string` | Optional version override (`ProtocolVersion20260728` or `ProtocolVersion20251125`). Defaults to negotiating `2026-07-28`. |
| `DisableStandaloneSSE` | `bool` | Disables standalone GET SSE streams on HTTP connections (recommended for MCP v2). |
| `MaxRetries` | `int` | Maximum reconnection attempts for HTTP streams. |
| `DisableMultiRoundTrip`| `bool` | Disables automatic SEP-2322 Multi Round-Trip request elicitation handling. |
| `Auth` | `AuthProvider` | Dynamic authentication provider (OAuth2, Client Credentials, Enterprise). |
| `Elicitation` | `ElicitationProvider` | Handler for interactive user input (`Form` and `URL` modes). |

---

## 4. Integrating Tools with Loom

Loom's `Client` maintains a pooled session with an idle timer. You call `client.Tools(ctx)` directly on the client without manual session setup:

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

    // 1. Configure and instantiate client
    client := mcp.NewClient(mcp.Config{
        Transport: "stdio",
        Command:   "npx",
        Args:      []string{"-y", "@modelcontextprotocol/server-everything"},
    })
    defer client.Close()

    // 2. Fetch tools (adapted to native Loom tools)
    tools, err := client.Tools(ctx)
    if err != nil {
        log.Fatalf("Failed to fetch tools: %v", err)
    }

    for _, t := range tools {
        fmt.Printf("Tool: %-20s ReadOnly: %-5v Dangerous: %-5v\n", 
            t.Definition.Name, 
            t.Definition.Annotation.IsReadOnly, 
            t.Definition.Annotation.IsDangerous,
        )
    }

    // 3. Register with Loom tool container
    container := tool.NewContainer(tools...)

    // 4. Bind container definitions to an LLM model
    // model = model.BindToolDefs(container.Definitions()...)
}
```

### Calling Tools Directly
You can also execute tools directly via `client.CallTool`:

```go
stream, err := client.CallTool(ctx, "echo", map[string]any{"message": "Hello MCP!"})
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

---

## 5. Resources and Prompts

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
for _, block := range content {
    fmt.Println("Resource text:", block.Text())
}

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

---

## 6. Multi-Server Management (`MultiClient`)

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

---

## 7. Authentication (`mcp/auth.go`)

Loom provides built-in authentication handlers for securing remote MCP connections:

### Static Authentication
For simple bearer tokens or static API keys, supply headers in `mcp.Config`:

```go
config := mcp.Config{
    Transport: "http",
    URL:       "https://api.example.com/mcp",
    Headers: map[string]string{
        "Authorization": "Bearer my-static-api-token",
    },
}
```

### Interactive OAuth2 (`mcp.OAuth2Provider`)
For user-facing CLI or desktop tools requiring the standard OAuth2 Authorization Code flow:

```go
config := mcp.Config{
    Transport: "http",
    URL:       "https://mcp.service.com",
    Auth: &mcp.OAuth2Provider{
        ClientID:     "my-client-id",
        ClientSecret: "my-client-secret", // Optional for public PKCE clients
        RedirectPort: 3142,               // Local callback port (defaults to 3142)
    },
}
```
When connecting, Loom spins up a local loopback server on the redirect port and prompts the user to complete authorization in their browser.

### Headless M2M OAuth2 (`mcp.ClientCredentialsProvider`)
For backend services, cron jobs, and daemons requiring machine-to-machine authentication without user intervention:

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
For enterprise deployments requiring Single Sign-On (OIDC) through an identity provider (e.g., Okta, Entra ID) and downstream token exchange with an MCP authorization server:

```go
config := mcp.Config{
    Transport: "http",
    URL:       "https://enterprise-mcp.internal",
    Auth: &mcp.EnterpriseProvider{
        ClientID:         "my-oidc-client-id",
        ClientSecret:     os.Getenv("OIDC_CLIENT_SECRET"),
        RedirectPort:     3142,
        IdPIssuerURL:     "https://login.okta.com/oauth2/default",
        MCPAuthServerURL: "https://auth.internal.corp",
        MCPResourceURI:   "urn:mcp:enterprise:data",
    },
}
```

---

## 8. Multi Round-Trip Elicitation (SEP-2322)

When an MCP server requires interactive input or user confirmation during tool execution, Loom delegates the request to your `ElicitationProvider`:

```go
type InteractiveElicitationHandler struct{}

func (h *InteractiveElicitationHandler) HandleElicit(ctx context.Context, params *mcp.ElicitParams) (*mcp.ElicitResult, error) {
    // Inspect elicitation mode (Form or URL)
    fmt.Printf("Server requested input: %s\n", params.Message)

    // Form elicitation: collect user inputs
    // URL elicitation: prompt user to complete an external browser action
    return &mcp.ElicitResult{
        Action: "accept",
        Content: map[string]any{
            "confirmation": true,
        },
    }, nil
}

func (h *InteractiveElicitationHandler) HandleElicitComplete(ctx context.Context, params *mcp.ElicitationCompleteParams) {
    fmt.Println("Elicitation complete for ID:", params.ElicitationID)
}

// Attach to Config
config := mcp.Config{
    Transport:   "http",
    URL:         "https://mcp.service.com",
    Elicitation: &InteractiveElicitationHandler{},
}
```

Loom automatically submits the `InputResponse` back to the server and resumes tool execution without breaking agent workflows.

---

## 9. Observability & OpenTelemetry

All MCP tool invocations through `client.CallTool` are automatically instrumented with OpenTelemetry:

- **Client Spans**: Named `tools/call` with standard semantic attributes:
  - `rpc.system.name = "jsonrpc"`
  - `rpc.method = "tools/call"`
  - `gen_ai.tool.name = "<toolName>"`
  - `loom.tool.type = "mcp"`
- **Trace Context Propagation**: Active trace and span context is injected into the MCP JSON-RPC call metadata (`params.Meta`), enabling distributed tracing across microservices and subprocesses.
- **Latency & Error Recording**: Duration and status codes are reported via OpenTelemetry metrics.

---

## Summary

- Use `mcp.NewClient` with `mcp.Config` to connect to stdio or HTTP servers.
- Use `client.Tools(ctx)` directly on the pooled client—no manual session management required.
- Take advantage of **MCP v2 (`2026-07-28`)** stateless discovery, subscriptions, caching, and multi-round-trip elicitation.
- Authenticate with `OAuth2Provider`, `ClientCredentialsProvider`, or `EnterpriseProvider`.
- Manage fleets of MCP servers with `mcp.NewMultiClient`.
