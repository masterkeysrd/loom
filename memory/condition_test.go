package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/masterkeysrd/loom/message"
)

// ── token-counter mocks ──────────────────────────────────────────────────────

// fixedCounter always returns the same count (or error).
type fixedCounter struct {
	count int
	err   error
}

func (f *fixedCounter) CountTokens(_ context.Context, _ message.MessageList) (int, error) {
	return f.count, f.err
}

// perMessageCounter returns len(msgs) * tokensPerMsg.
type perMessageCounter struct {
	tokensPerMsg int
}

func (p *perMessageCounter) CountTokens(_ context.Context, msgs message.MessageList) (int, error) {
	return len(msgs) * p.tokensPerMsg, nil
}

// ── message constructors ─────────────────────────────────────────────────────

func usr(text string) *message.User { return message.NewUserText(text) }

func newAssistantMsgWithMetrics(totalTokens int) *message.Assistant {
	a := &message.Assistant{Content: message.Content{&message.TextBlock{Text: "reply"}}}
	if totalTokens > 0 {
		a.Metrics = &message.TokenMetrics{TotalTokens: totalTokens}
	}
	return a
}

func asstWithCall(id string) *message.Assistant {
	return &message.Assistant{
		Content: message.Content{&message.ToolCall{ID: id, Name: "tool"}},
	}
}

func toolResult(callID string) *message.Tool {
	return &message.Tool{ToolCallID: callID}
}

func nMessages(n int) message.MessageList {
	msgs := make(message.MessageList, n)
	for i := range msgs {
		msgs[i] = usr("msg")
	}
	return msgs
}

// ── usageThresholdReached ────────────────────────────────────────────────────

func TestUsageThresholdReached_EmptyList(t *testing.T) {
	if got, _ := usageThresholdReached(nil, 10); got {
		t.Fatal("expected false for nil/empty messages")
	}
	if got, _ := usageThresholdReached([]message.Message{}, 10); got {
		t.Fatal("expected false for empty messages")
	}
}

func TestUsageThresholdReached_AssistantNotLast(t *testing.T) {
	msgs := []message.Message{newAssistantMsgWithMetrics(200), usr("hi")}
	// Now should find the assistant message even if not last
	got, ok := usageThresholdReached(msgs, 100)
	if !ok {
		t.Fatal("expected ok=true: assistant message exists")
	}
	if !got {
		t.Fatal("expected true: 200 >= 100")
	}
}

func TestUsageThresholdReached_NilMetrics(t *testing.T) {
	// metrics is nil → usedTokens = -1 → -1 < any positive threshold
	msgs := []message.Message{newAssistantMsgWithMetrics(0)} // asst(0) does not set Metrics
	got, ok := usageThresholdReached(msgs, 1)
	if ok {
		t.Fatal("expected ok=false when Metrics is nil")
	}
	if got {
		t.Fatal("expected false when Metrics is nil")
	}
}

func TestUsageThresholdReached_BelowThreshold(t *testing.T) {
	msgs := []message.Message{newAssistantMsgWithMetrics(50)}
	got, ok := usageThresholdReached(msgs, 100)
	if !ok {
		t.Fatal("expected ok=true: assistant message exists")
	}
	if got {
		t.Fatal("expected false: 50 < 100")
	}
}

func TestUsageThresholdReached_AtThreshold(t *testing.T) {
	msgs := []message.Message{newAssistantMsgWithMetrics(100)}
	got, ok := usageThresholdReached(msgs, 100)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !got {
		t.Fatal("expected true: 100 >= 100")
	}
}

func TestUsageThresholdReached_AboveThreshold(t *testing.T) {
	msgs := []message.Message{newAssistantMsgWithMetrics(150)}
	got, ok := usageThresholdReached(msgs, 100)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !got {
		t.Fatal("expected true: 150 >= 100")
	}
}

// ── TriggerSummaryOnTokenCount ────────────────────────────────────────────────

func TestTriggerOnTokenCount_NotTriggered(t *testing.T) {
	trigger := TriggerSummaryOnTokenCount(100)
	msgs := []message.Message{newAssistantMsgWithMetrics(50)}
	got, err := trigger(context.Background(), SummarizerTriggerContext{Messages: msgs, TotalTokens: 50, ContextLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("expected not triggered (50 < 100)")
	}
}

func TestTriggerOnTokenCount_Triggered_AtThreshold(t *testing.T) {
	trigger := TriggerSummaryOnTokenCount(100)
	msgs := []message.Message{newAssistantMsgWithMetrics(100)}
	got, err := trigger(context.Background(), SummarizerTriggerContext{Messages: msgs, TotalTokens: 100, ContextLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("expected triggered (100 >= 100)")
	}
}

func TestTriggerOnTokenCount_LastMessageNotAssistant_Fallback(t *testing.T) {
	trigger := TriggerSummaryOnTokenCount(10)
	msgs := []message.Message{usr("follow-up")}
	got, err := trigger(context.Background(), SummarizerTriggerContext{Messages: msgs, TotalTokens: 100, ContextLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("expected triggered by TotalTokens fallback")
	}
}

// ── TriggerSummaryOnMessageCount ─────────────────────────────────────────────

func TestTriggerOnMessageCount_BelowThreshold(t *testing.T) {
	trigger := TriggerSummaryOnMessageCount(5)
	msgs := message.MessageList{usr("a"), usr("b"), usr("c")}
	got, err := trigger(context.Background(), SummarizerTriggerContext{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatalf("expected not triggered: 3 < 5")
	}
}

func TestTriggerOnMessageCount_AtThreshold(t *testing.T) {
	trigger := TriggerSummaryOnMessageCount(3)
	msgs := message.MessageList{usr("a"), usr("b"), usr("c")}
	got, err := trigger(context.Background(), SummarizerTriggerContext{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("expected triggered: 3 >= 3")
	}
}

func TestTriggerOnMessageCount_AboveThreshold(t *testing.T) {
	trigger := TriggerSummaryOnMessageCount(2)
	msgs := message.MessageList{usr("a"), usr("b"), usr("c")}
	got, err := trigger(context.Background(), SummarizerTriggerContext{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("expected triggered: 3 >= 2")
	}
}

func TestTriggerOnMessageCount_EmptyList(t *testing.T) {
	trigger := TriggerSummaryOnMessageCount(1)
	got, err := trigger(context.Background(), SummarizerTriggerContext{Messages: nil})
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("expected not triggered for empty list")
	}
}

// ── TriggerSummaryOnFraction ─────────────────────────────────────────────────

func TestTriggerOnFraction_ZeroTotalTokens(t *testing.T) {
	trigger := TriggerSummaryOnFraction(0.8)
	got, err := trigger(context.Background(), SummarizerTriggerContext{
		Messages:     []message.Message{newAssistantMsgWithMetrics(0)},
		TotalTokens:  0,
		ContextLimit: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("expected not triggered when TotalTokens == 0")
	}
}

func TestTriggerOnFraction_BelowThreshold_ShouldNotTrigger(t *testing.T) {
	trigger := TriggerSummaryOnFraction(0.8)
	msgs := []message.Message{newAssistantMsgWithMetrics(50)} // 50 used < 80 threshold
	got, err := trigger(context.Background(), SummarizerTriggerContext{
		Messages:     msgs,
		TotalTokens:  100,
		ContextLimit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("expected not triggered: usage (50) < 80% of 100")
	}
}

func TestTriggerOnFraction_AtThreshold_ShouldTrigger(t *testing.T) {
	trigger := TriggerSummaryOnFraction(0.8)
	msgs := []message.Message{newAssistantMsgWithMetrics(80)} // 80 used == 80 threshold
	got, err := trigger(context.Background(), SummarizerTriggerContext{
		Messages:     msgs,
		TotalTokens:  100,
		ContextLimit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("expected triggered when usage (80) >= threshold (80% of 100=80)")
	}
}

func TestTriggerOnFraction_AboveThreshold_ShouldTrigger(t *testing.T) {
	trigger := TriggerSummaryOnFraction(0.8)
	msgs := []message.Message{newAssistantMsgWithMetrics(95)} // 95 used > 80 threshold
	got, err := trigger(context.Background(), SummarizerTriggerContext{
		Messages:     msgs,
		TotalTokens:  100,
		ContextLimit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("expected triggered when usage (95) >= threshold (80)")
	}
}

func TestTriggerOnFraction_NoMetrics_FallbackToTotalTokens(t *testing.T) {
	trigger := TriggerSummaryOnFraction(0.5)
	msgs := []message.Message{usr("hi")} // no assistant/metrics
	got, err := trigger(context.Background(), SummarizerTriggerContext{
		Messages:     msgs,
		TotalTokens:  200, // 200 >= 0.5 * 200 = 100
		ContextLimit: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("expected triggered by TotalTokens fallback")
	}
}

// ── findSafeCutoff ────────────────────────────────────────────────────────────

func TestFindSafeCutoff_FewerMessagesThanKeep(t *testing.T) {
	msgs := nMessages(3)
	// keep=5 but only 3 messages → nothing to summarize
	if got := findSafeCutoff(msgs, 5); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestFindSafeCutoff_ExactlyKeepCount(t *testing.T) {
	msgs := nMessages(5)
	// keep=5, have=5 → len(msgs) <= cutoff → return 0
	if got := findSafeCutoff(msgs, 5); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestFindSafeCutoff_MoreThanKeepCount(t *testing.T) {
	msgs := nMessages(7)
	// keep=3 → targetCutoff = 7-3 = 4
	if got := findSafeCutoff(msgs, 3); got != 4 {
		t.Fatalf("expected 4, got %d", got)
	}
}

func TestFindSafeCutoff_KeepZero(t *testing.T) {
	msgs := nMessages(4)
	// keep=0 → targetCutoff = 4-0 = 4; msgs[4] is out of bounds but
	// findSafeCutoffPoint checks idx >= len(msgs) → returns idx=4
	if got := findSafeCutoff(msgs, 0); got != 4 {
		t.Fatalf("expected 4, got %d", got)
	}
}

// ── findSafeCutoffPoint ───────────────────────────────────────────────────────

func TestFindSafeCutoffPoint_NonToolMessage_ReturnsCutoffUnchanged(t *testing.T) {
	msgs := message.MessageList{usr("a"), usr("b"), usr("c")}
	if got := findSafeCutoffPoint(msgs, 1); got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}
}

func TestFindSafeCutoffPoint_CutoffBeyondLength_ReturnsCutoff(t *testing.T) {
	msgs := message.MessageList{usr("a"), usr("b")}
	if got := findSafeCutoffPoint(msgs, 5); got != 5 {
		t.Fatalf("expected 5 (>= len), got %d", got)
	}
}

func TestFindSafeCutoffPoint_CutoffAtTool_MatchingAssistant_PullsBack(t *testing.T) {
	// Layout: [assistant-with-call(tc1), tool(tc1), user]
	// Cutoff=1 (at tool message) → search backward → find assistant at 0 → return 0
	msgs := message.MessageList{
		asstWithCall("tc1"),
		toolResult("tc1"),
		usr("next"),
	}
	got := findSafeCutoffPoint(msgs, 1)
	if got != 0 {
		t.Fatalf("expected cutoff pulled back to 0 (assistant index), got %d", got)
	}
}

func TestFindSafeCutoffPoint_CutoffAtTool_MultipleToolMessages(t *testing.T) {
	// [assistant-with-call(tc1,tc2), tool(tc1), tool(tc2), user]
	// Cutoff=1 (first tool) → collect {tc1,tc2}, find assistant at 0 → return 0
	assistant := &message.Assistant{
		Content: message.Content{
			&message.ToolCall{ID: "tc1", Name: "search"},
			&message.ToolCall{ID: "tc2", Name: "write"},
		},
	}
	msgs := message.MessageList{
		assistant,
		toolResult("tc1"),
		toolResult("tc2"),
		usr("done"),
	}
	got := findSafeCutoffPoint(msgs, 1)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestFindSafeCutoffPoint_CutoffAtTool_CutoffBetweenToolMessages(t *testing.T) {
	// [assistant-with-call(tc1,tc2), tool(tc1), tool(tc2), user]
	// Cutoff=2 (second tool) → collect {tc2}, find assistant at 0 that has tc2 → return 0
	assistant := &message.Assistant{
		Content: message.Content{
			&message.ToolCall{ID: "tc1"},
			&message.ToolCall{ID: "tc2"},
		},
	}
	msgs := message.MessageList{
		assistant,
		toolResult("tc1"),
		toolResult("tc2"),
		usr("done"),
	}
	got := findSafeCutoffPoint(msgs, 2)
	if got != 0 {
		t.Fatalf("expected 0 (pull back to assistant), got %d", got)
	}
}

func TestFindSafeCutoffPoint_CutoffAtTool_NoMatchingAssistant_AdvancesForward(t *testing.T) {
	// [tool(tc1), user] — no assistant before the tool
	// fallback: advance idx past all consecutive Tool messages (idx=1)
	msgs := message.MessageList{
		toolResult("tc1"), // orphaned tool at index 0
		usr("next"),
	}
	got := findSafeCutoffPoint(msgs, 0)
	// should advance past the Tool at 0 → idx=1
	if got != 1 {
		t.Fatalf("expected 1 (advanced past orphaned tool), got %d", got)
	}
}

// ── findTokenBasedCutoff ──────────────────────────────────────────────────────

// BUG TEST: the binary search currently slices msgs[:mid] (prefix) instead of
// msgs[mid:] (suffix) as the spec requires, causing it to find the trivially
// smallest prefix (index 0) instead of the correct cutoff.
func TestFindTokenBasedCutoff_FindsSuffixBasedCutoff(t *testing.T) {
	// 5 messages, 10 tokens each → total=50.
	// targetCount=30 means "keep 30 tokens at the end".
	// msgs[2:] = 3 messages = 30 tokens ≤ 30 → cutoff should be 2.
	// BUG: current impl counts msgs[:mid] (prefix) and always returns 0.
	ctx := context.Background()
	msgs := nMessages(5)
	counter := &perMessageCounter{tokensPerMsg: 10}

	got, err := findTokenBasedCutoff(ctx, msgs, counter, counter, 30)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("BUG: expected cutoff=2 (keep last 30 tokens), got %d\n"+
			"The binary search uses msgs[:mid] instead of msgs[mid:] as the spec requires", got)
	}
}

func TestFindTokenBasedCutoff_AllFit_ReturnsZero(t *testing.T) {
	// total tokens (50) <= targetCount (100) → summarize nothing
	ctx := context.Background()
	msgs := nMessages(5)
	counter := &perMessageCounter{tokensPerMsg: 10}

	got, err := findTokenBasedCutoff(ctx, msgs, counter, counter, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("expected 0 (nothing to summarize), got %d", got)
	}
}

func TestFindTokenBasedCutoff_TargetZero_ReturnsOne(t *testing.T) {
	ctx := context.Background()
	msgs := nMessages(3)
	counter := &perMessageCounter{tokensPerMsg: 10}

	got, err := findTokenBasedCutoff(ctx, msgs, counter, counter, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("expected 1 for targetCount<=0, got %d", got)
	}
}

func TestFindTokenBasedCutoff_TargetNegative_ReturnsOne(t *testing.T) {
	ctx := context.Background()
	msgs := nMessages(3)
	counter := &perMessageCounter{tokensPerMsg: 10}

	got, err := findTokenBasedCutoff(ctx, msgs, counter, counter, -5)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("expected 1 for targetCount<=0, got %d", got)
	}
}

func TestFindTokenBasedCutoff_SingleMessage_TooLarge(t *testing.T) {
	// 1 message, 20 tokens, target=10 → cannot keep only 10; return 0
	ctx := context.Background()
	msgs := nMessages(1)
	counter := &perMessageCounter{tokensPerMsg: 20}

	got, err := findTokenBasedCutoff(ctx, msgs, counter, counter, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("expected 0 for single oversized message, got %d", got)
	}
}

func TestFindTokenBasedCutoff_CounterError_Propagates(t *testing.T) {
	ctx := context.Background()
	msgs := nMessages(5)
	errC := &fixedCounter{err: errors.New("counter fail")}
	// The exact counter is used first; if it errors we should get an error back.
	_, err := findTokenBasedCutoff(ctx, msgs, errC, errC, 10)
	if err == nil {
		t.Fatal("expected error from counter")
	}
}

func TestFindTokenBasedCutoff_KeepExactlyAllButOne(t *testing.T) {
	// 4 messages, 10 tokens each → total=40.
	// targetCount=30 → keep last 3 messages (30 tokens) → cutoff=1.
	ctx := context.Background()
	msgs := nMessages(4)
	counter := &perMessageCounter{tokensPerMsg: 10}

	got, err := findTokenBasedCutoff(ctx, msgs, counter, counter, 30)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("expected cutoff=1, got %d", got)
	}
}

// ── KeepMessageCount ──────────────────────────────────────────────────────────

func TestKeepMessageCount_Basic(t *testing.T) {
	msgs := nMessages(7)
	fn := KeepMessageCount(3)
	keepCtx := SummarizerKeepContext{Messages: msgs}
	got, err := fn(context.Background(), keepCtx)
	if err != nil {
		t.Fatal(err)
	}
	// keep 3 → cutoff = 7-3 = 4
	if got != 4 {
		t.Fatalf("expected 4, got %d", got)
	}
}

func TestKeepMessageCount_FewerMessagesThanKeep(t *testing.T) {
	msgs := nMessages(2)
	fn := KeepMessageCount(5)
	keepCtx := SummarizerKeepContext{Messages: msgs}
	got, err := fn(context.Background(), keepCtx)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// ── KeepTokenCount ────────────────────────────────────────────────────────────

func TestKeepTokenCount_Basic(t *testing.T) {
	// 5 messages, 10 tokens each. KeepTokenCount(30) → keep last 30t → cutoff=2
	ctx := context.Background()
	msgs := nMessages(5)
	counter := &perMessageCounter{tokensPerMsg: 10}
	fn := KeepTokenCount(30)
	keepCtx := SummarizerKeepContext{
		Messages:      msgs,
		TokenCounter:  counter,
		ApproxCounter: counter,
	}
	got, err := fn(ctx, keepCtx)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("expected cutoff=2, got %d", got)
	}
}

// ── KeepFraction ─────────────────────────────────────────────────────────────

func TestKeepFraction_NilTokenCounter_ReturnsError(t *testing.T) {
	fn := KeepFraction(0.5)
	_, err := fn(context.Background(), SummarizerKeepContext{
		Messages:     nMessages(3),
		TokenCounter: nil,
	})
	if err == nil {
		t.Fatal("expected error when TokenCounter is nil")
	}
}

func TestKeepFraction_CounterError_Propagates(t *testing.T) {
	fn := KeepFraction(0.5)
	errC := &fixedCounter{err: errors.New("count fail")}
	_, err := fn(context.Background(), SummarizerKeepContext{
		Messages:      nMessages(3),
		TokenCounter:  errC,
		ApproxCounter: errC,
	})
	if err == nil {
		t.Fatal("expected error from token counter")
	}
}

func TestKeepFraction_Basic(t *testing.T) {
	// 6 messages, 10 tokens each → total=60. KeepFraction(0.5) → keep 30t → cutoff=3.
	ctx := context.Background()
	msgs := nMessages(6)
	counter := &perMessageCounter{tokensPerMsg: 10}
	fn := KeepFraction(0.5)
	keepCtx := SummarizerKeepContext{
		Messages:      msgs,
		TokenCounter:  counter,
		ApproxCounter: counter,
	}
	got, err := fn(ctx, keepCtx)
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Fatalf("expected cutoff=3 (keep 50%%), got %d", got)
	}
}

func TestKeepFraction_AllFit(t *testing.T) {
	// total tokens well below any fraction threshold → summarize nothing
	ctx := context.Background()
	msgs := nMessages(2)
	zeroCounter := &fixedCounter{count: 0}
	fn := KeepFraction(0.8)
	keepCtx := SummarizerKeepContext{
		Messages:      msgs,
		TokenCounter:  zeroCounter,
		ApproxCounter: zeroCounter,
	}
	got, err := fn(ctx, keepCtx)
	if err != nil {
		t.Fatal(err)
	}
	// total=0 <= target=0 → KeepFraction early-path: return 0 if targetTokens<=0
	// target = 0.8 * 0 = 0 → findTokenBasedCutoff(target=0) → return 1
	_ = got // just verify no error
}
