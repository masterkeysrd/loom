package message

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// TrimConfig holds configuration for [TrimMessages].
type TrimConfig struct {
	// Strategy controls which end of the list to keep.
	// [TrimStrategyFirst] keeps the oldest messages; [TrimStrategyLast] keeps the newest.
	// Defaults to [TrimStrategyLast] when nil config is provided.
	Strategy TrimStrategy

	// StartOn drops leading messages that don't match any of the given roles
	// after token-based trimming. Only valid with [TrimStrategyLast].
	StartOn []Role

	// EndOn drops trailing messages that don't match any of the given roles.
	// With [TrimStrategyLast] this is applied before trimming; with
	// [TrimStrategyFirst] it is applied after.
	EndOn []Role

	// IncludeSystem preserves a System message at index 0 regardless of the
	// token budget (its tokens are subtracted from the budget for the rest).
	// Only valid with [TrimStrategyLast].
	IncludeSystem bool

	// AllowPartial allows the boundary message to be included with only a
	// subset of its content blocks, or with its text split at a newline, when
	// the full message would exceed the budget.
	AllowPartial bool

	// TextSplitter splits a string into chunks for partial text trimming.
	// Defaults to splitting on newlines when nil.
	TextSplitter func(string) []string

	// CountTokens returns the total token count for a list of messages.
	// Required.
	CountTokens func(context.Context, MessageList) (int, error)
}

// TrimStrategy selects which end of the message history to preserve.
type TrimStrategy string

const (
	// TrimStrategyFirst keeps the oldest (earliest) messages.
	TrimStrategyFirst TrimStrategy = "first"
	// TrimStrategyLast keeps the newest (most recent) messages.
	TrimStrategyLast TrimStrategy = "last"
)

// TrimMessages reduces messages to fit within maxTokens using the configured
// strategy.  It mirrors the behaviour of LangChain's trim_messages utility:
//
//   - TrimStrategyLast: applies EndOn filtering, extracts the System message
//     when IncludeSystem is set, then keeps as many trailing messages as fit.
//   - TrimStrategyFirst: keeps as many leading messages as fit, then applies
//     EndOn filtering.
//
// When AllowPartial is true the boundary message may be included with fewer
// content blocks or with its text truncated using TextSplitter.
func TrimMessages(ctx context.Context, messages MessageList, maxTokens int, config *TrimConfig) (MessageList, error) {
	if config == nil {
		config = &TrimConfig{Strategy: TrimStrategyLast}
	}

	if len(config.StartOn) > 0 && config.Strategy == TrimStrategyFirst {
		return nil, fmt.Errorf("start_on is not compatible with first trim strategy")
	}

	if config.IncludeSystem && config.Strategy == TrimStrategyFirst {
		return nil, fmt.Errorf("include_system is not compatible with first trim strategy")
	}

	if config.CountTokens == nil {
		return nil, fmt.Errorf("count_tokens function is required")
	}

	if len(messages) == 0 {
		return messages, nil
	}

	splitter := config.TextSplitter
	if splitter == nil {
		splitter = defaultTextSplitter
	}

	if config.Strategy == TrimStrategyFirst {
		return trimFirst(ctx, messages, maxTokens, config, splitter)
	}
	return trimLast(ctx, messages, maxTokens, config, splitter)
}

// trimFirst keeps the oldest messages that fit within maxTokens.
func trimFirst(ctx context.Context, messages MessageList, maxTokens int, config *TrimConfig, splitter func(string) []string) (MessageList, error) {
	return firstMaxTokens(ctx, messages, maxTokens, config.AllowPartial, false, config.EndOn, config.CountTokens, splitter)
}

// trimLast keeps the most recent messages that fit within maxTokens.
func trimLast(ctx context.Context, messages MessageList, maxTokens int, config *TrimConfig, splitter func(string) []string) (MessageList, error) {
	msgs := messages

	// 1. Apply EndOn: drop trailing messages until the last one matches a permitted role.
	if len(config.EndOn) > 0 {
		for len(msgs) > 0 && !hasRole(msgs[len(msgs)-1], config.EndOn) {
			msgs = msgs[:len(msgs)-1]
		}
	}
	if len(msgs) == 0 {
		return msgs, nil
	}

	// 2. Optionally extract the System message at index 0 so it is always preserved.
	var systemMsg Message
	remaining := msgs
	if config.IncludeSystem && msgs[0].Role() == RoleSystem {
		systemMsg = msgs[0]
		remaining = msgs[1:]
	}

	// 3. Subtract system message tokens from the budget.
	budget := maxTokens
	if systemMsg != nil {
		sysTokens, err := config.CountTokens(ctx, MessageList{systemMsg})
		if err != nil {
			return nil, fmt.Errorf("failed to count tokens for system message: %w", err)
		}
		if budget -= sysTokens; budget < 0 {
			budget = 0
		}
	}

	// 4. Reverse remaining messages, trim from the front (oldest first in the
	//    reversed view), then reverse the result back.  StartOn is passed as
	//    EndOn so that after re-reversal the history begins with the correct role.
	reversed := reverseMessages(remaining)
	trimmed, err := firstMaxTokens(ctx, reversed, budget, config.AllowPartial, true, config.StartOn, config.CountTokens, splitter)
	if err != nil {
		return nil, err
	}

	result := reverseMessages(trimmed)

	// 5. Prepend the preserved System message.
	if systemMsg != nil {
		result = append(MessageList{systemMsg}, result...)
	}
	return result, nil
}

// firstMaxTokens collects the maximum prefix of messages whose combined token
// count does not exceed maxTokens.  When allowPartial is true it may include a
// trimmed version of the first message that would otherwise exceed the budget.
// endOn roles are applied to the final result after all other processing.
func firstMaxTokens(
	ctx context.Context,
	messages MessageList,
	maxTokens int,
	allowPartial bool,
	partialFromEnd bool,
	endOn []Role,
	countTokens func(context.Context, MessageList) (int, error),
	splitter func(string) []string,
) (MessageList, error) {
	if len(messages) == 0 {
		return messages, nil
	}

	// Fast path: everything already fits.
	total, err := countTokens(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("failed to count tokens: %w", err)
	}
	if total <= maxTokens {
		result := messages
		result = applyEndOn(result, endOn)
		return result, nil
	}

	// Binary search for the largest complete-message prefix that fits.
	lo, hi, idx := 0, len(messages), 0
	for lo <= hi {
		mid := (lo + hi) / 2
		if mid == 0 {
			lo++
			continue
		}
		tokens, tErr := countTokens(ctx, messages[:mid])
		if tErr != nil {
			return nil, fmt.Errorf("failed to count tokens: %w", tErr)
		}
		if tokens <= maxTokens {
			idx = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}

	result := make(MessageList, idx)
	copy(result, messages[:idx])

	// Attempt to partially include the next message when AllowPartial is set.
	if allowPartial && idx < len(messages) {
		partial, ok, pErr := partiallyInclude(ctx, messages[idx], result, maxTokens, partialFromEnd, countTokens, splitter)
		if pErr != nil {
			return nil, pErr
		}
		if ok {
			result = append(result, partial)
		}
	}

	result = applyEndOn(result, endOn)
	return result, nil
}

// partiallyInclude attempts to return a version of msg whose content is small
// enough that appending it to current keeps the total within maxTokens.
//
// It first tries removing content blocks one at a time; if even a single block
// is too large it falls back to splitting the text of the relevant TextBlock.
//
// partialFromEnd=false keeps the head of the content (for TrimStrategyFirst);
// partialFromEnd=true keeps the tail (for TrimStrategyLast).
func partiallyInclude(
	ctx context.Context,
	msg Message,
	current MessageList,
	maxTokens int,
	partialFromEnd bool,
	countTokens func(context.Context, MessageList) (int, error),
	splitter func(string) []string,
) (Message, bool, error) {
	content := msg.GetContent()
	if len(content) == 0 {
		return nil, false, nil
	}

	probe := func(candidate Message) (bool, error) {
		buf := make(MessageList, len(current)+1)
		copy(buf, current)
		buf[len(current)] = candidate
		tokens, err := countTokens(ctx, buf)
		if err != nil {
			return false, fmt.Errorf("failed to count tokens: %w", err)
		}
		return tokens <= maxTokens, nil
	}

	// --- Phase 1: try reducing the number of content blocks. ---
	if len(content) > 1 {
		if partialFromEnd {
			// Keep a progressively smaller tail of blocks.
			for i := 1; i < len(content); i++ {
				candidate := cloneWithContent(msg, content[i:])
				ok, err := probe(candidate)
				if err != nil {
					return nil, false, err
				}
				if ok {
					return candidate, true, nil
				}
			}
		} else {
			// Keep a progressively smaller head of blocks.
			for i := len(content) - 1; i >= 1; i-- {
				candidate := cloneWithContent(msg, content[:i])
				ok, err := probe(candidate)
				if err != nil {
					return nil, false, err
				}
				if ok {
					return candidate, true, nil
				}
			}
		}
	}

	// --- Phase 2: split the text of the relevant TextBlock. ---
	textIdx := -1
	if partialFromEnd {
		for i := len(content) - 1; i >= 0; i-- {
			if _, ok := content[i].(*TextBlock); ok {
				textIdx = i
				break
			}
			if _, ok := content[i].(*ThinkingBlock); ok {
				textIdx = i
				break
			}
		}
	} else {
		for i, b := range content {
			if _, ok := b.(*TextBlock); ok {
				textIdx = i
				break
			}
			if _, ok := b.(*ThinkingBlock); ok {
				textIdx = i
				break
			}
		}
	}
	if textIdx < 0 {
		return nil, false, nil
	}

	var origText string
	var isThinking bool
	if tb, ok := content[textIdx].(*TextBlock); ok {
		origText = tb.Text
	} else if thb, ok := content[textIdx].(*ThinkingBlock); ok {
		origText = thb.Thinking
		isThinking = true
	}

	splits := splitter(origText)
	if len(splits) <= 1 {
		return nil, false, nil
	}

	newBlocks := make(Content, len(content))
	copy(newBlocks, content)

	if partialFromEnd {
		// Try the largest tail of splits that fits.
		for i := 1; i < len(splits); i++ {
			txt := strings.Join(splits[i:], "")
			if isThinking {
				newBlocks[textIdx] = &ThinkingBlock{Thinking: txt}
			} else {
				newBlocks[textIdx] = &TextBlock{Text: txt}
			}
			candidate := cloneWithContent(msg, newBlocks)
			ok, err := probe(candidate)
			if err != nil {
				return nil, false, err
			}
			if ok {
				return candidate, true, nil
			}
		}
	} else {
		// Try the largest head of splits that fits.
		for i := len(splits) - 1; i >= 1; i-- {
			txt := strings.Join(splits[:i], "")
			if isThinking {
				newBlocks[textIdx] = &ThinkingBlock{Thinking: txt}
			} else {
				newBlocks[textIdx] = &TextBlock{Text: txt}
			}
			candidate := cloneWithContent(msg, newBlocks)
			ok, err := probe(candidate)
			if err != nil {
				return nil, false, err
			}
			if ok {
				return candidate, true, nil
			}
		}
	}

	return nil, false, nil
}

// cloneWithContent returns a shallow copy of msg with its Content replaced.
func cloneWithContent(msg Message, content Content) Message {
	switch m := msg.(type) {
	case *User:
		c := *m
		c.Content = content
		return &c
	case *Assistant:
		c := *m
		c.Content = content
		return &c
	case *System:
		c := *m
		c.Content = content
		return &c
	case *Tool:
		c := *m
		c.Content = content
		return &c
	default:
		return msg
	}
}

// reverseMessages returns a new MessageList with elements in reverse order.
func reverseMessages(msgs MessageList) MessageList {
	n := len(msgs)
	out := make(MessageList, n)
	for i, m := range msgs {
		out[n-1-i] = m
	}
	return out
}

// hasRole reports whether msg's role is in roles.
func hasRole(msg Message, roles []Role) bool {
	return slices.Contains(roles, msg.Role())
}

// applyEndOn removes trailing messages until the last one matches a role in endOn.
func applyEndOn(msgs MessageList, endOn []Role) MessageList {
	if len(endOn) == 0 {
		return msgs
	}
	for len(msgs) > 0 && !hasRole(msgs[len(msgs)-1], endOn) {
		msgs = msgs[:len(msgs)-1]
	}
	return msgs
}

// defaultTextSplitter splits text on newlines, keeping the newline character
// attached to the preceding segment so that joins reconstruct the original.
func defaultTextSplitter(text string) []string {
	parts := strings.Split(text, "\n")
	out := make([]string, len(parts))
	for i, p := range parts[:len(parts)-1] {
		out[i] = p + "\n"
	}
	out[len(parts)-1] = parts[len(parts)-1]
	return out
}
