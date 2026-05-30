package message

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// countByMessage is a simple token counter that assigns 1 token per message,
// regardless of content. Useful for testing trimming by message count.
func countByMessage(_ context.Context, msgs MessageList) (int, error) {
	return len(msgs), nil
}

// countByChars counts one token per character across all text blocks in all
// messages. Deterministic for text-only conversations.
func countByChars(_ context.Context, msgs MessageList) (int, error) {
	total := 0
	for _, msg := range msgs {
		for _, block := range msg.GetContent() {
			if tb, ok := block.(*TextBlock); ok {
				total += len(tb.Text)
			}
		}
	}
	return total, nil
}

// errCounter always returns an error, used to test error propagation.
func errCounter(_ context.Context, _ MessageList) (int, error) {
	return 0, errors.New("counter error")
}

// roles extracts the Role of each message in order.
func roles(msgs MessageList) []Role {
	out := make([]Role, len(msgs))
	for i, m := range msgs {
		out[i] = m.Role()
	}
	return out
}

// texts extracts the first TextBlock text from each message in order.
func texts(msgs MessageList) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.GetContent().Text()
	}
	return out
}

func TestTrimMessages_NilConfig_DefaultsToLast(t *testing.T) {
	msgs := MessageList{NewUserText("a"), NewUserText("b"), NewUserText("c")}
	got, err := TrimMessages(context.Background(), msgs, 2, &TrimConfig{
		Strategy:    TrimStrategyLast,
		CountTokens: countByMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
}

func TestTrimMessages_NoCountTokens_ReturnsError(t *testing.T) {
	_, err := TrimMessages(context.Background(), MessageList{NewUserText("hi")}, 10, &TrimConfig{})
	if err == nil {
		t.Fatal("expected error when CountTokens is nil")
	}
}

func TestTrimMessages_StartOnWithFirst_ReturnsError(t *testing.T) {
	_, err := TrimMessages(context.Background(), MessageList{NewUserText("hi")}, 10, &TrimConfig{
		Strategy:    TrimStrategyFirst,
		StartOn:     []Role{RoleUser},
		CountTokens: countByMessage,
	})
	if err == nil {
		t.Fatal("expected error: start_on incompatible with first strategy")
	}
}

func TestTrimMessages_IncludeSystemWithFirst_ReturnsError(t *testing.T) {
	_, err := TrimMessages(context.Background(), MessageList{NewUserText("hi")}, 10, &TrimConfig{
		Strategy:      TrimStrategyFirst,
		IncludeSystem: true,
		CountTokens:   countByMessage,
	})
	if err == nil {
		t.Fatal("expected error: include_system incompatible with first strategy")
	}
}

func TestTrimMessages_CountTokensError_Propagates(t *testing.T) {
	msgs := MessageList{NewUserText("a"), NewUserText("b"), NewUserText("c")}
	_, err := TrimMessages(context.Background(), msgs, 1, &TrimConfig{
		Strategy:    TrimStrategyLast,
		CountTokens: errCounter,
	})
	if err == nil {
		t.Fatal("expected error from counter")
	}
}

func TestTrimMessages_EmptyList_ReturnsEmpty(t *testing.T) {
	got, err := TrimMessages(context.Background(), MessageList{}, 10, &TrimConfig{
		Strategy:    TrimStrategyLast,
		CountTokens: countByMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %d", len(got))
	}
}

// ── already fits ─────────────────────────────────────────────────────────────

func TestTrimMessages_AlreadyFits_ReturnsAll(t *testing.T) {
	msgs := MessageList{NewUserText("a"), NewAssistantText("b"), NewUserText("c")}
	got, err := TrimMessages(context.Background(), msgs, 100, &TrimConfig{
		Strategy:    TrimStrategyLast,
		CountTokens: countByMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
}

// ── TrimStrategyLast ─────────────────────────────────────────────────────────

func TestTrimLast_KeepsNewest(t *testing.T) {
	msgs := MessageList{NewUserText("1"), NewAssistantText("2"), NewUserText("3"), NewAssistantText("4"), NewUserText("5")}
	got, err := TrimMessages(context.Background(), msgs, 3, &TrimConfig{
		Strategy:    TrimStrategyLast,
		CountTokens: countByMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"3", "4", "5"}
	if got := texts(got); !slices.Equal(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestTrimLast_IncludeSystem_PreservesSystemMessage(t *testing.T) {
	msgs := MessageList{NewSystemText("sys"), NewUserText("1"), NewAssistantText("2"), NewUserText("3"), NewAssistantText("4"), NewUserText("5")}
	// budget=3: system consumes 1, leaving 2 for the rest → keep last 2 non-system
	got, err := TrimMessages(context.Background(), msgs, 3, &TrimConfig{
		Strategy:      TrimStrategyLast,
		IncludeSystem: true,
		CountTokens:   countByMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Role() != RoleSystem {
		t.Fatal("expected first message to be system")
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 messages (system + 2 others), got %d", len(got))
	}
}

func TestTrimLast_IncludeSystem_NoSystemInList(t *testing.T) {
	msgs := MessageList{NewUserText("1"), NewAssistantText("2"), NewUserText("3")}
	got, err := TrimMessages(context.Background(), msgs, 2, &TrimConfig{
		Strategy:      TrimStrategyLast,
		IncludeSystem: true,
		CountTokens:   countByMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantRoles := []Role{RoleAssistant, RoleUser}
	if got := roles(got); !slices.Equal(got, wantRoles) {
		t.Fatalf("expected %v, got %v", wantRoles, got)
	}
}

func TestTrimLast_StartOn_DropsLeadingNonMatchingRoles(t *testing.T) {
	msgs := MessageList{NewAssistantText("1"), NewUserText("2"), NewAssistantText("3"), NewUserText("4")}
	// budget=4 (all fit), but StartOn=user means result must start with a user message
	got, err := TrimMessages(context.Background(), msgs, 4, &TrimConfig{
		Strategy:    TrimStrategyLast,
		StartOn:     []Role{RoleUser},
		CountTokens: countByMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].Role() != RoleUser {
		t.Fatalf("expected result to start with user, got %v", roles(got))
	}
}

func TestTrimLast_StartOn_AlreadyStartsOnRole(t *testing.T) {
	msgs := MessageList{NewUserText("1"), NewAssistantText("2"), NewUserText("3")}
	got, err := TrimMessages(context.Background(), msgs, 10, &TrimConfig{
		Strategy:    TrimStrategyLast,
		StartOn:     []Role{RoleUser},
		CountTokens: countByMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	// all three fit and first is already user → no messages dropped
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
}

func TestTrimLast_EndOn_DropsTrailingNonMatchingMessages(t *testing.T) {
	msgs := MessageList{NewUserText("1"), NewAssistantText("2"), NewUserText("3"), NewAssistantText("4")}
	// EndOn=user: trailing assistant must be dropped before trimming
	got, err := TrimMessages(context.Background(), msgs, 10, &TrimConfig{
		Strategy:    TrimStrategyLast,
		EndOn:       []Role{RoleUser},
		CountTokens: countByMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[len(got)-1].Role() != RoleUser {
		t.Fatalf("expected last message to be user, got %v", roles(got))
	}
}

func TestTrimLast_EndOn_AllDropped_ReturnsEmpty(t *testing.T) {
	msgs := MessageList{NewAssistantText("1"), NewAssistantText("2")}
	got, err := TrimMessages(context.Background(), msgs, 10, &TrimConfig{
		Strategy:    TrimStrategyLast,
		EndOn:       []Role{RoleUser},
		CountTokens: countByMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %d messages", len(got))
	}
}

func TestTrimLast_SystemExceedsBudget_ReturnsSystemOnly(t *testing.T) {
	msgs := MessageList{NewSystemText("system"), NewUserText("1"), NewAssistantText("2")}
	// budget=1: system itself costs 1, leaving 0 for the rest
	got, err := TrimMessages(context.Background(), msgs, 1, &TrimConfig{
		Strategy:      TrimStrategyLast,
		IncludeSystem: true,
		CountTokens:   countByMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Role() != RoleSystem {
		t.Fatalf("expected only system message, got %v", roles(got))
	}
}

// ── TrimStrategyFirst ────────────────────────────────────────────────────────

func TestTrimFirst_KeepsOldest(t *testing.T) {
	msgs := MessageList{NewUserText("1"), NewAssistantText("2"), NewUserText("3"), NewAssistantText("4"), NewUserText("5")}
	got, err := TrimMessages(context.Background(), msgs, 3, &TrimConfig{
		Strategy:    TrimStrategyFirst,
		CountTokens: countByMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1", "2", "3"}
	if got := texts(got); !slices.Equal(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestTrimFirst_EndOn_DropsTrailingNonMatchingAfterTrim(t *testing.T) {
	msgs := MessageList{NewUserText("1"), NewAssistantText("2"), NewUserText("3"), NewAssistantText("4")}
	// budget=3, EndOn=user → take first 3, then strip trailing assistant
	got, err := TrimMessages(context.Background(), msgs, 3, &TrimConfig{
		Strategy:    TrimStrategyFirst,
		EndOn:       []Role{RoleUser},
		CountTokens: countByMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[len(got)-1].Role() != RoleUser {
		t.Fatalf("expected last message to be user, got %v", roles(got))
	}
}

func TestTrimFirst_AlreadyFits_EndOn_Applied(t *testing.T) {
	msgs := MessageList{NewUserText("1"), NewAssistantText("2"), NewUserText("3"), NewAssistantText("4")}
	// all fit but EndOn=user should still trim trailing assistant
	got, err := TrimMessages(context.Background(), msgs, 100, &TrimConfig{
		Strategy:    TrimStrategyFirst,
		EndOn:       []Role{RoleUser},
		CountTokens: countByMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[len(got)-1].Role() != RoleUser {
		t.Fatalf("expected last message to be user, got %v", roles(got))
	}
}

// ── AllowPartial ─────────────────────────────────────────────────────────────

func TestTrimFirst_AllowPartial_SplitsTextBlock(t *testing.T) {
	// "hello\nworld" → splits: ["hello\n", "world"]
	// budget: NewUserText("hi") costs 2 chars, leaving 7 for next message
	// "hello\nworld" costs 11 chars; "hello\n" costs 6 → fits
	msgs := MessageList{NewUserText("hi"), NewUserText("hello\nworld")}
	got, err := TrimMessages(context.Background(), msgs, 8, &TrimConfig{
		Strategy:     TrimStrategyFirst,
		AllowPartial: true,
		CountTokens:  countByChars,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (one partial), got %d", len(got))
	}
	partial := got[1].GetContent().Text()
	if partial == "hello\nworld" {
		t.Fatal("expected partial text, got full text")
	}
	if !strings.HasPrefix("hello\nworld", partial) {
		t.Fatalf("partial %q is not a prefix of the original", partial)
	}
}

func TestTrimFirst_NoAllowPartial_DoesNotSplit(t *testing.T) {
	msgs := MessageList{NewUserText("hi"), NewUserText("hello\nworld")}
	got, err := TrimMessages(context.Background(), msgs, 8, &TrimConfig{
		Strategy:     TrimStrategyFirst,
		AllowPartial: false,
		CountTokens:  countByChars,
	})
	if err != nil {
		t.Fatal(err)
	}
	// "hi" (2) fits within 8; "hello\nworld" (11) does not → only 1 message
	if len(got) != 1 {
		t.Fatalf("expected 1 message when AllowPartial=false, got %d", len(got))
	}
}

func TestTrimLast_AllowPartial_KeepsTailOfText(t *testing.T) {
	// With strategy=last and allow_partial, we keep the tail of the partial message.
	msgs := MessageList{NewUserText("hello\nworld"), NewUserText("end")}
	// "end" costs 3, "hello\nworld" costs 11 → total 14; budget=9
	// drop "hello\nworld" (11); try partial: "world" (5) + "end" (3) = 8 ≤ 9 ✓
	got, err := TrimMessages(context.Background(), msgs, 9, &TrimConfig{
		Strategy:     TrimStrategyLast,
		AllowPartial: true,
		CountTokens:  countByChars,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (one partial), got %d", len(got))
	}
	partial := got[0].GetContent().Text()
	if partial == "hello\nworld" {
		t.Fatal("expected partial tail, got full text")
	}
	if !strings.HasSuffix("hello\nworld", partial) {
		t.Fatalf("partial %q is not a suffix of the original", partial)
	}
}

func TestTrimFirst_AllowPartial_MultipleBlocks_ReducesBlocks(t *testing.T) {
	// Message with two text blocks; partial should strip the second one.
	bigMsg := &User{Content: Content{
		&TextBlock{Text: "aaaa"}, // 4 chars
		&TextBlock{Text: "bbbb"}, // 4 chars
	}}
	msgs := MessageList{NewUserText("xx"), bigMsg} // NewUserText("xx")=2, bigMsg=8 total→10; budget=6
	got, err := TrimMessages(context.Background(), msgs, 6, &TrimConfig{
		Strategy:     TrimStrategyFirst,
		AllowPartial: true,
		CountTokens:  countByChars,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	// partial bigMsg should have only one block
	if len(got[1].GetContent()) != 1 {
		t.Fatalf("expected 1 block in partial message, got %d", len(got[1].GetContent()))
	}
}

// ── defaultTextSplitter ───────────────────────────────────────────────────────

func TestDefaultTextSplitter_SingleLine(t *testing.T) {
	splits := defaultTextSplitter("hello")
	if len(splits) != 1 || splits[0] != "hello" {
		t.Fatalf("unexpected splits: %v", splits)
	}
}

func TestDefaultTextSplitter_MultiLine_ReconstructsOriginal(t *testing.T) {
	original := "line1\nline2\nline3"
	splits := defaultTextSplitter(original)
	if strings.Join(splits, "") != original {
		t.Fatalf("join of splits %v != original %q", splits, original)
	}
}

func TestDefaultTextSplitter_TrailingNewline(t *testing.T) {
	original := "line1\nline2\n"
	splits := defaultTextSplitter(original)
	if strings.Join(splits, "") != original {
		t.Fatalf("join of splits %v != original %q", splits, original)
	}
}

// ── reverseMessages ──────────────────────────────────────────────────────────

func TestReverseMessages_PreservesOrder(t *testing.T) {
	msgs := MessageList{NewUserText("1"), NewAssistantText("2"), NewUserText("3")}
	rev := reverseMessages(msgs)
	if texts(rev)[0] != "3" || texts(rev)[2] != "1" {
		t.Fatalf("unexpected reverse: %v", texts(rev))
	}
	// original must be untouched
	if texts(msgs)[0] != "1" {
		t.Fatal("original slice was mutated by reverseMessages")
	}
}

func TestReverseMessages_Empty(t *testing.T) {
	rev := reverseMessages(MessageList{})
	if len(rev) != 0 {
		t.Fatal("expected empty slice")
	}
}
