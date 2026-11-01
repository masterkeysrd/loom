package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/masterkeysrd/loom/message"
)

type dummyInput struct {
	Location string `json:"location" jsonschema:"The city and state, e.g. San Francisco, CA"`
}

func TestWithDeferLoading(t *testing.T) {
	// 1. Functional option
	tl, err := New("weather", "Weather", "Get weather", func(ctx context.Context, in dummyInput) (string, error) {
		return "sunny", nil
	}, WithDeferLoading())
	if err != nil {
		t.Fatalf("failed to create tool: %v", err)
	}
	if !tl.Definition.DeferLoading {
		t.Errorf("expected DeferLoading to be true")
	}

	// 2. Method WithDeferLoading(false) returns copy
	tlNonDeferred := tl.WithDeferLoading(false)
	if tlNonDeferred.Definition.DeferLoading {
		t.Errorf("expected copied tool DeferLoading to be false")
	}
	// Original unchanged
	if !tl.Definition.DeferLoading {
		t.Errorf("expected original tool DeferLoading to remain true")
	}

	// 3. Method WithDeferLoading(true)
	tlDeferredAgain := tlNonDeferred.WithDeferLoading(true)
	if !tlDeferredAgain.Definition.DeferLoading {
		t.Errorf("expected copied tool DeferLoading to be true")
	}
}

func TestAdaptHandler_ToolReferenceBlocks(t *testing.T) {
	// Test []message.ToolReferenceBlock
	searchTool1, err := New("search_val", "Search", "Search tools", func(ctx context.Context, in map[string]any) ([]message.ToolReferenceBlock, error) {
		return []message.ToolReferenceBlock{
			{ToolName: "calc", Extras: map[string]any{"score": 1.0}},
			{ToolName: "fetch"},
		}, nil
	})
	if err != nil {
		t.Fatalf("failed to create tool: %v", err)
	}

	c1 := NewContainer(searchTool1)
	res1, err := c1.Call(context.Background(), &message.ToolCall{
		Name: "search_val",
		Args: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	if len(res1.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(res1.Content))
	}
	trb1, ok := res1.Content[0].(*message.ToolReferenceBlock)
	if !ok || trb1.ToolName != "calc" {
		t.Errorf("expected *message.ToolReferenceBlock with 'calc', got %#v", res1.Content[0])
	}

	// Test []*message.ToolReferenceBlock with a nil element filtered out
	searchTool2, err := New("search_ptr", "Search", "Search tools", func(ctx context.Context, in map[string]any) ([]*message.ToolReferenceBlock, error) {
		return []*message.ToolReferenceBlock{
			{ToolName: "plot"},
			nil,
			{ToolName: "chart"},
		}, nil
	})
	if err != nil {
		t.Fatalf("failed to create tool: %v", err)
	}

	c2 := NewContainer(searchTool2)
	res2, err := c2.Call(context.Background(), &message.ToolCall{
		Name: "search_ptr",
		Args: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	if len(res2.Content) != 2 {
		t.Fatalf("expected 2 content blocks (nil skipped), got %d", len(res2.Content))
	}
	trb2, ok := res2.Content[1].(*message.ToolReferenceBlock)
	if !ok || trb2.ToolName != "chart" {
		t.Errorf("expected *message.ToolReferenceBlock with 'chart', got %#v", res2.Content[1])
	}
}

func TestNewBM25Search(t *testing.T) {
	weatherTool, _ := New("get_weather", "Weather Forecast", "Retrieve current temperature and conditions for a given city", func(ctx context.Context, in dummyInput) (string, error) {
		return "sunny", nil
	})
	dbTool, _ := New("query_database", "Database Query", "Execute SQL queries against the analytical database", func(ctx context.Context, in map[string]any) (string, error) {
		return "rows", nil
	})
	emailTool, _ := New("send_email", "Email Client", "Send an email message with attachments to recipients", func(ctx context.Context, in map[string]any) (string, error) {
		return "sent", nil
	})

	searchTool, err := NewBM25Search([]*Tool{weatherTool, nil, dbTool, emailTool},
		WithSearchName("custom_search"),
		WithSearchDescription("Custom search tool"),
		WithSearchTopK(2),
	)
	if err != nil {
		t.Fatalf("NewBM25Search failed: %v", err)
	}

	// Verify metadata and annotations
	if searchTool.Definition.Name != "custom_search" {
		t.Errorf("expected name 'custom_search', got %q", searchTool.Definition.Name)
	}
	if searchTool.Definition.Description != "Custom search tool" {
		t.Errorf("expected description 'Custom search tool', got %q", searchTool.Definition.Description)
	}
	if !searchTool.Annotation.IsReadOnly || !searchTool.Annotation.IsIdempotent {
		t.Errorf("expected IsReadOnly and IsIdempotent annotations to be true")
	}

	c := NewContainer(searchTool)

	// 1. Search for weather
	res, err := c.Call(context.Background(), &message.ToolCall{
		Name: "custom_search",
		Args: map[string]any{"query": "weather forecast city"},
	})
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("expected at least 1 match for weather, got 0")
	}
	firstMatch, ok := res.Content[0].(*message.ToolReferenceBlock)
	if !ok || firstMatch.ToolName != "get_weather" {
		t.Errorf("expected first match to be 'get_weather', got %#v", res.Content[0])
	}

	// 2. Search for SQL
	res, err = c.Call(context.Background(), &message.ToolCall{
		Name: "custom_search",
		Args: map[string]any{"query": "sql database"},
	})
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("expected at least 1 match for sql, got 0")
	}
	firstMatch, ok = res.Content[0].(*message.ToolReferenceBlock)
	if !ok || firstMatch.ToolName != "query_database" {
		t.Errorf("expected first match to be 'query_database', got %#v", res.Content[0])
	}

	// 3. Search with no matches
	res, err = c.Call(context.Background(), &message.ToolCall{
		Name: "custom_search",
		Args: map[string]any{"query": "astronomy spaceship mars rover"},
	})
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}
	if len(res.Content) != 0 {
		t.Errorf("expected 0 matches, got %d", len(res.Content))
	}
}

func TestNewRegexSearch(t *testing.T) {
	weatherTool, _ := New("get_weather", "Weather Forecast", "Retrieve current temperature and conditions", func(ctx context.Context, in dummyInput) (string, error) {
		return "sunny", nil
	})
	dbTool, _ := New("query_database", "Database Query", "Execute SQL queries against the analytical database", func(ctx context.Context, in map[string]any) (string, error) {
		return "rows", nil
	})

	regexTool, err := NewRegexSearch([]*Tool{weatherTool, dbTool}, WithSearchTopK(5))
	if err != nil {
		t.Fatalf("NewRegexSearch failed: %v", err)
	}

	if !regexTool.Annotation.IsReadOnly || !regexTool.Annotation.IsIdempotent {
		t.Errorf("expected IsReadOnly and IsIdempotent to be true")
	}

	c := NewContainer(regexTool)

	// 1. Case-insensitive search match
	res, err := c.Call(context.Background(), &message.ToolCall{
		Name: "tool_search_regex",
		Args: map[string]any{"pattern": "WEATHER"},
	})
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", res.Content.Text())
	}
	if len(res.Content) != 1 {
		t.Fatalf("expected 1 match, got %d", len(res.Content))
	}
	ref, ok := res.Content[0].(*message.ToolReferenceBlock)
	if !ok || ref.ToolName != "get_weather" {
		t.Errorf("expected 'get_weather', got %#v", res.Content[0])
	}

	// 2. Pattern exceeding 200 chars returns error
	longPattern := strings.Repeat("a", 201)
	res, err = c.Call(context.Background(), &message.ToolCall{
		Name: "tool_search_regex",
		Args: map[string]any{"pattern": longPattern},
	})
	if err != nil {
		t.Fatalf("unexpected call failure: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true for pattern > 200 chars")
	}
	if !strings.Contains(res.Content.Text(), "200 characters") {
		t.Errorf("expected error message mentioning 200 characters, got %q", res.Content.Text())
	}

	// 3. Invalid regex syntax returns error
	res, err = c.Call(context.Background(), &message.ToolCall{
		Name: "tool_search_regex",
		Args: map[string]any{"pattern": "[unclosed("},
	})
	if err != nil {
		t.Fatalf("unexpected call failure: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true for invalid regex")
	}
	if !strings.Contains(res.Content.Text(), "invalid regular expression") {
		t.Errorf("expected error message mentioning invalid regular expression, got %q", res.Content.Text())
	}
}

func TestResolveActiveDefinitions(t *testing.T) {
	defs := []Definition{
		{Name: "always_active", DeferLoading: false},
		{Name: "deferred_1", DeferLoading: true},
		{Name: "deferred_2", DeferLoading: true},
		{Name: "deferred_3", DeferLoading: true},
	}

	// 1. Initial state with no messages: only always_active
	active := ResolveActiveDefinitions(defs, nil)
	if len(active) != 1 || active[0].Name != "always_active" {
		t.Errorf("expected only always_active, got %#v", active)
	}

	// 2. Message with message.Tool referencing tool by Name
	msgs := []message.Message{
		&message.Tool{
			Name: "deferred_1",
		},
	}
	active = ResolveActiveDefinitions(defs, msgs)
	if len(active) != 2 || active[0].Name != "always_active" || active[1].Name != "deferred_1" {
		t.Errorf("expected always_active and deferred_1, got %#v", active)
	}

	// 3. Message with message.Tool containing ToolReferenceBlock in Content
	msgs = append(msgs, &message.Tool{
		Name: "tool_search",
		Content: message.Content{
			&message.ToolReferenceBlock{ToolName: "deferred_2"},
		},
	})
	active = ResolveActiveDefinitions(defs, msgs)
	if len(active) != 3 || active[1].Name != "deferred_1" || active[2].Name != "deferred_2" {
		t.Errorf("expected always_active, deferred_1, deferred_2, got %#v", active)
	}

	// 4. Message with Assistant containing ServerToolResult with nested ToolReferenceBlock
	msgs = append(msgs, &message.Assistant{
		Content: message.Content{
			&message.ServerToolResult{
				ToolCallID: "srv-1",
				Name:       "web_search",
				Content: message.Content{
					&message.ToolReferenceBlock{ToolName: "deferred_3"},
				},
			},
		},
	})
	active = ResolveActiveDefinitions(defs, msgs)
	if len(active) != 4 {
		t.Fatalf("expected all 4 tools to be active, got %d: %#v", len(active), active)
	}
	if active[3].Name != "deferred_3" {
		t.Errorf("expected deferred_3 to be active, got %s", active[3].Name)
	}

	// 5. ToolCall block in content activates deferred tool
	msgsToolCall := []message.Message{
		&message.Assistant{
			Content: message.Content{
				&message.ToolCall{Name: "deferred_1"},
			},
		},
	}
	active = ResolveActiveDefinitions(defs, msgsToolCall)
	if len(active) != 2 || active[1].Name != "deferred_1" {
		t.Errorf("expected deferred_1 to be active from ToolCall block, got %#v", active)
	}
}
