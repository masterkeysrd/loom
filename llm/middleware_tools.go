package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/tool"
)

// DeferredToolsOptions configures [DeferredToolsMiddleware].
type DeferredToolsOptions struct {
	// Match overrides the default provider capability check when non-nil.
	// If Match returns true, the middleware transforms the request (filtering out
	// deferred tools from req.Tools and expanding ToolReferenceBlocks into schema text blocks).
	// If nil, the middleware runs whenever the provider does not implement
	// [DeferredToolsSupport] or SupportsDeferredTools(req.Model) returns false.
	Match func(ctx context.Context, req *Request) bool

	// FormatReference optionally customizes how a referenced tool definition is
	// rendered into a [message.Block] when replacing a [message.ToolReferenceBlock].
	// If nil, a default JSON block with name, description, and input_schema is used.
	FormatReference func(def tool.Definition) message.Block

	// DisableCoercion disables schema-based type coercion of stringified tool call
	// arguments for deferred tools. By default (false), when a provider returns a
	// [*message.ToolCall] for a deferred tool, string argument values are coerced
	// according to the tool's InputSchema (e.g., for XML tool parsers like Ollama's
	// that lack the deferred tool's schema in req.Tools).
	DisableCoercion bool
}

// DeferredToolsMiddleware returns a [Middleware] that handles deferred tools for providers
// that do not natively support deferred loading.
//
// When active (by default on providers without native deferred tool support for the requested model):
//  1. Filters out all tools marked with DeferLoading: true from req.Tools, keeping req.Tools
//     static across turns to preserve the prompt cache prefix.
//  2. Clones messages containing ToolReferenceBlocks and expands each reference in-place
//     into a schema text block (or a custom block formatted by opts.FormatReference).
//  3. Coerces stringified arguments returned by the provider for deferred tools into their
//     typed counterparts based on the tool's InputSchema (unless opts.DisableCoercion is true).
//  4. Leaves the caller's original *Request and Messages slice unmutated.
func DeferredToolsMiddleware(opts *DeferredToolsOptions) Middleware {
	return func(next Streamer) Streamer {
		return func(ctx context.Context, req *Request) (StreamResponse, error) {
			shouldTransform := true
			if opts != nil && opts.Match != nil {
				shouldTransform = opts.Match(ctx, req)
			} else if rt, ok := RuntimeFromContext(ctx); ok && rt.SupportsDeferredTools(req.Model) {
				shouldTransform = false
			}

			if !shouldTransform {
				return next(ctx, req)
			}

			// 1. Build lookup map from req.Tools
			toolsByName := make(map[string]tool.Definition, len(req.Tools))
			for _, t := range req.Tools {
				toolsByName[t.Name] = t
			}

			// 2. Filter req.Tools: keep ONLY !t.DeferLoading (activeTools)
			var activeTools []tool.Definition
			for _, t := range req.Tools {
				if !t.DeferLoading {
					activeTools = append(activeTools, t)
				}
			}

			// 3. Clone req.Messages and replace ToolReferenceBlocks in place
			msgs := slices.Clone(req.Messages)
			for i, msg := range msgs {
				if hasToolReference(msg) {
					cloned := message.CloneMessage(msg)
					switch m := cloned.(type) {
					case *message.System:
						m.Content = transformContent(m.Content, toolsByName, opts)
					case *message.User:
						m.Content = transformContent(m.Content, toolsByName, opts)
					case *message.Assistant:
						m.Content = transformContent(m.Content, toolsByName, opts)
					case *message.Tool:
						m.Content = transformContent(m.Content, toolsByName, opts)
					}
					msgs[i] = cloned
				}
			}

			// 4. Pass shallow copy of req to next
			reqCopy := *req
			reqCopy.Tools = activeTools
			reqCopy.Messages = msgs

			stream, err := next(ctx, &reqCopy)
			if err != nil || (opts != nil && opts.DisableCoercion) {
				return stream, err
			}

			return func(yield func(message.AssistantChunk, error) bool) {
				for chunk, err := range stream {
					if err != nil {
						if !yield(chunk, err) {
							return
						}
						continue
					}
					for _, b := range chunk.Content {
						if tc, ok := b.(*message.ToolCall); ok {
							if def, ok := toolsByName[tc.Name]; ok && def.DeferLoading && def.InputSchema != nil {
								tc.Args = coerceMapArgs(tc.Args, def.InputSchema)
							}
						}
					}
					if !yield(chunk, nil) {
						return
					}
				}
			}, nil
		}
	}
}

// InlineToolsOptions configures [InlineToolsMiddleware].
type InlineToolsOptions struct {
	// Match overrides the default provider capability check when non-nil.
	// If Match returns true, the middleware transforms the request (filtering out
	// tools in inlineDefs from req.Tools and expanding ToolDefinitionBlocks into schema text blocks).
	// If nil, the middleware runs whenever the provider does not implement
	// [InlineToolsSupport] or SupportsInlineTools(req.Model) returns false.
	Match func(ctx context.Context, req *Request) bool

	// FormatDefinition optionally customizes how an inline tool definition is
	// rendered into a [message.Block] when replacing a [message.ToolDefinitionBlock].
	// If nil, a default JSON block with name, description, and input_schema is used.
	FormatDefinition func(def tool.Definition) message.Block

	// DisableCoercion disables schema-based type coercion of stringified tool call
	// arguments for inline tools. By default (false), when a provider returns a
	// [*message.ToolCall] for an inline tool, string argument values are coerced
	// according to the tool's InputSchema.
	DisableCoercion bool
}

// InlineToolsMiddleware returns a [Middleware] that handles mid-conversation inline
// tool definitions ([message.ToolDefinitionBlock]) for providers that do not natively
// support inline tool injection.
//
// When active (by default on providers without native inline tool support for the requested model):
//  1. Scans req.Messages for all ToolDefinitionBlocks and collects inline tool definitions.
//  2. Filters out any tool from req.Tools whose Name matches an inline tool, keeping
//     req.Tools static and cache-safe.
//  3. Clones messages containing ToolDefinitionBlocks and replaces each block in-place
//     with a schema text block (or a custom block formatted by opts.FormatDefinition).
//  4. Coerces stringified arguments returned by the provider for inline tools into their
//     typed counterparts based on the tool's InputSchema (unless opts.DisableCoercion is true).
//  5. Leaves the caller's original *Request and Messages slice unmutated.
func InlineToolsMiddleware(opts *InlineToolsOptions) Middleware {
	return func(next Streamer) Streamer {
		return func(ctx context.Context, req *Request) (StreamResponse, error) {
			shouldTransform := true
			if opts != nil && opts.Match != nil {
				shouldTransform = opts.Match(ctx, req)
			} else if rt, ok := RuntimeFromContext(ctx); ok && rt.SupportsInlineTools(req.Model) {
				shouldTransform = false
			}

			if !shouldTransform {
				return next(ctx, req)
			}

			// 1. Scan req.Messages for all ToolDefinitionBlocks
			inlineDefs := make(map[string]tool.Definition)
			for _, msg := range req.Messages {
				collectToolDefinitions(msg, inlineDefs)
			}

			if len(inlineDefs) == 0 {
				return next(ctx, req)
			}

			// 2. Filter req.Tools: omit tools that are provided inline
			var filteredTools []tool.Definition
			for _, t := range req.Tools {
				if _, isInline := inlineDefs[t.Name]; !isInline {
					filteredTools = append(filteredTools, t)
				}
			}

			// 3. Clone req.Messages and replace ToolDefinitionBlocks in place
			msgs := slices.Clone(req.Messages)
			for i, msg := range msgs {
				if hasToolDefinition(msg) {
					cloned := message.CloneMessage(msg)
					switch m := cloned.(type) {
					case *message.System:
						m.Content = transformInlineContent(m.Content, inlineDefs, opts)
					case *message.User:
						m.Content = transformInlineContent(m.Content, inlineDefs, opts)
					case *message.Assistant:
						m.Content = transformInlineContent(m.Content, inlineDefs, opts)
					case *message.Tool:
						m.Content = transformInlineContent(m.Content, inlineDefs, opts)
					}
					msgs[i] = cloned
				}
			}

			// 4. Pass shallow copy of req to next
			reqCopy := *req
			reqCopy.Tools = filteredTools
			reqCopy.Messages = msgs

			stream, err := next(ctx, &reqCopy)
			if err != nil || (opts != nil && opts.DisableCoercion) {
				return stream, err
			}

			return func(yield func(message.AssistantChunk, error) bool) {
				for chunk, err := range stream {
					if err != nil {
						if !yield(chunk, err) {
							return
						}
						continue
					}
					for _, b := range chunk.Content {
						if tc, ok := b.(*message.ToolCall); ok {
							if def, ok := inlineDefs[tc.Name]; ok && def.InputSchema != nil {
								tc.Args = coerceMapArgs(tc.Args, def.InputSchema)
							}
						}
					}
					if !yield(chunk, nil) {
						return
					}
				}
			}, nil
		}
	}
}

func collectToolDefinitions(msg message.Message, defs map[string]tool.Definition) {
	if msg == nil {
		return
	}
	for _, b := range msg.GetContent() {
		collectToolDefinitionsFromBlock(b, defs)
	}
}

func collectToolDefinitionsFromBlock(b message.Block, defs map[string]tool.Definition) {
	switch block := b.(type) {
	case *message.ToolDefinitionBlock:
		defs[block.Name] = tool.Definition{
			Name:        block.Name,
			Description: block.Description,
			InputSchema: block.InputSchema,
		}
	case *message.ServerToolResult:
		for _, sub := range block.Content {
			collectToolDefinitionsFromBlock(sub, defs)
		}
	}
}

func hasToolDefinition(msg message.Message) bool {
	if msg == nil {
		return false
	}
	return slices.ContainsFunc(msg.GetContent(), blockHasToolDefinition)
}

func blockHasToolDefinition(b message.Block) bool {
	switch block := b.(type) {
	case *message.ToolDefinitionBlock:
		return true
	case *message.ServerToolResult:
		if slices.ContainsFunc(block.Content, blockHasToolDefinition) {
			return true
		}
	}
	return false
}

func transformInlineContent(content message.Content, inlineDefs map[string]tool.Definition, opts *InlineToolsOptions) message.Content {
	res := make(message.Content, len(content))
	for i, b := range content {
		res[i] = transformInlineBlock(b, inlineDefs, opts)
	}
	return res
}

func transformInlineBlock(b message.Block, inlineDefs map[string]tool.Definition, opts *InlineToolsOptions) message.Block {
	switch block := b.(type) {
	case *message.ToolDefinitionBlock:
		def, ok := inlineDefs[block.Name]
		if !ok {
			def = tool.Definition{
				Name:        block.Name,
				Description: block.Description,
				InputSchema: block.InputSchema,
			}
		}
		return formatToolDefinition(def, opts)
	case *message.ServerToolResult:
		var newNested []message.Block
		modified := false
		for _, sub := range block.Content {
			transformed := transformInlineBlock(sub, inlineDefs, opts)
			if transformed != sub {
				modified = true
			}
			newNested = append(newNested, transformed)
		}
		if modified {
			block.Content = newNested
		}
		return block
	default:
		return b
	}
}

func formatToolDefinition(def tool.Definition, opts *InlineToolsOptions) message.Block {
	if opts != nil && opts.FormatDefinition != nil {
		return opts.FormatDefinition(def)
	}
	data, _ := json.Marshal(map[string]any{
		"type":         "tool_definition",
		"name":         def.Name,
		"description":  def.Description,
		"input_schema": def.InputSchema,
		"function": map[string]any{
			"name":        def.Name,
			"description": def.Description,
			"parameters":  def.InputSchema,
		},
	})
	text := fmt.Sprintf(
		"Tool %q is now loaded, active, and callable in this session. You CAN and SHOULD invoke %q directly as a tool call right now using the following schema (even though %q is not listed in the initial <tools> header — do NOT search for %q again):\n<tools>\n%s\n</tools>\n",
		def.Name, def.Name, def.Name, def.Name, string(data),
	)
	return &message.TextBlock{Text: text}
}

func hasToolReference(msg message.Message) bool {
	if msg == nil {
		return false
	}
	return slices.ContainsFunc(msg.GetContent(), blockHasToolReference)
}

func blockHasToolReference(b message.Block) bool {
	switch block := b.(type) {
	case *message.ToolReferenceBlock:
		return true
	case *message.ServerToolResult:
		if slices.ContainsFunc(block.Content, blockHasToolReference) {
			return true
		}
	}
	return false
}

func transformContent(content message.Content, toolsByName map[string]tool.Definition, opts *DeferredToolsOptions) message.Content {
	res := make(message.Content, len(content))
	for i, b := range content {
		res[i] = transformBlock(b, toolsByName, opts)
	}
	return res
}

func transformBlock(b message.Block, toolsByName map[string]tool.Definition, opts *DeferredToolsOptions) message.Block {
	switch block := b.(type) {
	case *message.ToolReferenceBlock:
		return formatToolReference(block.ToolName, toolsByName, opts)
	case *message.ServerToolResult:
		var newNested []message.Block
		modified := false
		for _, sub := range block.Content {
			transformed := transformBlock(sub, toolsByName, opts)
			if transformed != sub {
				modified = true
			}
			newNested = append(newNested, transformed)
		}
		if modified {
			block.Content = newNested
		}
		return block
	default:
		return b
	}
}

func formatToolReference(toolName string, toolsByName map[string]tool.Definition, opts *DeferredToolsOptions) message.Block {
	def, found := toolsByName[toolName]
	if found && opts != nil && opts.FormatReference != nil {
		return opts.FormatReference(def)
	}
	if found {
		data, _ := json.Marshal(map[string]any{
			"type":         "tool_definition",
			"name":         def.Name,
			"description":  def.Description,
			"input_schema": def.InputSchema,
			"function": map[string]any{
				"name":        def.Name,
				"description": def.Description,
				"parameters":  def.InputSchema,
			},
		})
		text := fmt.Sprintf(
			"Tool %q is now loaded, active, and callable in this session. You CAN and SHOULD invoke %q directly as a tool call right now using the following schema (even though %q is not listed in the initial <tools> header — do NOT search for %q again):\n<tools>\n%s\n</tools>\n",
			def.Name, def.Name, def.Name, def.Name, string(data),
		)
		return &message.TextBlock{Text: text}
	}
	return &message.TextBlock{Text: fmt.Sprintf("{\"type\":\"tool_reference\",\"name\":%q}\n", toolName)}
}

func schemaTypes(s *jsonschema.Schema) []string {
	if s == nil {
		return nil
	}
	var types []string
	if s.Type != "" {
		types = append(types, s.Type)
	}
	if len(s.Properties) > 0 && !slices.Contains(types, "object") {
		types = append(types, "object")
	}
	if (s.Items != nil || len(s.PrefixItems) > 0) && !slices.Contains(types, "array") {
		types = append(types, "array")
	}
	for _, t := range s.Types {
		if t != "" && !slices.Contains(types, t) {
			types = append(types, t)
		}
	}
	for _, sub := range s.AnyOf {
		for _, t := range schemaTypes(sub) {
			if !slices.Contains(types, t) {
				types = append(types, t)
			}
		}
	}
	for _, sub := range s.OneOf {
		for _, t := range schemaTypes(sub) {
			if !slices.Contains(types, t) {
				types = append(types, t)
			}
		}
	}
	for _, sub := range s.AllOf {
		for _, t := range schemaTypes(sub) {
			if !slices.Contains(types, t) {
				types = append(types, t)
			}
		}
	}
	return types
}

func findPropertySchema(s *jsonschema.Schema, propName string) *jsonschema.Schema {
	if s == nil {
		return nil
	}
	if s.Properties != nil {
		if ps, ok := s.Properties[propName]; ok {
			return ps
		}
	}
	for _, sub := range s.AllOf {
		if ps := findPropertySchema(sub, propName); ps != nil {
			return ps
		}
	}
	for _, sub := range s.AnyOf {
		if ps := findPropertySchema(sub, propName); ps != nil {
			return ps
		}
	}
	for _, sub := range s.OneOf {
		if ps := findPropertySchema(sub, propName); ps != nil {
			return ps
		}
	}
	if s.AdditionalProperties != nil {
		return s.AdditionalProperties
	}
	return nil
}

func coerceMapArgs(args map[string]any, schema *jsonschema.Schema) map[string]any {
	if args == nil || schema == nil {
		return args
	}
	res := make(map[string]any, len(args))
	for k, v := range args {
		propSchema := findPropertySchema(schema, k)
		if propSchema != nil {
			res[k] = coerceValue(v, propSchema)
		} else {
			res[k] = v
		}
	}
	return res
}

func coerceValue(val any, schema *jsonschema.Schema) any {
	if schema == nil {
		return val
	}
	types := schemaTypes(schema)

	switch v := val.(type) {
	case string:
		trimmed := strings.TrimSpace(v)

		if slices.Contains(types, "null") && strings.EqualFold(trimmed, "null") {
			return nil
		}

		if slices.Contains(types, "string") {
			return val
		}

		// 1. "boolean"
		if slices.Contains(types, "boolean") {
			if b, err := strconv.ParseBool(trimmed); err == nil {
				return b
			}
		}

		// 2. "integer"
		if slices.Contains(types, "integer") {
			if i, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
				return i
			}
			if f, err := strconv.ParseFloat(trimmed, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f) && f >= math.MinInt64 && f <= math.MaxInt64 {
				return int64(f)
			}
		}

		// 3. "number"
		if slices.Contains(types, "number") {
			if f, err := strconv.ParseFloat(trimmed, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
				return f
			}
		}

		// 4. "array"
		if slices.Contains(types, "array") {
			var arr []any
			if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
				if schema.Items != nil {
					for i, item := range arr {
						arr[i] = coerceValue(item, schema.Items)
					}
				}
				return arr
			}
		}

		// 5. "object"
		if slices.Contains(types, "object") {
			var obj map[string]any
			if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
				return coerceMapArgs(obj, schema)
			}
		}

		return val

	case map[string]any:
		return coerceMapArgs(v, schema)

	case []any:
		if schema.Items != nil {
			res := make([]any, len(v))
			for i, item := range v {
				res[i] = coerceValue(item, schema.Items)
			}
			return res
		}
		return val

	case float64:
		if slices.Contains(types, "integer") && !slices.Contains(types, "number") && !math.IsNaN(v) && !math.IsInf(v, 0) && v == math.Trunc(v) && v >= math.MinInt64 && v <= math.MaxInt64 {
			return int64(v)
		}
		return val

	default:
		return val
	}
}
