package memory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/masterkeysrd/loom/message"
)

// ── mock Invoker ─────────────────────────────────────────────────────────────

type mockInvoker struct {
	response string
	err      error
	calls    []message.MessageList // recorded call arguments
}

func (m *mockInvoker) Invoke(_ context.Context, msgs []message.Message) (*message.Assistant, error) {
	m.calls = append(m.calls, msgs)
	if m.err != nil {
		return nil, m.err
	}
	return &message.Assistant{
		Content: message.Content{&message.TextBlock{Text: m.response}},
	}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// alwaysTrigger returns a SummarizerTrigger that always fires.
func alwaysTrigger() SummarizerTrigger {
	return func(_ context.Context, _ SummarizerTriggerContext) (bool, error) { return true, nil }
}

// neverTrigger returns a SummarizerTrigger that never fires.
func neverTrigger() SummarizerTrigger {
	return func(_ context.Context, _ SummarizerTriggerContext) (bool, error) { return false, nil }
}

// errorTrigger returns a SummarizerTrigger that always errors.
func errorTrigger() SummarizerTrigger {
	return func(_ context.Context, _ SummarizerTriggerContext) (bool, error) {
		return false, errors.New("trigger error")
	}
}

// keepFirst returns a SummarizerKeepFunc that always keeps the first n messages
// (i.e. cutoff = len - n).
func keepFirst(n int) SummarizerKeepFunc {
	return KeepMessageCount(n)
}

// keepNone returns a SummarizerKeepFunc that always returns cutoff=0 (nothing to keep).
func keepNone() SummarizerKeepFunc {
	return func(_ context.Context, _ SummarizerKeepContext) (int, error) { return 0, nil }
}

// errorKeep returns a SummarizerKeepFunc that always errors.
func errorKeep() SummarizerKeepFunc {
	return func(_ context.Context, _ SummarizerKeepContext) (int, error) {
		return 0, errors.New("keep error")
	}
}

// simpleInvoker creates a mockInvoker with the given canned response.
func simpleInvoker(resp string) *mockInvoker { return &mockInvoker{response: resp} }

// makeConfig returns a minimal valid SummarizerConfig.
func makeConfig(_ *mockInvoker, trigger SummarizerTrigger, keep SummarizerKeepFunc) SummarizerConfig {
	return SummarizerConfig{
		TokenCounter: &fixedCounter{count: 100},
		Triggers:     []SummarizerTrigger{trigger},
		Keep:         keep,
	}
}

func makeSummarizer(t *testing.T, invoker *mockInvoker, cfg SummarizerConfig) *Summarizer {
	t.Helper()
	s, err := NewSummarizer(invoker, cfg)
	if err != nil {
		t.Fatalf("NewSummarizer: %v", err)
	}
	return s
}

func nMsgs(n int) message.MessageList {
	msgs := make(message.MessageList, n)
	for i := range msgs {
		msgs[i] = message.NewUserText("msg")
	}
	return msgs
}

// ── NewSummarizer validation ──────────────────────────────────────────────────

func TestNewSummarizer_NilInvoker(t *testing.T) {
	_, err := NewSummarizer(nil, SummarizerConfig{
		TokenCounter: &fixedCounter{count: 0},
		Triggers:     []SummarizerTrigger{alwaysTrigger()},
		Keep:         keepNone(),
	})
	if err == nil {
		t.Fatal("expected error for nil invoker")
	}
}

func TestNewSummarizer_NoTriggers(t *testing.T) {
	_, err := NewSummarizer(simpleInvoker("ok"), SummarizerConfig{
		TokenCounter: &fixedCounter{count: 0},
		Triggers:     nil,
		Keep:         keepNone(),
	})
	if err == nil {
		t.Fatal("expected error for empty triggers")
	}
}

func TestNewSummarizer_NilTokenCounter(t *testing.T) {
	_, err := NewSummarizer(simpleInvoker("ok"), SummarizerConfig{
		TokenCounter: nil,
		Triggers:     []SummarizerTrigger{alwaysTrigger()},
		Keep:         keepNone(),
	})
	if err == nil {
		t.Fatal("expected error for nil TokenCounter")
	}
}

func TestNewSummarizer_InvalidPromptTemplate(t *testing.T) {
	_, err := NewSummarizer(simpleInvoker("ok"), SummarizerConfig{
		PromptTemplate: "{{.Unclosed",
		TokenCounter:   &fixedCounter{count: 0},
		Triggers:       []SummarizerTrigger{alwaysTrigger()},
		Keep:           keepNone(),
	})
	if err == nil {
		t.Fatal("expected error for invalid template")
	}
}

func TestNewSummarizer_ValidConfig_NoError(t *testing.T) {
	_, err := NewSummarizer(simpleInvoker("ok"), SummarizerConfig{
		TokenCounter: &fixedCounter{count: 0},
		Triggers:     []SummarizerTrigger{alwaysTrigger()},
		Keep:         keepNone(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewSummarizer_CustomTemplate_Accepted(t *testing.T) {
	_, err := NewSummarizer(simpleInvoker("ok"), SummarizerConfig{
		PromptTemplate: "summarize: {{.NewMessages}}",
		TokenCounter:   &fixedCounter{count: 0},
		Triggers:       []SummarizerTrigger{alwaysTrigger()},
		Keep:           keepNone(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── Summarize: no summarization path ─────────────────────────────────────────

func TestSummarize_TriggerNotFired_ReturnsInputUnchanged(t *testing.T) {
	inv := simpleInvoker("should not be called")
	s := makeSummarizer(t, inv, makeConfig(inv, neverTrigger(), keepNone()))

	msgs := nMsgs(3)
	out, err := s.Summarize(context.Background(), SummarizeInput{
		CurrentSummary: "old summary",
		Messages:       msgs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.calls) != 0 {
		t.Fatal("invoker should not be called when trigger does not fire")
	}
	if out.Summary != "old summary" {
		t.Fatalf("expected original summary, got %q", out.Summary)
	}
	if len(out.Messages) != 3 {
		t.Fatalf("expected original messages unchanged, got %d", len(out.Messages))
	}
	if out.Tokens != 0 {
		t.Fatalf("expected Tokens=0 when not summarizing, got %d", out.Tokens)
	}
}

func TestSummarize_MultipleTriggers_OnlyOneNeeded(t *testing.T) {
	inv := simpleInvoker("summary text")
	cfg := SummarizerConfig{
		TokenCounter: &fixedCounter{count: 50},
		Triggers:     []SummarizerTrigger{neverTrigger(), alwaysTrigger()},
		Keep:         keepFirst(1),
	}
	s := makeSummarizer(t, inv, cfg)

	msgs := nMsgs(4)
	out, err := s.Summarize(context.Background(), SummarizeInput{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.calls) == 0 {
		t.Fatal("expected invoker to be called when at least one trigger fires")
	}
	_ = out
}

func TestSummarize_CutoffZero_ReturnsInputUnchanged(t *testing.T) {
	inv := simpleInvoker("should not be called")
	cfg := makeConfig(inv, alwaysTrigger(), keepNone()) // keepNone returns 0
	s := makeSummarizer(t, inv, cfg)

	msgs := nMsgs(5)
	out, err := s.Summarize(context.Background(), SummarizeInput{
		CurrentSummary: "prev",
		Messages:       msgs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.calls) != 0 {
		t.Fatal("invoker should not be called when cutoff is 0")
	}
	if out.Summary != "prev" {
		t.Fatalf("expected original summary preserved, got %q", out.Summary)
	}
	if len(out.Messages) != 5 {
		t.Fatalf("expected messages unchanged, got %d", len(out.Messages))
	}
}

// ── Summarize: summarization path ────────────────────────────────────────────

func TestSummarize_BasicFlow_ProducesNewMessages(t *testing.T) {
	inv := simpleInvoker("  extracted context  ")
	cfg := SummarizerConfig{
		TokenCounter: &fixedCounter{count: 100},
		Triggers:     []SummarizerTrigger{alwaysTrigger()},
		Keep:         keepFirst(2), // keep last 2 of 5 → cutoff=3
	}
	s := makeSummarizer(t, inv, cfg)

	msgs := nMsgs(5)
	out, err := s.Summarize(context.Background(), SummarizeInput{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}

	// Invoker was called once.
	if len(inv.calls) != 1 {
		t.Fatalf("expected 1 invoker call, got %d", len(inv.calls))
	}

	// Summary is trimmed text from the invoker.
	if out.Summary != "extracted context" {
		t.Fatalf("expected trimmed summary, got %q", out.Summary)
	}

	// Output messages = 1 summary message + 2 kept messages.
	if len(out.Messages) != 3 {
		t.Fatalf("expected 3 output messages (1 summary + 2 kept), got %d", len(out.Messages))
	}

	// First output message is the summary user message.
	if out.Messages[0].Role() != message.RoleUser {
		t.Fatalf("expected summary message to be User, got %s", out.Messages[0].Role())
	}
	if !strings.Contains(out.Messages[0].GetContent().Text(), "extracted context") {
		t.Fatalf("summary message does not contain expected text: %q", out.Messages[0].GetContent().Text())
	}

	// loom_src metadata is set.
	meta := out.Messages[0].GetMetadata()
	if meta == nil || meta["loom_src"] != "summarizer" {
		t.Fatalf("expected loom_src=summarizer metadata, got %v", meta)
	}

	// Tokens field reflects the pre-summary token count.
	if out.Tokens != 100 {
		t.Fatalf("expected Tokens=100, got %d", out.Tokens)
	}
}

func TestSummarize_SummaryLeadingTrailingSpaces_AreTrimmed(t *testing.T) {
	inv := simpleInvoker("\n  trimmed summary  \n")
	cfg := makeConfig(inv, alwaysTrigger(), keepFirst(1))
	s := makeSummarizer(t, inv, cfg)

	out, err := s.Summarize(context.Background(), SummarizeInput{Messages: nMsgs(3)})
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary != "trimmed summary" {
		t.Fatalf("expected trimmed summary, got %q", out.Summary)
	}
}

func TestSummarize_CurrentSummaryIncludedInPrompt(t *testing.T) {
	inv := simpleInvoker("new summary")
	cfg := SummarizerConfig{
		PromptTemplate: "prev={{.CurrentSummary}} msgs={{.NewMessages}}",
		TokenCounter:   &fixedCounter{count: 10},
		Triggers:       []SummarizerTrigger{alwaysTrigger()},
		Keep:           keepFirst(1),
	}
	s := makeSummarizer(t, inv, cfg)

	out, err := s.Summarize(context.Background(), SummarizeInput{
		CurrentSummary: "THE_PREVIOUS_SUMMARY",
		Messages:       nMsgs(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = out

	// The prompt passed to the invoker should contain the previous summary.
	if len(inv.calls) == 0 {
		t.Fatal("invoker was not called")
	}
	prompt := inv.calls[0][0].GetContent().Text()
	if !strings.Contains(prompt, "THE_PREVIOUS_SUMMARY") {
		t.Fatalf("expected prompt to contain current summary, got: %q", prompt)
	}
}

func TestSummarize_MessagesGetIDs_Assigned(t *testing.T) {
	inv := simpleInvoker("ok")
	cfg := makeConfig(inv, neverTrigger(), keepNone())
	s := makeSummarizer(t, inv, cfg)

	// Messages without IDs.
	msgs := message.MessageList{
		message.NewUserText("a"),
		message.NewUserText("b"),
	}
	_, err := s.Summarize(context.Background(), SummarizeInput{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range msgs {
		if m.GetID() == "" {
			t.Fatalf("message %d has no ID after Summarize", i)
		}
	}
}

func TestSummarize_PreExistingIDs_Preserved(t *testing.T) {
	inv := simpleInvoker("ok")
	cfg := makeConfig(inv, neverTrigger(), keepNone())
	s := makeSummarizer(t, inv, cfg)

	msg := message.NewUserText("hi")
	msg.SetID("my-id")
	msgs := message.MessageList{msg}
	_, err := s.Summarize(context.Background(), SummarizeInput{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}
	if msgs[0].GetID() != "my-id" {
		t.Fatalf("expected ID preserved, got %q", msgs[0].GetID())
	}
}

// ── Summarize: error paths ────────────────────────────────────────────────────

func TestSummarize_TokenCounterError_ReturnsError(t *testing.T) {
	inv := simpleInvoker("ok")
	cfg := SummarizerConfig{
		TokenCounter: &fixedCounter{err: errors.New("count fail")},
		Triggers:     []SummarizerTrigger{alwaysTrigger()},
		Keep:         keepNone(),
	}
	s := makeSummarizer(t, inv, cfg)

	_, err := s.Summarize(context.Background(), SummarizeInput{Messages: nMsgs(2)})
	if err == nil {
		t.Fatal("expected error when TokenCounter fails")
	}
}

func TestSummarize_TriggerError_ReturnsError(t *testing.T) {
	inv := simpleInvoker("ok")
	cfg := makeConfig(inv, errorTrigger(), keepNone())
	s := makeSummarizer(t, inv, cfg)

	_, err := s.Summarize(context.Background(), SummarizeInput{Messages: nMsgs(2)})
	if err == nil {
		t.Fatal("expected error when trigger errors")
	}
}

func TestSummarize_KeepFuncError_ReturnsError(t *testing.T) {
	inv := simpleInvoker("ok")
	cfg := makeConfig(inv, alwaysTrigger(), errorKeep())
	s := makeSummarizer(t, inv, cfg)

	_, err := s.Summarize(context.Background(), SummarizeInput{Messages: nMsgs(2)})
	if err == nil {
		t.Fatal("expected error when keep func errors")
	}
}

func TestSummarize_InvokerError_ReturnsError(t *testing.T) {
	inv := &mockInvoker{err: errors.New("LLM unavailable")}
	cfg := makeConfig(inv, alwaysTrigger(), keepFirst(1))
	s := makeSummarizer(t, inv, cfg)

	_, err := s.Summarize(context.Background(), SummarizeInput{Messages: nMsgs(3)})
	if err == nil {
		t.Fatal("expected error when invoker returns error")
	}
}

// ── Summarize: TrimTokensToSumarize ──────────────────────────────────────────

func TestSummarize_TrimTokensToSumarize_Zero_NoTrimming(t *testing.T) {
	inv := simpleInvoker("compact")
	cfg := SummarizerConfig{
		TokenCounter:         &fixedCounter{count: 50},
		TrimTokensToSumarize: 0, // disabled
		Triggers:             []SummarizerTrigger{alwaysTrigger()},
		Keep:                 keepFirst(1),
	}
	s := makeSummarizer(t, inv, cfg)

	msgs := nMsgs(3)
	_, err := s.Summarize(context.Background(), SummarizeInput{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.calls) != 1 {
		t.Fatalf("expected invoker called once, got %d", len(inv.calls))
	}
}

func TestSummarize_TrimTokensToSumarize_Positive_TrimsBeforeInvoker(t *testing.T) {
	// 4 messages, each "x" (1 char ≈ not counted by fixedCounter).
	// Use a perMessageCounter so trimming is driven by message count.
	// TrimTokensToSumarize=1 forces trim to fit within 1 token.
	// perMessageCounter(1) → each message = 1 token → only 1 message fits.
	inv := simpleInvoker("trimmed summary")
	counter := &perMessageCounter{tokensPerMsg: 10}
	cfg := SummarizerConfig{
		TokenCounter:         counter,
		TrimTokensToSumarize: 10, // only 1 message (10 tokens) fits
		Triggers:             []SummarizerTrigger{alwaysTrigger()},
		Keep:                 keepFirst(1),
	}
	s := makeSummarizer(t, inv, cfg)

	msgs := nMsgs(4)
	_, err := s.Summarize(context.Background(), SummarizeInput{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}
}

// ── Summarize: output message structure ──────────────────────────────────────

func TestSummarize_OutputMessages_OrderIsCorrect(t *testing.T) {
	// 5 messages, keep last 2 → cutoff=3.
	// Output: [summary_msg, msgs[3], msgs[4]].
	inv := simpleInvoker("the summary")
	cfg := SummarizerConfig{
		TokenCounter: &fixedCounter{count: 10},
		Triggers:     []SummarizerTrigger{alwaysTrigger()},
		Keep:         keepFirst(2),
	}
	s := makeSummarizer(t, inv, cfg)

	msgs := make(message.MessageList, 5)
	for i := range msgs {
		m := message.NewUserText(strings.Repeat(string(rune('a'+i)), 3)) // "aaa", "bbb", ...
		msgs[i] = m
	}

	out, err := s.Summarize(context.Background(), SummarizeInput{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}

	if len(out.Messages) != 3 {
		t.Fatalf("expected 3 output messages, got %d", len(out.Messages))
	}
	// First is the summary message.
	if !strings.Contains(out.Messages[0].GetContent().Text(), "the summary") {
		t.Fatalf("first message should be summary, got: %q", out.Messages[0].GetContent().Text())
	}
	// Remaining are the original kept messages (msgs[3] and msgs[4]).
	if out.Messages[1].GetContent().Text() != "ddd" {
		t.Fatalf("expected kept msg[3]='ddd', got %q", out.Messages[1].GetContent().Text())
	}
	if out.Messages[2].GetContent().Text() != "eee" {
		t.Fatalf("expected kept msg[4]='eee', got %q", out.Messages[2].GetContent().Text())
	}
}

func TestSummarize_SummaryMessage_ContainsSummaryPrefix(t *testing.T) {
	inv := simpleInvoker("the context")
	cfg := makeConfig(inv, alwaysTrigger(), keepFirst(1))
	s := makeSummarizer(t, inv, cfg)

	out, err := s.Summarize(context.Background(), SummarizeInput{Messages: nMsgs(3)})
	if err != nil {
		t.Fatal(err)
	}
	text := out.Messages[0].GetContent().Text()
	if !strings.HasPrefix(text, "Summary of previous conversation history:") {
		t.Fatalf("unexpected summary message prefix: %q", text)
	}
}

// ── Summarize: empty messages to summarize ────────────────────────────────────

func TestSummarize_EmptyMessagesToSummarize_NoInvokerCall(t *testing.T) {
	// keepFirst(5) on a 5-message list: len=5, keep=5 → cutoff=0 → no summarization.
	inv := simpleInvoker("should not happen")
	cfg := makeConfig(inv, alwaysTrigger(), keepFirst(5))
	s := makeSummarizer(t, inv, cfg)

	_, err := s.Summarize(context.Background(), SummarizeInput{Messages: nMsgs(5)})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.calls) != 0 {
		t.Fatal("invoker must not be called when cutoff=0")
	}
}
