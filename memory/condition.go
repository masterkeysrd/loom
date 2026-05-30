package memory

import (
	"context"
	"fmt"
	"math/bits"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
)

type SummarizerTrigger func(ctx context.Context, sCtx SummarizerTriggerContext) (bool, error)

type SummarizerTriggerContext struct {
	Messages     []message.Message
	TotalTokens  int
	ContextLimit int
}

func TriggerSummaryOnTokenCount(threshold int) SummarizerTrigger {
	return func(ctx context.Context, sCtx SummarizerTriggerContext) (bool, error) {
		triggered, ok := usageThresholdReached(sCtx.Messages, threshold)
		if ok {
			return triggered, nil
		}
		return sCtx.TotalTokens >= threshold, nil
	}
}

func TriggerSummaryOnMessageCount(threshold int) SummarizerTrigger {
	return func(ctx context.Context, sCtx SummarizerTriggerContext) (bool, error) {
		return len(sCtx.Messages) >= threshold, nil
	}
}

func TriggerSummaryOnFraction(ratio float64) SummarizerTrigger {
	return func(ctx context.Context, sCtx SummarizerTriggerContext) (bool, error) {
		if sCtx.ContextLimit <= 0 {
			return false, nil
		}

		threshold := int(ratio * float64(sCtx.ContextLimit))
		triggered, ok := usageThresholdReached(sCtx.Messages, threshold)
		if ok {
			return triggered, nil
		}
		return sCtx.TotalTokens >= threshold, nil
	}
}

func usageThresholdReached(messages []message.Message, threshold int) (bool, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(*message.Assistant)
		if !ok || msg.Metrics == nil {
			continue
		}
		return msg.Metrics.TotalTokens >= threshold, true
	}
	return false, false
}

type SummarizerKeepFunc func(ctx context.Context, keepCtx SummarizerKeepContext) (int, error)

type SummarizerKeepContext struct {
	Messages      message.MessageList
	TokenCounter  llm.TokenCounter
	ApproxCounter llm.TokenCounter
}

func KeepMessageCount(threshold int) SummarizerKeepFunc {
	return func(ctx context.Context, keepCtx SummarizerKeepContext) (int, error) {
		return findSafeCutoff(keepCtx.Messages, threshold), nil
	}
}

func KeepTokenCount(threshold int) SummarizerKeepFunc {
	return func(ctx context.Context, keepCtx SummarizerKeepContext) (int, error) {
		return findTokenBasedCutoff(ctx, keepCtx.Messages, keepCtx.TokenCounter, keepCtx.ApproxCounter, threshold)
	}
}

func KeepFraction(ratio float64) SummarizerKeepFunc {
	return func(ctx context.Context, keepCtx SummarizerKeepContext) (int, error) {
		if keepCtx.TokenCounter == nil {
			return 0, fmt.Errorf("token counter is required for fraction-based cutoff")
		}

		totalTokens, err := keepCtx.TokenCounter.CountTokens(ctx, keepCtx.Messages)
		if err != nil {
			return 0, fmt.Errorf("failed to count tokens: %w", err)
		}

		targetTokens := int(ratio * float64(totalTokens))
		return findTokenBasedCutoff(ctx, keepCtx.Messages, keepCtx.TokenCounter, keepCtx.ApproxCounter, targetTokens)
	}
}

func findSafeCutoff(msgs message.MessageList, cutoff int) int {
	if len(msgs) <= cutoff {
		return 0
	}

	targetCutoff := len(msgs) - cutoff
	return findSafeCutoffPoint(msgs, targetCutoff)
}

// findSafeCutoffPoint finds a safe cutoff point that doesn't split Assistant/Tool messages pairs.
//
// If the `cutoffIndex` is a `message.Tool` search backwards for the nearest `message.Assistant` containing
// the corresponding tool call and ajust the cutoff to include it. This ensures tool calls
// request and responses stays together.
//
// Fall back to advancing forward past `message.Tool` if not matching `message.Assistant` (edge case).
func findSafeCutoffPoint(msgs message.MessageList, cutoffIndex int) int {
	if cutoffIndex >= len(msgs) || msgs[cutoffIndex].Role() != message.RoleTool {
		return cutoffIndex
	}

	toolCallsIDs := make(map[string]struct{})
	idx := cutoffIndex
	for idx < len(msgs) && msgs[idx].Role() == message.RoleTool {
		if msg, ok := msgs[idx].(*message.Tool); ok && msg.ToolCallID != "" {
			toolCallsIDs[msg.ToolCallID] = struct{}{}
		}
		idx++
	}

	// Search backwards for matching Assistant message containing the tool calls
	for i := cutoffIndex - 1; i >= 0; i-- {
		msg, ok := msgs[i].(*message.Assistant)
		if !ok {
			continue
		}

		for _, toolCall := range msg.ToolCalls() {
			if _, exists := toolCallsIDs[toolCall.ID]; exists {
				// Found the assistant message containing a tool call that would be cut, adjust cutoff to include it
				return i
			}
		}
	}

	// No matching Assistant message found, advance cutoff past the Tool messages
	return idx
}

func findTokenBasedCutoff(ctx context.Context, msgs message.MessageList, tokenCounter, partialTokenCounter llm.TokenCounter, targetCount int) (int, error) {
	if targetCount <= 0 {
		return 1, nil
	}

	count, err := tokenCounter.CountTokens(ctx, msgs)
	if err != nil {
		return 0, fmt.Errorf("failed to count tokens: %w", err)
	}

	if count <= targetCount {
		return 0, nil
	}

	left, right := 0, len(msgs)
	candiate := len(msgs)
	maxIterations := bits.Len(uint(len(msgs))) + 1 // log2(n) + 1 to ensure we cover all possibilities
	for range maxIterations {
		if left >= right {
			break
		}

		mid := (left + right) / 2
		partialCount, err := partialTokenCounter.CountTokens(ctx, msgs[mid:])
		if err != nil {
			return 0, fmt.Errorf("failed to count tokens for cutoff candidate: %w", err)
		}

		if partialCount <= targetCount {
			candiate = mid
			right = mid
		} else {
			left = mid + 1
		}
	}

	if candiate == len(msgs) {
		candiate = left
	}
	if candiate >= len(msgs) {
		if len(msgs) == 1 {
			return 0, nil
		}
		candiate = len(msgs) - 1
	}

	return findSafeCutoffPoint(msgs, candiate), nil
}
