// Package loomantigravity provides an [llm.Provider] implementation that connects
// directly to Google Cloud Code / Antigravity streaming endpoints.
//
// # Features
//
//   - Authentication: [oauth2.TokenSource] integration supporting Loom's [auth.PersistentTokenSource],
//     automated OAuth 2.0 PKCE loopback login via [auth.LoginLoopback], helper configuration with [NewOAuthConfig],
//     and static or environment-sourced tokens.
//   - Dynamic Model Discovery: Discovers available models directly from Google's endpoint without static catalogs.
//   - Model Aliasing & Heuristic Resolution: Automatically maps canonical names (e.g. "gemini-3.8-flash")
//     and effort variants (e.g. "gemini-3.8-flash-high") to gateway endpoints like "gemini-3.8-flash-tiered".
//   - Reasoning & Thinking Effort: Configurable thinking budgets and levels ("low", "medium", "high").
//   - Compact vs. Raw Profiles: Clean family-level profiles by default; raw endpoints togglable via [Config.RawProfiles].
//   - Quota Inspection: Implements [llm.QuotaProvider] for real-time or cached rate limit and capacity inspection.
package loomantigravity
