// Package loomantigravity provides an [llm.Provider] implementation that connects
// directly to Google Cloud Code / Antigravity streaming endpoints.
//
// # Features
//
//   - Authentication: OAuth 2.0 PKCE flow with automated token persistence and background refresh,
//     environment variable fallbacks (ANTIGRAVITY_TOKEN, GOOGLE_ACCESS_TOKEN), and static bearer tokens.
//   - Dynamic Model Discovery: Discovers available models directly from Google's endpoint without static catalogs.
//   - Model Aliasing & Heuristic Resolution: Automatically maps canonical names (e.g. "gemini-3.8-flash")
//     and effort variants (e.g. "gemini-3.8-flash-high") to gateway endpoints like "gemini-3.8-flash-tiered".
//   - Reasoning & Thinking Effort: Configurable thinking budgets and levels ("low", "medium", "high").
//   - Compact vs. Raw Profiles: Clean family-level profiles by default; raw endpoints togglable via [Config.RawProfiles].
//   - Quota Inspection: Implements [llm.QuotaProvider] for real-time or cached rate limit and capacity inspection.
package loomantigravity
