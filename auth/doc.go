// Package auth provides general OAuth 2.0 and token management primitives for Loom.
//
// # Key Features
//
//   - [PersistentTokenSource]: A thread-safe [oauth2.TokenSource] implementation that integrates
//     developer-managed token persistence (loading and saving from disk, database, or secret stores)
//     with automated token refreshing and interactive login fallbacks.
//   - [BeginAuth] & [CompleteAuth]: Two-phase PKCE (RFC 7636) authorization code flow helpers
//     suitable for command-line tools, local developer CLIs, web backends, and background services.
//   - [LoginLoopback]: Local loopback HTTP listener that automates browser-based OAuth 2.0 PKCE login,
//     supporting customizable HTML callback templates, HTTP response handlers, and URL notification callbacks.
//   - [LoadTokenFile] & [SaveTokenFile]: Secure file-based token persistence helpers that enforce
//     safe directory (0700) and file (0600) permissions.
//   - [StaticTokenSource] & [EnvTokenSource]: Lightweight [oauth2.TokenSource] adapters for static bearer
//     tokens or environment variable resolution.
//   - [OpenBrowser]: Cross-platform helper for opening authorization URLs in the user's default browser.
package auth
