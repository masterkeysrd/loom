---
title: Configure MCP connections
---

For a first connection, [list tools and make a call](../mcp.md). Use this reference to configure a transport, authenticate a server, or inspect protocol behavior.

## Configure a transport

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

## Authenticate a connection

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

### Standard TokenSource (`mcp.TokenSourceProvider`)
You can pass any standard `oauth2.TokenSource` (e.g. `auth.NewPersistentTokenSource`, `auth.StaticTokenSource`, or Google Cloud / AWS credentials) directly for MCP authentication:

```go
config := mcp.Config{
    Transport: "http",
    URL:       "https://mcp.service.com",
    Auth: &mcp.TokenSourceProvider{
        TokenSource: myTokenSource,
    },
}
```

### Interactive OAuth2 (`mcp.OAuth2Provider`)
For user-facing CLI or desktop tools requiring the standard OAuth2 Authorization Code flow with on-demand loopback listening:

```go
config := mcp.Config{
    Transport: "http",
    URL:       "https://mcp.service.com",
    Auth: &mcp.OAuth2Provider{
        ClientID:     "my-client-id",
        ClientSecret: "my-client-secret", // Optional for public PKCE clients
        RedirectURL:  "http://localhost:3142/callback",
        CodeFetcher: mcp.LoopbackCodeFetcher("http://localhost:3142/callback", auth.LoopbackOptions{
            OnAuthURL: func(url string) {
                fmt.Printf("Authorize at: %s\n", url)
                _ = auth.OpenBrowser(url)
            },
        }),
    },
}
```
Loom spins up a local loopback listener on-demand when an authorization challenge occurs, receives the authorization code, and cleanly closes the listener before completing the exchange.

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
        RedirectURL:      "http://localhost:3142/callback",
        CodeFetcher:      mcp.LoopbackCodeFetcher("http://localhost:3142/callback", auth.LoopbackOptions{}),
        IdPIssuerURL:     "https://login.okta.com/oauth2/default",
        MCPAuthServerURL: "https://auth.internal.corp",
        MCPResourceURI:   "urn:mcp:enterprise:data",
    },
}
```

## Client types

Loom's MCP implementation centers around the following types:

- **`Client`**: A pooled client for an individual MCP server. It manages session pooling, idle timeouts (5-minute default), and automatic reconnection transparently.
- **`MultiClient`**: A registry managing multiple named MCP servers simultaneously (`mc.Tools(ctx, "serverName")`, `mc.GetPrompt(...)`).
- **`Config`**: Defines connection parameters, transport (`stdio` or `http`), protocol version negotiation, timeouts, authentication, and elicitation.
- **`AuthProvider`**: Dynamic authentication handlers for OAuth2 Authorization Code, Client Credentials (M2M), and Enterprise OIDC flows.
- **`ElicitationProvider`**: Interactive handlers for server-initiated user input requests (`Form` and `URL` modes).

## Protocol negotiation and capabilities

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

> **Note.** **MCP Logging Deprecation (SEP-2577)**: In protocol version `2026-07-28`, protocol-level logging notifications (`OnLogMessage`, `SetLoggingLevel`) are deprecated in favor of standard OpenTelemetry tracing/metrics and process `stderr`.

## Handle multi-round-trip elicitation

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

## Trace tool calls

All MCP tool invocations through `client.CallTool` are automatically instrumented with OpenTelemetry:

- **Client Spans**: Named `tools/call` with standard semantic attributes:
  - `rpc.system.name = "jsonrpc"`
  - `rpc.method = "tools/call"`
  - `gen_ai.tool.name = "<toolName>"`
  - `loom.tool.type = "mcp"`
- **Trace Context Propagation**: Active trace and span context is injected into the MCP JSON-RPC call metadata (`params.Meta`), enabling distributed tracing across microservices and subprocesses.
- **Latency & Error Recording**: Duration and status codes are reported via OpenTelemetry metrics.
