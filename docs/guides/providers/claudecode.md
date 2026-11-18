# Claude Code Provider 🔌

The Claude Code provider (`providers/claudecode`) implements [`llm.Provider`](../../../llm/provider.go) on top of the official `claude` CLI (`stream-json` + `control` protocol) using an **in-process mini-proxy**.

It lets you run standard Loom graphs (`Agent -> Tools -> Agent` loops, `model.BindTools`, checkpoints, compaction, and retries) backed by your local Claude Code installation and authentication (Claude Pro/Max subscription, `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY`, or custom Anthropic-compatible endpoints such as Ollama).

## 1. Prerequisites & Authentication

Ensure the `claude` CLI is installed and authenticated:

```bash
claude auth login
# or set one of:
export CLAUDE_CODE_OAUTH_TOKEN='...'
export ANTHROPIC_API_KEY='...'
```

### `CLAUDE_CONFIG_DIR` & Multi-Account Support

- **Ephemeral isolated mode (default when `ConfigDir` is empty):** Creates a temporary `CLAUDE_CONFIG_DIR` (removed on `Provider.Close()`) and syncs file-based `.credentials.json` from `~/.claude` when present. Ideal on Linux/CI or when using `CLAUDE_CODE_OAUTH_TOKEN` / `ANTHROPIC_API_KEY`.
- **macOS Keychain & Multi-account directories:** On macOS, `claude auth login` stores OAuth credentials in the macOS Keychain keyed by `CLAUDE_CONFIG_DIR`. To use an existing login or run multiple accounts concurrently (e.g., personal + work), pass `WithConfigDir` explicitly per `Provider`:

```go
personal, _ := loomclaudecode.NewDefaultProvider(
    loomclaudecode.WithConfigDir(filepath.Join(home, ".claude")),
)
work, _ := loomclaudecode.NewDefaultProvider(
    loomclaudecode.WithConfigDir(filepath.Join(home, ".claude-work")),
)
```

## 2. Basic Usage

```go
import (
    "github.com/masterkeysrd/loom/llm"
    loomclaudecode "github.com/masterkeysrd/loom/providers/claudecode"
)

provider, err := loomclaudecode.NewDefaultProvider()
if err != nil {
    // handle error
}
defer provider.Close()

model, err := llm.NewModel(provider, "sonnet", nil)
if err != nil {
    // handle error
}
```

Model profiles are derived dynamically from [`providers/anthropic`](anthropic.md)'s `loomgen` catalog alongside Claude Code aliases (`sonnet`, `opus`, `haiku`, `default`). Custom model IDs (e.g., Ollama models) are also accepted by `llm.NewModel`.

Always call `defer provider.Close()` so warm or parked CLI subprocesses and any temporary config directories are cleaned up when your application exits.

## 3. How Loops, Retries, Compaction & Caching Work

The in-process mini-proxy tags `Assistant` responses with `claudecode.session_id`, `claudecode.turn_token`, and `claudecode.prefix_digest`, and routes each `Stream()` call down one of three paths:

1. **Path A (Live Tip Match):**
   - In Loom `Agent -> Tools -> Agent` loops, `llm.Request.Tools` are advertised over the CLI's in-process SDK MCP `control` channel (`--mcp-config` `type: "sdk"`, with built-in CLI tools disabled by default via `--tools ""`).
   - When the model invokes a tool, Loom receives `*message.ToolCall`, executes your Go tool in the `Tools` node, and the proxy delivers the resulting `*message.Tool` messages back to the parked CLI process as `mcp_response` frames—preserving Anthropic prompt caching with zero process respawn.
2. **Path B (Snapshot Fork):**
   - Clean `.jsonl` session transcripts are snapshotted per turn inside `CLAUDE_CONFIG_DIR`.
   - On `graph.RetryMiddleware` retries or parallel graph branches from an earlier checkpoint, the proxy clones the snapshot to a forked session and resumes via `--resume`, preserving the prompt-cache prefix without mutating the original session.
3. **Path C (Fresh Session Fallback):**
   - On first turns, Loom-side conversation compaction (detected via `prefix_digest` mismatch), history edits, or cold checkpoint restores across process restarts, a fresh CLI session is started from `request.Messages`.

## 4. Permissions

Permissions depend on which tools are enabled:

- **Loom-bound tools (`model.BindTools(...)`, default):** Built-in Claude Code tools are disabled (`--tools ""`). The CLI's `can_use_tool` control checks auto-allow `mcp__loom__*` only so the CLI forwards `tools/call` up to Loom as `*message.ToolCall`. All actual execution, allowlists, HITL interrupts, and sandboxing happen inside your Loom `Tools` node—identically to other providers.
- **Native Claude Code tools (`WithAllowedNativeTools` / `Session.NativeTools`):** Built-in tools enabled via `WithAllowedNativeTools("Read", "Grep")` run inside the CLI subprocess and are gated by `WithPermissionMode(...)` (defaults to `"bypassPermissions"` for headless loops).

## 5. Configuration, Compaction & Sync-on-Write

### Provider Options

```go
provider, err := loomclaudecode.NewDefaultProvider(
    loomclaudecode.WithCLIPath("/usr/local/bin/claude"),
    loomclaudecode.WithConfigDir("/path/to/claude-config"),
    loomclaudecode.WithWorkingDir("/path/to/repo"),
    loomclaudecode.WithIdleTTL(10 * time.Minute),
    loomclaudecode.WithAllowedNativeTools("Read", "Grep"),
    loomclaudecode.WithTraceWriter(os.Stderr), // debug raw stdin/stdout/stderr frames
)
```

### Per-Request Extension (`claudecode.Session`)

```go
model = model.WithExtension(loomclaudecode.Session{
    WorkingDir:  "/path/to/workspace",
    NativeTools: []string{"Read", "Bash"},
    ForceFresh:  false,
})

// Trigger native Claude Code compaction (`/compact`) for a single turn:
resp, err := model.Invoke(ctx, msgs, llm.WithExtensionOption(loomclaudecode.Session{
    Compact:             true,
    CompactInstructions: "Preserve architecture decisions and open TODOs",
}))
```

When Claude Code compacts (either automatically via `auto` or manually via `Session.Compact`), the provider emits `on_llm_compacting` and `on_llm_compacted` events to `stream.WriterFromContext(ctx)` and tags `Assistant.Metadata` with `claudecode.compacted`, `claudecode.compact_trigger`, and `claudecode.compact_pre_tokens`.

### Idle Notifications (`llm.IdleMessageNotifier`) & Sync-on-Write (`*llm.OutOfSyncError`)

To receive unsolicited events emitted while a CLI session is idle between turns (such as background compaction or unexpected exits) as standard `message.Message` values (carrying `IdleEventExtension` and `SyncCheckpointExtension`):

```go
if notifier, ok := provider.(llm.IdleMessageNotifier); ok {
    unsub := notifier.OnIdleMessage(func(msg message.Message) {
        // Offload blocking I/O (e.g. webhooks) to your own goroutine/channel.
    })
    defer unsub()
}
```

Enable `WithSyncOnWrite(true)` if you want `Stream()` to reject writes with `*llm.OutOfSyncError` (carrying `Messages message.MessageList`) whenever an idle compaction or out-of-band history edit diverges from the live CLI session, letting your graph rearrange `state.Messages` and resubmit on the same warm session:

```go
provider, err := loomclaudecode.NewDefaultProvider(
    loomclaudecode.WithSyncOnWrite(true),
)

resp, err := model.Invoke(ctx, state.Messages)
var syncErr *llm.OutOfSyncError
if errors.As(err, &syncErr) && len(syncErr.Messages) > 0 {
    state.Messages = append(slices.Clone(syncErr.Messages), pendingUserMsgs...)
    resp, err = model.Invoke(ctx, state.Messages)
}
```

### Quota Inspection (`llm.QuotaProvider`)

When running with a `claude.ai` subscription credential, `Provider` implements [`llm.QuotaProvider`](../llm-package.md#6-quota--rate-limit-inspection-llmquotaprovider) by querying Claude Code's `/usage` slash command and decoding `usage_report.rate_limits` (`limits[]` rolling windows and `extra_usage` credit pools):

```go
if qp, ok := provider.(llm.QuotaProvider); ok {
    snap, err := qp.GetQuotaSnapshot(ctx) // cached 30s by default (WithQuotaCacheTTL)
    _ = snap
    _ = err
}
```

Unauthenticated sessions or plain `ANTHROPIC_API_KEY` environments return an empty snapshot without error. Use `go run ./examples/claudecode --quota` to inspect active pools from the CLI.

## 6. Running the Example & Local Endpoints (e.g. Ollama)

See [`examples/claudecode`](../../../examples/claudecode/main.go) for a streaming interactive REPL + `Agent -> Tools -> Agent` demo:

```bash
# With your local Claude Code login (~/.claude):
go run ./examples/claudecode --model sonnet

# Inspect subscription rate limits and extra usage via /usage:
go run ./examples/claudecode --quota

# With a separate account config directory:
go run ./examples/claudecode --config-dir ~/.claude-work --model sonnet

# With a local Anthropic-compatible endpoint (e.g. Ollama) and protocol trace:
OLLAMA_CONTEXT_LENGTH=65536 ollama serve
go run ./examples/claudecode --debug --endpoint http://localhost:11434 --model qwen3-coder
```

## 7. Limitations

- Sampling parameters not exposed by the `claude` CLI (`Temperature`, `TopP`, `TopK`, `PresencePenalty`, `FrequencyPenalty`, `Stop`, `ContextWindow`) are ignored; `Thinking.Effort` maps to `--effort <level>` (`low`, `medium`, `high`, `xhigh`, `max`).
- Multimodal media blocks (`ImageBlock`, `AudioBlock`, `VideoBlock`, `DocumentBlock`) are not transported over the `claude` CLI `stream-json` prompt/MCP bridge and fail fast with `profile.ErrUnsupportedModality` instead of being dropped; use [`providers/anthropic`](anthropic.md) for multimodal image/PDF inputs.
- Model swaps and permission-mode changes on sequential warm turns reuse the live session via `control_request` (`set_model`, `set_permission_mode`), and context cancellation issues an `interrupt` control request before shutdown.
- `ListModels` / `GetModel` discover models dynamically from the CLI's `initialize` handshake (matching `providers/copilot`, so `ListProfiles` / `GetProfile` return cached discovered models + overrides without spawning eagerly) and enrich matched IDs with limits, pricing, and modalities from `providers/anthropic`.
- When using a Claude Pro/Max subscription (`CLAUDE_CODE_OAUTH_TOKEN` or local login), reported dollar estimates are informational; token usage (`Input`, `Output`, `CacheRead`, `CacheWrite`) is still reported in Loom telemetry and Studio.
- If you have an `ANTHROPIC_API_KEY` and do not need the `claude` CLI harness or subscription auth, [`providers/anthropic`](anthropic.md) remains the recommended choice for lowest latency and full parameter control.
