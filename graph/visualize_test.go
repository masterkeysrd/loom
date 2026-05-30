package graph

import (
	"context"
	"strings"
	"testing"
)

type MockState struct{}

func (s MockState) Copy() MockState { return MockState{} }

func TestToMermaid(t *testing.T) {
	builder := New[MockState]()
	builder.AddNode("node1", NodeFunc[MockState](func(ctx context.Context, s MockState) (Command[MockState], error) {
		return nil, nil
	}))
	builder.AddNode("node2", NodeFunc[MockState](func(ctx context.Context, s MockState) (Command[MockState], error) {
		return nil, nil
	}))
	builder.AddNode("node3", NodeFunc[MockState](func(ctx context.Context, s MockState) (Command[MockState], error) {
		return nil, nil
	}))

	builder.AddEdge(START, "node1")
	builder.AddEdge("node1", "node2")
	builder.AddConditionalEdge("node2", "node3", func(s MockState) bool { return true })
	builder.AddRouteEdge("node3", func(s MockState) (string, error) { return "a", nil }, map[string]string{
		"a": "node1",
		"b": END,
	})

	g, _ := builder.Build()
	mermaid := g.ToMermaid()

	expectedLines := []string{
		"graph TD",
		"  __END__((END))",
		"  __START__((START))",
		"  node1[node1]",
		"  node2[node2]",
		"  node3[node3]",
		"  __START__ --> node1",
		"  node1 --> node2",
		"  node2 -. [conditional] .-> node3",
		"  node3 -- a --> node1",
		"  node3 -- b --> __END__",
	}

	for _, line := range expectedLines {
		if !strings.Contains(mermaid, line) {
			t.Errorf("expected mermaid to contain %q, but it didn't.\nGot:\n%s", line, mermaid)
		}
	}
}
