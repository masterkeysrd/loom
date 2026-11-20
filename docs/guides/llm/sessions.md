---
title: Manage caches and stateful sessions
---

Some providers can reuse a cached prompt or keep a conversation open between turns. Choose the operation your provider supports.

## Reuse a prompt cache

For providers that support explicit resource management (like Google Gemini's Context Caching), Loom provides a `CacheManager` API.

```go
// 1. Create a long-lived cache from a list of messages
cacheID, err := model.
    WithExtension(loomgenai.CacheCreation{DisplayName: "Project Docs"}).
    CreateCache(ctx, documentMessages)

// 2. Use the cache ID in subsequent calls
resp, _ := model.Invoke(ctx, prompt, llm.WithExtensionOption(loomgenai.ContextCache{ID: cacheID}))

// 3. Delete when no longer needed
model.DeleteCache(ctx, cacheID)
```

## Reconcile conversation history

Stateful providers (such as `providers/claudecode` and `providers/copilot`) can push unsolicited `message.Message` events while idle between turns via [`llm.IdleMessageNotifier`](https://pkg.go.dev/github.com/masterkeysrd/loom/llm#IdleMessageNotifier) and reject out-of-sync writes with [`llm.OutOfSyncError`](https://pkg.go.dev/github.com/masterkeysrd/loom/llm#OutOfSyncError):

```go
if notifier, ok := provider.(llm.IdleMessageNotifier); ok {
    unsub := notifier.OnIdleMessage(func(msg message.Message) {
        // Promptly enqueue or react to idle updates (e.g. background compaction)
    })
    defer unsub()
}

resp, err := model.Invoke(ctx, messages)
var syncErr *llm.OutOfSyncError
if errors.As(err, &syncErr) && len(syncErr.Messages) > 0 {
    // Reconcile local conversation history using syncErr.Messages and resubmit
}
```
