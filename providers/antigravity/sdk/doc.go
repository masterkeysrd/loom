// Package sdk provides a low-level Go client and raw wire protocol types for
// Google Cloud Code / Antigravity internal HTTP endpoints
// (`/v1internal:loadCodeAssist`, `/v1internal:fetchAvailableModels`,
// `/v1internal:retrieveUserQuotaSummary`, `/v1internal:streamGenerateContent`,
// and `/v1internal:countTokens`).
//
// This package intentionally contains no Loom-specific abstractions, model
// compaction, alias resolution, or profile mapping; see parent package
// `github.com/masterkeysrd/loom/providers/antigravity` for the Loom [llm.Provider].
package sdk
