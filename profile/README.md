# profile

The `profile` package defines provider-agnostic model catalog metadata, capabilities, context/dimension limits, modalities, and tiered token pricing shared across `llm`, `decision`, `embedding`, and `providers/*`.

---

## Core Types

| Type | Description |
|---|---|
| `Profile` | Complete metadata for a model (`ID`, `Name`, `Family`, `Capabilities`, `Limits`, `Modalities`, `Pricing`). |
| `Provider` | Optional interface (`GetProfile(id string) (Profile, bool)`) implemented by model providers. |
| `Capabilities` | Supported workloads and features (`Completion`, `Decision`, `Embedding`, `Attachment`, `Reasoning`, `ToolCall`, `Temperature`). |
| `Limits` | Token and vector limits (`Context`, `Output`, `Dimensions`). |
| `Modalities` | Supported input and output modalities (`text`, `image`, `audio`, `video`, `pdf`, `embedding`). |
| `Pricing` / `TierPricing` | Per-million-token pricing and context-window tier overrides. |
| `UnsupportedModalityError` | Typed error (wrapping `ErrUnsupportedModality`) returned when a model profile or provider wire format rejects an input modality or source format. |
| `UnsupportedCapabilityError` | Typed error (wrapping `ErrUnsupportedCapability`) returned when a request uses a capability (`tool_call`, `completion`, `embedding`, `decision`) disabled on a known model profile. |

---

## Modality & Capability Validation

All built-in Loom providers (`openai`, `anthropic`, `genai`, `ollama`, `copilot`, `claudecode`, `antigravity`, and `typesafe`) enforce strict media validation using `profile`:

```go
import (
    "errors"

    "github.com/masterkeysrd/loom/profile"
)

if prof, ok := provider.GetProfile(req.Model); ok {
    if err := prof.ValidateMessagesModalities(provider.Name(), req.Model, req.Messages); err != nil {
        // errors.Is(err, profile.ErrUnsupportedModality) == true
        return err
    }
    if err := prof.ValidateToolCallCapability(provider.Name(), req.Model, len(req.Tools) > 0); err != nil {
        // errors.Is(err, profile.ErrUnsupportedCapability) == true
        return err
    }
}
```

When a model is unknown/unlisted (`GetProfile` returns `false` or `Modalities.Inputs` is empty), profile checks pass through and the provider's wire-format mapper still rejects any unrepresentable media blocks rather than silently dropping them.

---

## Cost Estimation & Catalog Search

```go
import (
    "github.com/masterkeysrd/loom/message"
    "github.com/masterkeysrd/loom/profile"
)

// Search a slice of profiles by ID or Name (case-insensitive)
matches := profile.Search(profiles, "embedding")

// Merge live API metadata onto an authoritative static base profile without overwriting pricing or limits
merged := profile.Merge(staticProfile, liveProfile)

// Estimate token cost in nano-dollars (automatically applying tiered pricing if applicable)
est := merged.Estimate(message.TokenDetails{
    Input:  1500,
    Output: 500,
})
```
