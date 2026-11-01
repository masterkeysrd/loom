package message

import (
	"encoding/json"
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

func TestAggregatorMetadata(t *testing.T) {
	agg := NewAssistantAggregator()
	agg.Add(&AssistantChunk{
		Metadata: map[string]any{
			"openai.response_id": "resp_123",
		},
	})
	agg.Add(&AssistantChunk{
		Content: []Block{&TextBlock{Text: "Hello"}},
		Metadata: map[string]any{
			"custom_key": "custom_val",
		},
	})

	msg, err := agg.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	meta := msg.GetMetadata()
	if meta == nil {
		t.Fatal("expected metadata to be populated")
	}
	if meta["openai.response_id"] != "resp_123" {
		t.Errorf("expected openai.response_id 'resp_123', got %v", meta["openai.response_id"])
	}
	if meta["custom_key"] != "custom_val" {
		t.Errorf("expected custom_key 'custom_val', got %v", meta["custom_key"])
	}
}

func TestAggregatorExtrasPreservation(t *testing.T) {
	agg := NewAssistantAggregator()
	agg.Add(&AssistantChunk{
		Content: []Block{
			&ThinkingBlock{
				Thinking: "Step 1: thinking...",
				Extras: map[string]any{
					"openai.reasoning_id": "rs_1",
				},
			},
		},
	})
	agg.Add(&AssistantChunk{
		Content: []Block{
			&ThinkingBlock{
				Thinking: " Step 2: conclusion.",
				Extras: map[string]any{
					"openai.encrypted_content": "enc_blob_123",
				},
			},
		},
	})

	msg, err := agg.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 merged thinking block, got %d", len(msg.Content))
	}
	tb, ok := msg.Content[0].(*ThinkingBlock)
	if !ok {
		t.Fatalf("expected *ThinkingBlock, got %T", msg.Content[0])
	}
	if tb.Thinking != "Step 1: thinking... Step 2: conclusion." {
		t.Errorf("unexpected thinking text: %q", tb.Thinking)
	}
	if tb.Extras["openai.reasoning_id"] != "rs_1" {
		t.Errorf("expected reasoning_id 'rs_1', got %v", tb.Extras["openai.reasoning_id"])
	}
	if tb.Extras["openai.encrypted_content"] != "enc_blob_123" {
		t.Errorf("expected encrypted_content 'enc_blob_123', got %v", tb.Extras["openai.encrypted_content"])
	}
}

func TestAggregatorServerToolInPlaceOrdering(t *testing.T) {
	chunks := []AssistantChunk{
		{
			Content: []Block{
				&ThinkingBlock{Thinking: "Thinking about the problem..."},
			},
		},
		{
			Content: []Block{
				&ServerToolCallChunk{
					Index:     1,
					ID:        "call_search",
					Name:      "web_search",
					ArgsChunk: `{"query":`,
					Extras:    map[string]any{"source": "provider"},
				},
			},
		},
		{
			Content: []Block{
				&ServerToolCallChunk{
					Index:     1,
					ArgsChunk: `"golang"}`,
				},
			},
		},
		{
			Content: []Block{
				&ServerToolResult{
					ToolCallID: "call_search",
					Name:       "web_search",
					Content: Content{
						&ToolReferenceBlock{
							ToolName: "fetch_webpage",
							Extras:   map[string]any{"auto_loaded": true},
						},
					},
					Output: map[string]any{"result": "found"},
				},
			},
		},
		{
			Content: []Block{
				&TextBlock{Text: "Here are the search results:"},
			},
		},
		{
			Content: []Block{
				&ToolCallChunk{
					Index:     4,
					ID:        "call_client",
					Name:      "fetch_webpage",
					ArgsChunk: `{"url":`,
					Extras:    map[string]any{"client": true},
				},
			},
		},
		{
			Content: []Block{
				&ToolCallChunk{
					Index:     4,
					ArgsChunk: `"https://golang.org"}`,
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
		t.Fatalf("Build failed: %v", err)
	}

	if len(msg.Content) != 5 {
		t.Fatalf("expected 5 content blocks, got %d", len(msg.Content))
	}

	// 1. ThinkingBlock
	tb, ok := msg.Content[0].(*ThinkingBlock)
	if !ok || tb.Thinking != "Thinking about the problem..." {
		t.Errorf("block 0: expected ThinkingBlock with 'Thinking about the problem...', got %#v", msg.Content[0])
	}

	// 2. ServerToolCall
	stc, ok := msg.Content[1].(*ServerToolCall)
	if !ok {
		t.Fatalf("block 1: expected *ServerToolCall, got %T", msg.Content[1])
	}
	if stc.ID != "call_search" || stc.Name != "web_search" || stc.Index != 1 {
		t.Errorf("block 1: unexpected ServerToolCall fields: %#v", stc)
	}
	if stc.Args["query"] != "golang" {
		t.Errorf("block 1: expected query 'golang', got %v", stc.Args["query"])
	}
	if stc.Extras["source"] != "provider" {
		t.Errorf("block 1: expected source 'provider', got %v", stc.Extras["source"])
	}

	// 3. ServerToolResult
	str, ok := msg.Content[2].(*ServerToolResult)
	if !ok {
		t.Fatalf("block 2: expected *ServerToolResult, got %T", msg.Content[2])
	}
	if str.ToolCallID != "call_search" || str.Name != "web_search" {
		t.Errorf("block 2: unexpected ServerToolResult fields: %#v", str)
	}
	if len(str.Content) != 1 {
		t.Fatalf("block 2: expected 1 nested content block, got %d", len(str.Content))
	}
	trb, ok := str.Content[0].(*ToolReferenceBlock)
	if !ok || trb.ToolName != "fetch_webpage" || trb.Extras["auto_loaded"] != true {
		t.Errorf("block 2: unexpected ToolReferenceBlock: %#v", str.Content[0])
	}

	// 4. TextBlock
	txt, ok := msg.Content[3].(*TextBlock)
	if !ok || txt.Text != "Here are the search results:" {
		t.Errorf("block 3: expected TextBlock, got %#v", msg.Content[3])
	}

	// 5. ToolCall
	tc, ok := msg.Content[4].(*ToolCall)
	if !ok {
		t.Fatalf("block 4: expected *ToolCall, got %T", msg.Content[4])
	}
	if tc.ID != "call_client" || tc.Name != "fetch_webpage" || tc.Index != 4 {
		t.Errorf("block 4: unexpected ToolCall fields: %#v", tc)
	}
	if tc.Args["url"] != "https://golang.org" {
		t.Errorf("block 4: expected url 'https://golang.org', got %v", tc.Args["url"])
	}
	if tc.Extras["client"] != true {
		t.Errorf("block 4: expected client true, got %v", tc.Extras["client"])
	}

	// Verify Assistant helper methods
	clientCalls := msg.ToolCalls()
	if len(clientCalls) != 1 || clientCalls[0].ID != "call_client" {
		t.Errorf("msg.ToolCalls() should only return client tool calls, got %#v", clientCalls)
	}

	serverCalls := msg.ServerToolCalls()
	if len(serverCalls) != 1 || serverCalls[0].ID != "call_search" {
		t.Errorf("msg.ServerToolCalls() should only return server tool calls, got %#v", serverCalls)
	}

	serverResults := msg.ServerToolResults()
	if len(serverResults) != 1 || serverResults[0].ToolCallID != "call_search" {
		t.Errorf("msg.ServerToolResults() should only return server tool results, got %#v", serverResults)
	}
}

func TestAggregatorServerToolSharedPointerIsolation(t *testing.T) {
	chunks := []AssistantChunk{
		{
			Content: []Block{
				&ServerToolCallChunk{
					Index:     1,
					ID:        "call_1",
					Name:      "web_search",
					ArgsChunk: `{"query": "foo"`,
					Extras:    map[string]any{"k": "v1"},
				},
				&ServerToolResult{
					ToolCallID: "call_1",
					Name:       "web_search",
					Content: Content{
						&ToolReferenceBlock{
							ToolName: "deferred_tool",
							Extras:   map[string]any{"ref": "val"},
						},
					},
					Output: map[string]any{"data": "original"},
					Extras: map[string]any{"extra_key": "orig"},
				},
			},
		},
		{
			Content: []Block{
				&ServerToolCallChunk{
					Index:     1,
					ArgsChunk: `}`,
					Extras:    map[string]any{"k2": "v2"},
				},
			},
		},
	}

	agg1 := NewAssistantAggregator()
	agg2 := NewAssistantAggregator()

	for i := range chunks {
		agg1.Add(&chunks[i])
		agg2.Add(&chunks[i])
	}

	msg1, err := agg1.Build()
	if err != nil {
		t.Fatalf("agg1.Build failed: %v", err)
	}
	msg2, err := agg2.Build()
	if err != nil {
		t.Fatalf("agg2.Build failed: %v", err)
	}

	// Mutate msg1's server tool call extras and args
	stc1 := msg1.ServerToolCalls()[0]
	stc2 := msg2.ServerToolCalls()[0]
	if stc1 == stc2 {
		t.Error("expected different ServerToolCall pointers between aggregators")
	}
	stc1.Extras["k"] = "mutated"
	if stc2.Extras["k"] != "v1" {
		t.Errorf("ServerToolCall.Extras mutated in sibling aggregator: got %v", stc2.Extras["k"])
	}

	// Mutate msg1's server tool result extras and output
	str1 := msg1.ServerToolResults()[0]
	str2 := msg2.ServerToolResults()[0]
	if str1 == str2 {
		t.Error("expected different ServerToolResult pointers between aggregators")
	}
	str1.Extras["extra_key"] = "mutated"
	if str2.Extras["extra_key"] != "orig" {
		t.Errorf("ServerToolResult.Extras mutated in sibling aggregator: got %v", str2.Extras["extra_key"])
	}

	// Mutate msg1's nested ToolReferenceBlock extras
	trb1 := str1.Content[0].(*ToolReferenceBlock)
	trb2 := str2.Content[0].(*ToolReferenceBlock)
	if trb1 == trb2 {
		t.Error("expected different ToolReferenceBlock pointers between aggregators")
	}
	trb1.Extras["ref"] = "mutated"
	if trb2.Extras["ref"] != "val" {
		t.Errorf("ToolReferenceBlock.Extras mutated in sibling aggregator: got %v", trb2.Extras["ref"])
	}
}

func TestContentServerToolJSONRoundTrip(t *testing.T) {
	origContent := Content{
		&ThinkingBlock{Thinking: "reasoning..."},
		&ServerToolCall{
			ID:     "call_srv_1",
			Name:   "code_execution",
			Index:  1,
			Args:   map[string]any{"cmd": "echo hi"},
			Extras: map[string]any{"env": "sandbox"},
		},
		&ServerToolResult{
			ToolCallID: "call_srv_1",
			Name:       "code_execution",
			Content: Content{
				&TextBlock{Text: "Execution completed."},
				&ToolReferenceBlock{
					ToolName: "custom_chart",
					Extras:   map[string]any{"version": "1.0"},
				},
			},
			Output:  map[string]any{"stdout": "hi\n"},
			IsError: false,
			Extras:  map[string]any{"duration_ms": float64(42)},
		},
		&ToolReferenceBlock{
			ToolName: "top_level_ref",
			Extras:   map[string]any{"scope": "turn"},
		},
		&ToolCall{
			ID:     "call_cl_1",
			Name:   "display_chart",
			Index:  2,
			Args:   map[string]any{"data": "sample"},
			Extras: map[string]any{"theme": "dark"},
		},
	}

	data, err := json.Marshal(origContent)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded Content
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if len(decoded) != len(origContent) {
		t.Fatalf("length mismatch: got %d, want %d", len(decoded), len(origContent))
	}

	// Block 0: ThinkingBlock
	if tb, ok := decoded[0].(*ThinkingBlock); !ok || tb.Thinking != "reasoning..." {
		t.Errorf("decoded block 0: got %#v", decoded[0])
	}

	// Block 1: ServerToolCall
	stc, ok := decoded[1].(*ServerToolCall)
	if !ok || stc.ID != "call_srv_1" || stc.Name != "code_execution" || stc.Index != 1 {
		t.Errorf("decoded block 1: got %#v", decoded[1])
	}
	if stc.Args["cmd"] != "echo hi" || stc.Extras["env"] != "sandbox" {
		t.Errorf("decoded block 1 args/extras: got %#v", stc)
	}

	// Block 2: ServerToolResult
	str, ok := decoded[2].(*ServerToolResult)
	if !ok || str.ToolCallID != "call_srv_1" || str.Name != "code_execution" || str.IsError != false {
		t.Errorf("decoded block 2: got %#v", decoded[2])
	}
	if len(str.Content) != 2 {
		t.Fatalf("decoded block 2 content length: got %d, want 2", len(str.Content))
	}
	if txt, ok := str.Content[0].(*TextBlock); !ok || txt.Text != "Execution completed." {
		t.Errorf("decoded block 2 content[0]: got %#v", str.Content[0])
	}
	if trb, ok := str.Content[1].(*ToolReferenceBlock); !ok || trb.ToolName != "custom_chart" || trb.Extras["version"] != "1.0" {
		t.Errorf("decoded block 2 content[1]: got %#v", str.Content[1])
	}
	if str.Extras["duration_ms"] != float64(42) {
		t.Errorf("decoded block 2 extras: got %v", str.Extras["duration_ms"])
	}

	// Block 3: ToolReferenceBlock
	trb, ok := decoded[3].(*ToolReferenceBlock)
	if !ok || trb.ToolName != "top_level_ref" || trb.Extras["scope"] != "turn" {
		t.Errorf("decoded block 3: got %#v", decoded[3])
	}

	// Block 4: ToolCall
	tc, ok := decoded[4].(*ToolCall)
	if !ok || tc.ID != "call_cl_1" || tc.Name != "display_chart" || tc.Index != 2 {
		t.Errorf("decoded block 4: got %#v", decoded[4])
	}
	if tc.Args["data"] != "sample" || tc.Extras["theme"] != "dark" {
		t.Errorf("decoded block 4 args/extras: got %#v", tc)
	}
}
