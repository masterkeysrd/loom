# Model Context Protocol (MCP) 🔌

The Model Context Protocol (MCP) is an open standard that enables developers to build secure, two-way connections between their data sources and AI-powered tools. Loom provides first-class support for MCP, allowing you to easily integrate tools, resources, and prompts from any MCP-compliant server.

## 1. Core Concepts

Loom's MCP package revolves around three main components:

- **`Client`**: A handle to a single MCP server.
- **`MultiClient`**: A registry for managing multiple MCP servers simultaneously.
- **`SessionClient`**: A stateful, session-scoped client for executing tools and fetching resources.

## 2. Configuration

MCP servers can be local subprocesses (stdio) or remote servers (HTTP/SSE).

```go
config := mcp.Config{
    Transport: "stdio",
    Command:   "python",
    Args:      []string{"my_server.py"},
    Env:       map[string]string{"API_KEY": "secret"},
}

// Or for a remote server
config := mcp.Config{
    Transport: "http",
    URL:       "https://mcp.example.com/sse",
    Headers:   map[string]string{"Authorization": "Bearer token"},
}
```

## 3. Integrating Tools

One of the most powerful features of MCP is the ability to dynamically extract tools from a server and use them within Loom.

```go
client := mcp.NewClient(config)
ctx := context.Background()

// Start a session
session, _ := client.Session(ctx)
defer session.Close()

// Extract tools as native Loom tools
tools, _ := session.Tools(ctx)

// Add them to a Loom tool container
container := tool.NewContainer(tools...)
```

## 4. Resources and Prompts

MCP servers can also expose data resources and pre-defined prompt templates.

### Fetching Resources

Resources are fetched as `ResourceContents`, which can contain text or binary data.

```go
// Fetch all resources from a server
resources, _ := client.GetResources(ctx, nil)

for _, res := range resources {
    fmt.Printf("URI: %s, Text: %s\n", res.URI, res.Text)
}
```

### Using Prompts

MCP prompts are returned as a slice of native Loom messages (`[]message.Message`), making them ready to be injected into a conversation.

```go
// Fetch a prompt template with arguments
messages, _ := client.GetPrompt(ctx, "summarize", map[string]string{
    "text": "The long text to summarize...",
})

// Add them to your message history
history = append(history, messages...)
```

## 5. Multi-Server Management

The `MultiClient` allows you to manage multiple MCP servers under a single registry.

```go
configs := map[string]mcp.Config{
    "weather": {Transport: "http", URL: "http://localhost:8000/mcp"},
    "db":      {Transport: "stdio", Command: "mcp-db-server"},
}

mc := mcp.NewMultiClient(configs)

// Fetch tools from the weather server
session, _ := mc.Session(ctx, "weather")
weatherTools, _ := session.Tools(ctx)
```

## 6. Authentication

Loom supports both static and dynamic authentication flows for MCP.

### Static Auth
Use `Headers` in `Config` for simple API keys or `Env` for local subprocess credentials.

### Dynamic Auth (OAuth2 & Enterprise)
For servers requiring interactive login, use the `AuthProvider` interface.

```go
config := mcp.Config{
    Transport: "http",
    URL:       "https://enterprise.mcp.com/sse",
    Auth: &mcp.EnterpriseProvider{
        ClientID:     "my-id",
        IdPIssuerURL: "https://okta.example.com",
        // ...
    },
}
```
When a session is started, Loom will automatically spin up a local callback server and prompt the user to authenticate in their browser.

## 7. Elicitation (Interactive Input)

Elicitation allows an MCP server to request information or actions from the user during a session (e.g., asking for a password or confirming an action).

To support this, implement the `ElicitationProvider` interface and provide it in your `Config`.

```go
type MyElicitProvider struct{}

func (p *MyElicitProvider) HandleElicit(ctx context.Context, params *mcp.ElicitParams) (*mcp.ElicitResult, error) {
    fmt.Println("Server request:", params.Message)
    // Handle form input or URL action here...
    return &mcp.ElicitResult{Action: "accept"}, nil
}

func (p *MyElicitProvider) HandleElicitComplete(ctx context.Context, params *mcp.ElicitationCompleteParams) {
    fmt.Println("Elicitation complete:", params.ElicitationID)
}

config := mcp.Config{
    Transport:   "stdio",
    Command:     "my-server",
    Elicitation: &MyElicitProvider{},
}
```

Loom provides the infrastructure to bridge these server-side requests to your custom UI logic.

## Summary

- **Standardized**: Connect to any MCP-compliant data source.
- **Native Integration**: MCP tools and prompts map directly to Loom types.
- **Secure**: Built-in support for OAuth2 and Enterprise OIDC.
- **Flexible**: Manage multiple servers via `MultiClient`.
