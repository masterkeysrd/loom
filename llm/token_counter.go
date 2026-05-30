package llm

import (
	"context"
	"strings"

	"github.com/masterkeysrd/loom/message"
)

// TokenCounter estimates the number of tokens consumed by a message list.
// Implementations may use exact model-specific tokenizers or cheap
// approximations. The counter is used by components such as [memory.Summarizer]
// to decide when to compress a conversation history.
type TokenCounter interface {
	CountTokens(ctx context.Context, messages message.MessageList) (int, error)
}

// ApproximateTokenCounter estimates token counts using the common
// "4 characters ≈ 1 token" heuristic. It is accurate enough for triggering
// summarization thresholds without requiring a live model call or an external
// tokenizer library.
type ApproximateTokenCounter struct{}

// CountTokens sums an approximate token count across all text and thinking
// blocks in messages. Tool-call argument maps are not counted because they
// are typically small and structurally variable.
func (c ApproximateTokenCounter) CountTokens(_ context.Context, messages message.MessageList) (int, error) {
	var total int
	for _, msg := range messages {
		for _, block := range msg.GetContent() {
			switch v := block.(type) {
			case *message.TextBlock:
				total += approxTokens(v.Text)
			case *message.ThinkingBlock:
				total += approxTokens(v.Thinking)
			}
		}
	}
	return total, nil
}

// approxTokens returns an approximate token count for s using the
// 4-characters-per-token rule. It never returns less than 1 for non-empty input.
func approxTokens(s string) int {
	n := len(strings.TrimSpace(s))
	if n == 0 {
		return 0
	}
	if t := n / 4; t > 0 {
		return t
	}
	return 1
}
