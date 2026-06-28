package message

import (
	"testing"
)

// TestAggregatorSharedPointerIsolation verifies that two independent
// AssistantAggregators accumulating the same stream of chunks do NOT
// corrupt each other's state via shared *TextBlock / *ThinkingBlock
// pointers.
//
// Before the fix, both aggregators appended the *incoming* pointer
// directly into a.blocks.  The second aggregator's upsert would then
// call `last.(*TextBlock).Text += incoming.Text` where `last` was the
// SAME pointer that the first aggregator had already mutated, producing
// doubled text in the final built message (e.g. "Hello! How How can
// can I I help help...").
func TestAggregatorSharedPointerIsolation(t *testing.T) {
	// Simulate three streaming text chunks (as a provider would emit).
	chunks := []AssistantChunk{
		{Content: []Block{&TextBlock{Text: "Hello!"}}},
		{Content: []Block{&TextBlock{Text: " How"}}},
		{Content: []Block{&TextBlock{Text: " are you?"}}},
	}

	// Two separate aggregators that both process the same chunk stream,
	// mimicking the telemetry middleware aggregator and the Invoke aggregator.
	agg1 := NewAssistantAggregator()
	agg2 := NewAssistantAggregator()

	for i := range chunks {
		agg1.Add(&chunks[i])
		agg2.Add(&chunks[i])
	}

	msg1, err := agg1.Build()
	if err != nil {
		t.Fatalf("agg1.Build: %v", err)
	}
	msg2, err := agg2.Build()
	if err != nil {
		t.Fatalf("agg2.Build: %v", err)
	}

	want := "Hello! How are you?"

	got1 := extractText(msg1.Content)
	got2 := extractText(msg2.Content)

	if got1 != want {
		t.Errorf("agg1: got %q, want %q", got1, want)
	}
	if got2 != want {
		t.Errorf("agg2: got %q, want %q", got2, want)
	}
}

// TestAggregatorTextAccumulation verifies that a single aggregator
// correctly concatenates text chunks into one TextBlock.
func TestAggregatorTextAccumulation(t *testing.T) {
	chunks := []AssistantChunk{
		{Content: []Block{&TextBlock{Text: "foo"}}},
		{Content: []Block{&TextBlock{Text: "bar"}}},
		{Content: []Block{&TextBlock{Text: "baz"}}},
	}
	agg := NewAssistantAggregator()
	for i := range chunks {
		agg.Add(&chunks[i])
	}
	msg, err := agg.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := extractText(msg.Content)
	if want := "foobarbaz"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// Should be collapsed into a single TextBlock.
	if len(msg.Content) != 1 {
		t.Errorf("expected 1 content block, got %d", len(msg.Content))
	}
}

// TestAggregatorThinkingSharedPointerIsolation is the thinking-block
// equivalent of TestAggregatorSharedPointerIsolation.
func TestAggregatorThinkingSharedPointerIsolation(t *testing.T) {
	chunks := []AssistantChunk{
		{Content: []Block{&ThinkingBlock{Thinking: "think1"}}},
		{Content: []Block{&ThinkingBlock{Thinking: "think2"}}},
	}

	agg1 := NewAssistantAggregator()
	agg2 := NewAssistantAggregator()
	for i := range chunks {
		agg1.Add(&chunks[i])
		agg2.Add(&chunks[i])
	}

	msg1, _ := agg1.Build()
	msg2, _ := agg2.Build()

	want := "think1think2"
	got1 := extractThinking(msg1.Content)
	got2 := extractThinking(msg2.Content)

	if got1 != want {
		t.Errorf("agg1: got %q, want %q", got1, want)
	}
	if got2 != want {
		t.Errorf("agg2: got %q, want %q", got2, want)
	}
}

func extractText(blocks []Block) string {
	var s string
	for _, b := range blocks {
		if tb, ok := b.(*TextBlock); ok {
			s += tb.Text
		}
	}
	return s
}

func extractThinking(blocks []Block) string {
	var s string
	for _, b := range blocks {
		if tb, ok := b.(*ThinkingBlock); ok {
			s += tb.Thinking
		}
	}
	return s
}

func TestAggregatorCumulativeMetrics(t *testing.T) {
	chunks := []AssistantChunk{
		{
			Content: []Block{&TextBlock{Text: "foo"}},
			Metrics: &TokenMetrics{
				TotalTokens: 10,
				Tokens: TokenDetails{
					Input:  8,
					Output: 2,
				},
			},
		},
		{
			Content: []Block{&TextBlock{Text: "bar"}},
			Metrics: &TokenMetrics{
				TotalTokens: 11,
				Tokens: TokenDetails{
					Input:  8,
					Output: 3,
				},
			},
		},
	}

	agg := NewAssistantAggregator()
	for i := range chunks {
		agg.Add(&chunks[i])
	}

	msg, err := agg.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if msg.Metrics == nil {
		t.Fatal("expected metrics to be set")
	}

	if msg.Metrics.TotalTokens != 11 {
		t.Errorf("expected TotalTokens 11 (latest cumulative value), got %d (likely summed incorrectly)", msg.Metrics.TotalTokens)
	}
	if msg.Metrics.Tokens.Input != 8 {
		t.Errorf("expected Input tokens 8, got %d", msg.Metrics.Tokens.Input)
	}
	if msg.Metrics.Tokens.Output != 3 {
		t.Errorf("expected Output tokens 3, got %d", msg.Metrics.Tokens.Output)
	}
}
