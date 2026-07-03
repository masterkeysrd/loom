// Package mcp provides a client for the Model Context Protocol (MCP).
//
// It supports multiple transport modes (stdio, HTTP) and authentication
// methods (OAuth2, Enterprise Managed Authorization). The package exposes
// a single Client for individual server connections and a MultiClient
// for managing multiple servers simultaneously.
//
// Key features:
//
//   - Tool execution with streaming and structured content
//   - Prompt and resource retrieval
//   - Resource subscription and updates
//   - Autocomplete (completion) support
//   - Dynamic authentication via OAuth2 and enterprise flows
//   - Interactive elicitation handling
//
// # Configuration
//
// Each server is configured via a Config struct specifying the transport
// type, command (for stdio), URL (for HTTP), and optional auth provider.
//
// # Authentication
//
// The package includes built-in providers for OAuth2 and Enterprise flows.
// OAuth2Provider handles standard authorization code flows with a local
// redirect server. EnterpriseProvider supports OIDC login with managed
// authorization servers.
//
// # Multi-Client
//
// MultiClient manages a map of named MCP servers, providing a unified
// interface for tools, prompts, resources, and configuration across all
// connected servers.
package mcp
