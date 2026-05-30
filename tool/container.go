package tool

import (
	"context"
	"fmt"

	"github.com/masterkeysrd/loom/internal/collection"
	"github.com/masterkeysrd/loom/message"
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

func (c *Container) Call(ctx context.Context, tc *message.ToolCall) (*message.Tool, error) {
	tool, exists := c.tools.Get(tc.Name)
	if !exists {
		return &message.Tool{
			ToolCallID: tc.ID,
			Name:       tc.Name,
			Content:    message.Content{&message.TextBlock{Text: fmt.Sprintf("tool %q not found", tc.Name)}},
		}, nil
	}

	resp, err := tool.Handler(ctx, tc)
	if err != nil {
		return &message.Tool{
			ToolCallID: tc.ID,
			Name:       tc.Name,
			Content:    message.Content{&message.TextBlock{Text: fmt.Sprintf("error executing tool %q: %v", tc.Name, err)}},
		}, nil
	}

	return resp, nil
}
