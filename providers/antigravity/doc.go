// Package loomantigravity provides an [llm.Provider] implementation that connects
// directly to Google Cloud Code / Antigravity streaming endpoints.
//
// # Features
//
//   - Authentication: [oauth2.TokenSource] integration supporting Loom's [auth.PersistentTokenSource],
//     automated OAuth 2.0 PKCE loopback login via [auth.LoginLoopback], helper configuration with [NewOAuthConfig],
//     and static or environment-sourced tokens.
//   - Companion Project Discovery: Automatically resolves `cloudaicompanionProject` once per token via
//     `/v1internal:loadCodeAssist` (configurable via [Config.CompanionProject]), keeping it separate from
//     an explicit user billing project (`X-Goog-User-Project` via [Config.ProjectID]).
//   - Dynamic Model Discovery: Discovers available models directly from Google's endpoint without static catalogs,
//     emitting a versioned CLI `User-Agent` (overridable via `ANTIGRAVITY_USER_AGENT` or [Config.UserAgent]).
//   - Model Aliasing & Heuristic Resolution: Automatically maps canonical names (e.g. "gemini-3.8-flash",
//     "gemini-4-carbon") and effort variants across both `-tiered` gateway endpoints and dedicated per-effort
//     endpoints (`-low`, `-medium`, `-high`).
//   - Reasoning & Thinking Effort: Configurable thinking budgets and levels ("LOW", "MEDIUM", "HIGH").
//   - Session Affinity & Prompt Correlation: [Session] extension (`antigravity.session`) for sticky replica
//     KV-cache routing (`request.session_id`) and outer-envelope correlation (`user_prompt_id`).
//   - Compact vs. Raw Profiles: Clean family-level profiles by default; raw endpoints togglable via [Config.RawProfiles].
//   - Quota Inspection: Implements [llm.QuotaProvider] for real-time or cached rate limit and capacity inspection.
package loomantigravity
