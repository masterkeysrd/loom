package tool

import (
	"context"
	"fmt"

	"github.com/masterkeysrd/loom/internal/collection"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/stream"
)

type Container struct {
	tools *collection.OrderedMap[string, *Tool]
}

func NewContainer(tools ...*Tool) *Container {
	m := collection.NewOrderedMap[string, *Tool]()
	for _, tool := range tools {
		m.Set(tool.Definition.Name, tool)
	}

	return &Container{tools: m}
}

func (c *Container) AddTools(tool ...*Tool) {
	for _, t := range tool {
		c.tools.Set(t.Definition.Name, t)
	}
}

func (c *Container) ListTools() []*Tool {
	tools := make([]*Tool, 0, c.tools.Len())
	for tool := range c.tools.Values() {
		tools = append(tools, tool)
	}
	return tools
}

func (c *Container) Definitions() []Definition {
	defs := make([]Definition, 0, c.tools.Len())
	for tool := range c.tools.Values() {
		defs = append(defs, tool.Definition)
	}
	return defs
}

func (c *Container) Stream(ctx context.Context, tc *message.ToolCall) (ToolStream, error) {
	t, exists := c.tools.Get(tc.Name)
	if !exists {
		return nil, fmt.Errorf("tool %q: %w", tc.Name, ErrToolNotFound)
	}

	streamer, err := t.Handler(ctx, tc)
	if err != nil {
		return nil, err
	}

	sw, hasWriter := stream.WriterFromContext(ctx)
	if hasWriter {
		ctx = stream.WithMetadata(ctx, stream.Metadata{Source: "tool:" + tc.Name})
	}

	return func(yield func(message.ToolChunk, error) bool) {
		for chunk, err := range streamer {
			if hasWriter && err == nil {
				_ = sw.Write(ctx, chunk)
			}
			if !yield(chunk, err) {
				return
			}
		}
	}, nil
}

func (c *Container) Call(ctx context.Context, tc *message.ToolCall) (*message.Tool, error) {
	stream, err := c.Stream(ctx, tc)
	if err != nil {
		return nil, err
	}

	var content message.Content
	var structured any
	var isError bool
	for chunk, err := range stream {
		if err != nil {
			return nil, err
		}
		if len(chunk.Content) > 0 {
			content = append(content, chunk.Content...)
		}
		if chunk.StructuredContent != nil {
			structured = chunk.StructuredContent
		}
		if chunk.IsError {
			isError = true
		}
	}

	return &message.Tool{
		ToolCallID:        tc.ID,
		Name:              tc.Name,
		Content:           content,
		StructuredContent: structured,
		IsError:           isError,
	}, nil
}
