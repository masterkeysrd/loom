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

---

## Cost Estimation & Catalog Search

```go
import (
    "github.com/masterkeysrd/loom/message"
    "github.com/masterkeysrd/loom/profile"
)

// Search a slice of profiles by ID or Name (case-insensitive)
matches := profile.Search(profiles, "embedding")

// Estimate token cost in nano-dollars (automatically applying tiered pricing if applicable)
costDetails, totalCost := prof.EstimateCost(message.TokenDetails{
    Input:  1500,
    Output: 500,
})
```
