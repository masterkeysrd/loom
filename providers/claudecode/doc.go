// Package loomclaudecode provides an [llm.Provider] implementation backed by
// the official Claude Code CLI (`claude`) using its `stream-json` and
// bidirectional `control` protocol via an in-process mini-proxy.
//
// # Architecture
//
// The provider keeps Loom in control of the outer graph loop, state,
// compaction, checkpoints, and tool execution (`Agent -> Tools -> Agent`),
// while managing `claude` CLI subprocesses in-process:
//
//   - Path A (Live Tip Match): Reuses warm/parked CLI subprocesses across
//     sequential turns and `Agent -> Tools -> Agent` hops by bridging
//     [llm.Request.Tools] over the CLI's in-process MCP `control` protocol
//     (`mcp_response`), preserving Anthropic prompt caching with zero startup cost.
//   - Path B (Snapshot Fork): Tracks per-turn metadata (`claudecode.session_id`
//     and `claudecode.turn_token` including a message/tools prefix digest) and
//     snapshots clean `.jsonl` session transcripts inside an isolated
//     `CLAUDE_CONFIG_DIR`. On `RetryMiddleware` retries or parallel graph
//     forks, the snapshot is cloned and resumed (`--resume`) so the structured
//     prompt-cache prefix stays intact without mutating the original session.
//   - Path C (Fresh Fallback): On first turns, Loom-side history compaction,
//     history edits, or cold checkpoint restores, starts a fresh CLI session
//     seeded from [llm.Request.Messages].
//
// # Limitations
//
// Because requests run through the `claude` CLI rather than Anthropic's raw
// `/v1/messages` API, sampling knobs such as `Temperature`, `TopP`, `TopK`,
// `PresencePenalty`, `FrequencyPenalty`, `Stop`, and `ContextWindow` are not
// forwarded and are ignored unless explicitly noted. When using
// `CLAUDE_CODE_OAUTH_TOKEN` or local subscription login, reported dollar costs
// are informational estimates rather than direct API billing.
package loomclaudecode
