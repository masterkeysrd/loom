package graph

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type MockState struct{}

func (s MockState) Copy() MockState { return MockState{} }

func TestToMermaid(t *testing.T) {
	builder := New[MockState]()
	builder.AddNode("node1", NodeFunc(func(ctx context.Context, s MockState) (Command[MockState], error) {
		return nil, nil
	}))
	builder.AddNode("node2", NodeFunc(func(ctx context.Context, s MockState) (Command[MockState], error) {
		return nil, nil
	}))
	builder.AddNode("node3", NodeFunc(func(ctx context.Context, s MockState) (Command[MockState], error) {
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

type testCustomEdge struct {
	valid       bool
	transitions []Transition
}

func (c *testCustomEdge) NextNode(s MockState) (string, error) {
	if len(c.transitions) > 0 {
		return c.transitions[0].Target, nil
	}
	return "", nil
}

func (c *testCustomEdge) Validate() error {
	if !c.valid {
		return errors.New("custom edge validation failure")
	}
	return nil
}

func (c *testCustomEdge) Transitions() []Transition {
	return c.transitions
}

func TestAddCustomEdge_VisualizationAndValidation(t *testing.T) {
	dummyNode := NodeFunc(func(ctx context.Context, s MockState) (Command[MockState], error) {
		return nil, nil
	})

	t.Run("fails when custom edge validation fails", func(t *testing.T) {
		b := New[MockState]()
		b.AddNode("n1", dummyNode)
		b.AddEdge(START, "n1")
		b.AddCustomEdge("n1", &testCustomEdge{valid: false})

		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "custom edge validation failure") {
			t.Fatalf("expected custom edge validation failure, got: %v", err)
		}
	})

	t.Run("fails when custom edge targets non-existent node", func(t *testing.T) {
		b := New[MockState]()
		b.AddNode("n1", dummyNode)
		b.AddEdge(START, "n1")
		b.AddCustomEdge("n1", &testCustomEdge{
			valid: true,
			transitions: []Transition{
				{Target: "nonexistent", Label: "go", Conditional: false},
			},
		})

		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "nonexistent") {
			t.Fatalf("expected missing target node error, got: %v", err)
		}
	})

	t.Run("visualizes custom transitions in mermaid", func(t *testing.T) {
		b := New[MockState]()
		b.AddNode("n1", dummyNode)
		b.AddNode("n2", dummyNode)
		b.AddNode("n3", dummyNode)

		b.AddEdge(START, "n1")
		b.AddCustomEdge("n1", &testCustomEdge{
			valid: true,
			transitions: []Transition{
				{Target: "n2", Label: "custom_path", Conditional: false},
				{Target: "n3", Label: "fallback_path", Conditional: true},
			},
		})
		b.AddEdge("n2", END)
		b.AddEdge("n3", END)

		g, err := b.Build()
		if err != nil {
			t.Fatalf("unexpected build error: %v", err)
		}

		mermaid := g.ToMermaid()
		expectedLines := []string{
			"n1 -- custom_path --> n2",
			"n1 -. [fallback_path] .-> n3",
		}
		for _, line := range expectedLines {
			if !strings.Contains(mermaid, line) {
				t.Errorf("expected mermaid to contain %q, but got:\n%s", line, mermaid)
			}
		}
	})
}

