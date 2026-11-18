# GitHub Copilot Provider 🐙

The GitHub Copilot provider (`providers/copilot`) implements [`llm.Provider`](../../../llm/provider.go) on top of the official GitHub Copilot Go SDK (`github.com/github/copilot-sdk/go`), adding dynamic model discovery (`llm.ModelLister`), quota inspection (`llm.QuotaProvider`), and idle notifications / sync-on-write (`llm.IdleMessageNotifier`, `*llm.OutOfSyncError`).

It lets you run standard Loom graphs (`Agent -> Tools -> Agent` loops, `model.BindTools`, checkpoints, structured output, thinking effort, and compaction) backed by your GitHub Copilot subscription or a BYOK endpoint (OpenAI, Azure, Anthropic, or Ollama).

> [!NOTE]
> For a concise breakdown of Copilot SDK caveats, parameter limitations, and deviations from `providers/claudecode`, see [`copilot.md`](../../../copilot.md) at the repository root.

## 1. Prerequisites & Authentication

Ensure the `copilot` CLI is installed (on `PATH`, via `COPILOT_CLI_PATH`, or passed with `WithCLIPath`) and authenticated:

```bash
copilot login
# or set a fine-grained PAT / OAuth token:
export COPILOT_GITHUB_TOKEN='...'
```

> [!IMPORTANT]
> GitHub Copilot **does not support classic Personal Access Tokens (`ghp_...`)**. If `GITHUB_TOKEN` contains a classic `ghp_` token, either unset it before running `copilot login` / `go run ./examples/copilot` or pass a fine-grained PAT (`github_pat_...` with *Copilot Requests* permission) or `COPILOT_GITHUB_TOKEN`.

When `WithBaseDirectory` is unset, `providers/copilot` leaves `BaseDirectory` empty so the Copilot SDK uses its standard `~/.copilot` directory. Pass `WithBaseDirectory(...)` explicitly if you want isolated session/config storage.

## 2. Basic Usage

```go
import (
    "github.com/masterkeysrd/loom/llm"
    loomcopilot "github.com/masterkeysrd/loom/providers/copilot"
)

provider, err := loomcopilot.NewDefaultProvider()
if err != nil {
    // handle error
}
defer provider.Close()

model, err := llm.NewModel(provider, "auto", nil)
if err != nil {
    // handle error
}
```

Model profiles are populated dynamically via `provider.ListModels(ctx)` (`models.list` or custom `WithOnListModels`) with `OverrideProfile` support. Discovered model IDs that match generated `models.dev` metadata (`providers/copilot/profiles.gen.go`, refreshed via `go generate ./providers/copilot`) are automatically enriched with family, release dates, descriptions, input modalities (`PDF`/`Audio`/`Video`), attachment support, USD reference pricing/tiers, and fallback context/reasoning metadata—while keeping live `models.list` limits/efforts authoritative, enforcing `Capabilities.Temperature = false`, and never exposing unadvertised static catalog entries in `ListProfiles()` or `GetProfile()`. Always call `defer provider.Close()` so active/parked sessions and the SDK runtime stop cleanly.

## 3. How Loops, Retries & History Work

`providers/copilot` tags `Assistant` responses with `copilot.session_id`, `copilot.turn_token`, `copilot.prefix_digest`, and `copilot.event_id`, and routes each `Stream()` call down one of two paths:

1. **Path A (Live Tip Match):**
   - In Loom `Agent -> Tools -> Agent` loops, `llm.Request.Tools` are registered as custom `copilot.Tool` definitions (`SkipPermission: true`, with built-in CLI tools disabled by default).
   - When the model invokes a Loom tool, the SDK tool handler parks in-flight while `Stream()` yields `*message.ToolCall` to Loom. Once Loom's `Tools` node executes your Go tool and sends `*message.Tool` back, Path A unblocks the parked handler with `copilot.ToolResult` without recreating the session.
   - Sequential user turns reuse the same warm `*copilot.Session`.
2. **Path C (Fresh Session Fallback):**
   - On first turns, Loom-side history edits or compaction (`prefix_digest` mismatch), `RetryMiddleware` retries / parallel graph branches, or cold checkpoint restores across restarts, a fresh `*copilot.Session` is created from `request.Messages`. *(The Copilot SDK does not currently expose a non-mutating session fork primitive.)*

## 4. Permissions

- **Loom-bound tools (`model.BindTools(...)`, default):** Built-in Copilot CLI tools (`builtin:*`) and built-in subagents are disabled by default so Loom owns tool execution and permissions in its `Tools` node.
- **Runtime permission requests:** To prevent pending-permission deadlocks, `providers/copilot` always installs an `OnPermissionRequest` callback:
  - Custom `WithPermissionHandler(fn)` if provided;
  - `copilot.PermissionHandler.ApproveAll` if `WithAllowAllPermissions(true)` is set;
  - Otherwise an explicit rejection (`*rpc.PermissionDecisionReject`).
- **Native Copilot tools (`WithAllowedNativeTools` / `Session.NativeTools`):** Enable specific built-in tools such as `"view"` or `"grep"` alongside a `WithPermissionHandler` or `WithAllowAllPermissions(true)`. Native tools execute inside the Copilot runtime turn rather than yielding `*message.ToolCall` to Loom.

## 5. Configuration, Compaction, Quotas & BYOK

### Provider Options

```go
provider, err := loomcopilot.NewDefaultProvider(
    loomcopilot.WithCLIPath("/opt/homebrew/bin/copilot"),
    loomcopilot.WithBaseDirectory("/path/to/copilot-home"),
    loomcopilot.WithWorkingDir("/path/to/repo"),
    loomcopilot.WithSystemMessageMode("replace"), // or "append"
    loomcopilot.WithIdleTTL(10 * time.Minute),
    loomcopilot.WithTraceWriter(os.Stderr), // structured SDK session trace lines
)
```

### Per-Request Extension (`copilot.Session`) & Compaction

```go
model = model.WithExtension(loomcopilot.Session{
    WorkingDir:  "/path/to/workspace",
    NativeTools: []string{"view", "grep"},
    ForceFresh:  false,
})

// Trigger Copilot's native `session.history.compact` RPC for a single turn:
resp, err := model.Invoke(ctx, msgs, llm.WithExtensionOption(loomcopilot.Session{
    Compact:             true,
    CompactInstructions: "Preserve architecture decisions and open TODOs",
}))
```

Manual or background compaction emits `on_llm_compacting` and `on_llm_compacted` events to `stream.WriterFromContext(ctx)` and tags `Assistant.Metadata` with `copilot.compacted`, `copilot.compact_trigger`, `copilot.compact_tokens_removed`, and `copilot.compact_messages_removed`.

### Idle Notifications (`llm.IdleMessageNotifier`) & Sync-on-Write (`*llm.OutOfSyncError`)

```go
provider, err := loomcopilot.NewDefaultProvider(
    loomcopilot.WithSyncOnWrite(true),
    loomcopilot.WithOnIdleMessage(func(msg message.Message) {
        // Offload blocking I/O (e.g. webhooks) to your own goroutine/channel.
    }),
)
```

When `WithSyncOnWrite(true)` is enabled, idle background compaction/truncation or session shutdown causes the next stale write to fail with `*llm.OutOfSyncError` (carrying reconciled `syncErr.Messages`), matching `providers/claudecode`.

### Quota Inspection & Credit Consumption (`llm.QuotaProvider`, `TokenMetrics.Credits`)

```go
snap, err := provider.GetQuotaSnapshot(ctx) // "premium_interactions", "chat", "completions"
```

When Copilot reports `AssistantUsageData`, `chunk.Metrics.Credits` (`message.CreditMetrics`) is populated with server-reported `ai_credits` (`TotalNanoAiu / 1e9`) or `premium_requests` fallback, along with `Multiplier` (`Cost`) and `Status` (`AiCreditsStatus`).

### BYOK / Local Endpoints (e.g. Ollama)

```go
provider, err := loomcopilot.NewDefaultProvider(
    loomcopilot.WithBYOKProvider(&copilot.ProviderConfig{
        Type:    "openai",
        BaseURL: "http://localhost:11434/v1",
    }),
)
```

## 6. Running the Example

See [`examples/copilot`](../../../examples/copilot/main.go) for a streaming interactive REPL + `Agent -> Tools -> Agent` demo:

```bash
# Using your local `copilot login` session (`auto` model):
go run ./examples/copilot

# Using a specific Copilot model:
go run ./examples/copilot --model gpt-5.4

# Inspect account quota buckets:
go run ./examples/copilot --quota

# Using a local OpenAI-compatible BYOK endpoint (e.g. Ollama) with trace logs:
go run ./examples/copilot --debug --endpoint http://localhost:11434/v1 --model qwen3-coder
```

## 7. Limitations

- Sampling and context parameters not exposed by the Copilot SDK (`Temperature`, `TopP`, `TopK`, `PresencePenalty`, `FrequencyPenalty`, `Stop`, `MaxTokens`, `ContextWindow`) are ignored (except BYOK `ProviderConfig.MaxPromptTokens` / `MaxOutputTokens`).
- Inline `ImageBlock` data (`len(Data) > 0`) is supported on the active `User` prompt (via `AttachmentBlob`) and `Tool` results (via `ToolBinaryResult`); URL-only/empty images, historical transcript media, and `AudioBlock`/`VideoBlock`/`DocumentBlock` inputs fail fast with `profile.ErrUnsupportedModality`.
- `ResponseSchema` and `ThinkingConfig` (`ReasoningEffort`) are forwarded when supported.
- Dynamic `ModelInfo` entries report billing multipliers and AI-credit prices rather than USD token pricing; `Profile.Pricing` and long-context tiers are filled from generated `models.dev` reference data (`profiles.gen.go`) when the model ID matches, and remain zero for custom/BYOK or uncatalogued IDs unless overridden via `OverrideProfile`.
