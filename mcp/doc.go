// Package mcp provides a client for the Model Context Protocol (MCP).
//
// It supports MCP v2 (protocol version 2026-07-28) stateless discovery
// (server/discover) with automatic fallback to legacy stateful MCP v1
// (2025-11-25) initialize, multiple transport modes (stdio, Streamable HTTP),
// and authentication methods (OAuth2 Authorization Code, Client Credentials,
// Enterprise Managed Authorization). The package exposes a single Client for
// individual server connections and a MultiClient for managing multiple
// servers simultaneously.
//
// Key features:
//
//   - MCP v2 (2026-07-28) stateless discovery (server/discover) and sessionless operation
//   - Streaming subscriptions via subscriptions/listen
//   - Automatic multi-round-trip request handling and elicitation (SEP-2322)
//   - Client-side TTL caching support (SEP-2549)
//   - Tool execution with streaming, progress reporting, structured content, and schema validation
//   - Tool safety annotations and output schema mapping
//   - Prompt and resource retrieval
//   - Autocomplete (completion) support
//   - Dynamic authentication via OAuth2 (authorization code, client credentials) and enterprise flows
//   - Interactive elicitation handling (Form and URL)
//
// # Configuration
//
// Each server is configured via a Config struct specifying the transport
// type, command (for stdio), URL (for HTTP), optional protocol version,
// and optional auth provider.
//
// # Authentication
//
// The package includes built-in providers for OAuth2 and Enterprise flows:
//   - OAuth2Provider handles standard authorization code flows with a local redirect server.
//   - ClientCredentialsProvider handles OAuth2 machine-to-machine (M2M) flows.
//   - EnterpriseProvider supports OIDC login with managed authorization servers.
//
// # Multi-Client
//
// MultiClient manages a map of named MCP servers, providing a unified
// interface for tools, prompts, resources, and configuration across all
// connected servers.
package mcp
