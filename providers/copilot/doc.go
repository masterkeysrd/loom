// Package loomcopilot provides an [llm.Provider] implementation backed by the
// official GitHub Copilot Go SDK (`github.com/microsoft/copilot-sdk/go/v2`).
//
// # Architecture
//
// The provider keeps Loom in control of the outer graph loop, state,
// checkpoints, and tool execution (`Agent -> Tools -> Agent`), while managing
// a Copilot SDK [copilot.Client] and per-conversation [copilot.Session]
// instances:
//
//   - Path A (Live Tip Match): Reuses warm/parked SDK sessions across
//     sequential turns and `Agent -> Tools -> Agent` hops. Loom's
//     [llm.Request.Tools] are registered as custom [copilot.Tool] definitions
//     whose handlers park in-flight and yield `*message.ToolCall` back to Loom,
//     resuming cleanly when Loom delivers `*message.Tool` results.
//   - Path C (Fresh Fallback): On first turns, Loom-side history compaction,
//     retries/graph forks, or cold checkpoint restores, creates a fresh SDK
//     session seeded from [llm.Request.Messages]. (The Copilot SDK does not
//     currently expose a non-mutating session fork primitive; see `copilot.md`.)
//
// # Optional Interfaces
//
// In addition to [llm.Provider] and [profile.Provider], [Provider] implements:
//   - [llm.ModelLister]: queries `client.ListModels` (or custom `OnListModels`).
//   - [llm.QuotaProvider]: queries `client.RPC.Account.GetQuota`.
//   - [llm.IdleMessageNotifier]: emits idle compaction, truncation, and
//     session shutdown notifications as [message.Message] values, paired with
//     `WithSyncOnWrite(true)` and `*llm.OutOfSyncError` reconciliation.
package loomcopilot
