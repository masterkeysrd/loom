package tool

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/internal/bm25"
	"github.com/masterkeysrd/loom/message"
)

type searchConfig struct {
	name        string
	description string
	topK        int
}

// SearchOption configures a tool search instance.
type SearchOption func(*searchConfig)

// WithSearchName overrides the default name of the tool search tool.
func WithSearchName(name string) SearchOption {
	return func(c *searchConfig) {
		c.name = name
	}
}

// WithSearchDescription overrides the default description of the tool search tool.
func WithSearchDescription(desc string) SearchOption {
	return func(c *searchConfig) {
		c.description = desc
	}
}

// WithSearchTopK sets the maximum number of tools to return in search results.
func WithSearchTopK(k int) SearchOption {
	return func(c *searchConfig) {
		c.topK = k
	}
}

// BM25SearchInput is the input schema for the BM25 keyword tool search.
type BM25SearchInput struct {
	Query string `json:"query" jsonschema:"Search query terms to find relevant tools"`
}

// NewBM25Search creates a tool that searches available tools using Okapi BM25
// keyword matching and returns matching tools as [message.ToolReferenceBlock]s.
func NewBM25Search(tools []*Tool, opts ...SearchOption) (*Tool, error) {
	cfg := &searchConfig{
		name:        "tool_search_bm25",
		description: "Searches for available tools using BM25 keyword matching and loads matching tools into context.",
		topK:        5,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.topK <= 0 {
		cfg.topK = 5
	}

	idx := bm25.New[*Tool]()
	for _, t := range tools {
		if t == nil {
			continue
		}
		var sb strings.Builder
		sb.WriteString(t.Definition.Name)
		sb.WriteString(" ")
		sb.WriteString(t.Definition.Title)
		sb.WriteString(" ")
		sb.WriteString(t.Definition.Description)
		extractSchemaText(t.Definition.InputSchema, &sb)

		idx.Add(sb.String(), t)
	}

	handler := func(ctx context.Context, in BM25SearchInput) ([]message.ToolReferenceBlock, error) {
		results := idx.SearchSorted(in.Query, nil)

		res := make([]message.ToolReferenceBlock, 0, cfg.topK)
		seen := make(map[string]bool)
		for _, r := range results {
			name := r.Item.Definition.Name
			if !seen[name] {
				seen[name] = true
				res = append(res, message.ToolReferenceBlock{
					ToolName: name,
				})
				if len(res) >= cfg.topK {
					break
				}
			}
		}
		return res, nil
	}

	return New(
		cfg.name,
		"Tool Search (BM25)",
		cfg.description,
		handler,
		WithAnnotation(Annotation{
			IsReadOnly:   true,
			IsIdempotent: true,
		}),
	)
}

func extractSchemaText(s *jsonschema.Schema, sb *strings.Builder) {
	if s == nil {
		return
	}
	if s.Description != "" {
		sb.WriteString(" ")
		sb.WriteString(s.Description)
	}
	if s.Title != "" {
		sb.WriteString(" ")
		sb.WriteString(s.Title)
	}
	for name, prop := range s.Properties {
		sb.WriteString(" ")
		sb.WriteString(name)
		if prop != nil {
			extractSchemaText(prop, sb)
		}
	}
}

// RegexSearchInput is the input schema for regex-based tool search.
type RegexSearchInput struct {
	Pattern string `json:"pattern" jsonschema:"Regular expression pattern to match against tool names and descriptions"`
}

// NewRegexSearch creates a tool that searches available tools using a regular
// expression pattern against tool names, titles, and descriptions, returning
// matching tools as [message.ToolReferenceBlock]s.
func NewRegexSearch(tools []*Tool, opts ...SearchOption) (*Tool, error) {
	cfg := &searchConfig{
		name:        "tool_search_regex",
		description: "Searches for available tools using a regular expression pattern and loads matching tools into context.",
		topK:        5,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.topK <= 0 {
		cfg.topK = 5
	}

	var validTools []*Tool
	for _, t := range tools {
		if t != nil {
			validTools = append(validTools, t)
		}
	}

	handler := func(ctx context.Context, in RegexSearchInput) ([]message.ToolReferenceBlock, error) {
		if len(in.Pattern) > 200 {
			return nil, NewError("pattern exceeds maximum length of 200 characters")
		}

		pattern := in.Pattern
		if !strings.HasPrefix(pattern, "(?i)") {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, NewError(fmt.Sprintf("invalid regular expression: %v", err))
		}

		results := make([]message.ToolReferenceBlock, 0, cfg.topK)
		seen := make(map[string]bool)
		for _, t := range validTools {
			if re.MatchString(t.Definition.Name) || re.MatchString(t.Definition.Title) || re.MatchString(t.Definition.Description) {
				if !seen[t.Definition.Name] {
					seen[t.Definition.Name] = true
					results = append(results, message.ToolReferenceBlock{
						ToolName: t.Definition.Name,
					})
					if len(results) >= cfg.topK {
						break
					}
				}
			}
		}
		return results, nil
	}

	return New(
		cfg.name,
		"Tool Search (Regex)",
		cfg.description,
		handler,
		WithAnnotation(Annotation{
			IsReadOnly:   true,
			IsIdempotent: true,
		}),
	)
}

// ResolveActiveDefinitions filters defs against messages, returning only the definitions
// that are immediately active: any non-deferred tools, plus any deferred tools that have
// been loaded via [message.ToolReferenceBlock] or previously invoked in the conversation.
func ResolveActiveDefinitions(defs []Definition, messages []message.Message) []Definition {
	loaded := make(map[string]bool)
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if toolMsg, ok := msg.(*message.Tool); ok {
			if toolMsg.Name != "" {
				loaded[toolMsg.Name] = true
			}
		}
		for _, block := range msg.GetContent() {
			scanBlock(block, loaded)
		}
	}

	var active []Definition
	for _, def := range defs {
		if !def.DeferLoading || loaded[def.Name] {
			active = append(active, def)
		}
	}
	return active
}

func scanBlock(b message.Block, loaded map[string]bool) {
	if b == nil {
		return
	}
	switch block := b.(type) {
	case *message.ToolReferenceBlock:
		if block.ToolName != "" {
			loaded[block.ToolName] = true
		}
	case *message.ToolCall:
		if block.Name != "" {
			loaded[block.Name] = true
		}
	case *message.ServerToolCall:
		if block.Name != "" {
			loaded[block.Name] = true
		}
	case *message.ServerToolResult:
		if block.Name != "" {
			loaded[block.Name] = true
		}
		for _, sub := range block.Content {
			scanBlock(sub, loaded)
		}
	}
}
