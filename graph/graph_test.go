package graph_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/masterkeysrd/loom/graph"
)

type mockCheckpointer struct {
	mu          sync.Mutex
	checkpoints map[string]graph.Checkpoint
}

func (m *mockCheckpointer) Load(ctx context.Context, loc graph.Location) (*graph.Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp, ok := m.checkpoints[loc.CheckpointID]
	if !ok {
		return nil, nil // Contract: return nil, nil when not found
	}
	return &cp, nil
}

func (m *mockCheckpointer) Record(ctx context.Context, cp graph.Checkpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkpoints[cp.Location.CheckpointID] = cp
	return nil
}

type TestState struct {
	Count   int    `json:"count"`
	Message string `json:"message"`
}

func (s TestState) Copy() TestState {
	return s
}

func TestGraphMetadata(t *testing.T) {
	builder := graph.New[TestState]().WithName("test_graph")

	builder.AddNode("node1", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.Update[TestState](func(state TestState) TestState {
			state.Count = 42
			state.Message = "hello from node1"
			return state
		}), nil
	}))

	builder.AddNode("node2", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.Update[TestState](func(state TestState) TestState {
			state.Count = 100
			return state
		}), nil
	}))

	builder.AddEdge(graph.START, "node1")
	builder.AddEdge("node1", "node2")
	builder.AddEdge("node2", graph.END)

	g, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	cp := &mockCheckpointer{
		checkpoints: make(map[string]graph.Checkpoint),
	}
	g.SetCheckpointer(cp)

	ctx := context.Background()
	// Run the graph
	loc := &graph.Location{
		ThreadID:     "thread-123",
		CheckpointNS: "",
		CheckpointID: uuid.NewString(),
	}
	initialInput := graph.Update[TestState](func(s TestState) TestState {
		s.Count = 5
		return s
	})

	snap, err := g.Execute(ctx, initialInput, loc)
	if err != nil {
		t.Fatal(err)
	}

	if snap.State.Count != 100 {
		t.Errorf("expected final count 100, got %d", snap.State.Count)
	}

	// Verify checkpoints
	cp.mu.Lock()
	defer cp.mu.Unlock()

	if len(cp.checkpoints) != 3 {
		t.Fatalf("expected 3 checkpoints, got %d", len(cp.checkpoints))
	}

	var startCp, node1Cp, node2Cp *graph.Checkpoint

	// Find startCp (its parent is not in cp.checkpoints)
	for _, c := range cp.checkpoints {
		if c.Parent == nil {
			startCp = &c
			break
		}
		if _, exists := cp.checkpoints[c.Parent.CheckpointID]; !exists {
			startCp = &c
			break
		}
	}

	if startCp == nil {
		t.Fatal("start checkpoint not found")
	}

	// Find node1Cp (its Parent is startCp)
	for _, c := range cp.checkpoints {
		if c.Parent != nil && c.Parent.CheckpointID == startCp.Location.CheckpointID {
			node1Cp = &c
			break
		}
	}

	if node1Cp == nil {
		t.Fatal("node1 checkpoint not found")
	}

	// Find node2Cp (its Parent is node1Cp)
	for _, c := range cp.checkpoints {
		if c.Parent != nil && c.Parent.CheckpointID == node1Cp.Location.CheckpointID {
			node2Cp = &c
			break
		}
	}

	if node2Cp == nil {
		t.Fatal("node2 checkpoint not found")
	}

	// Verify START metadata: source should be "input", step 1, writes should reflect input Count = 5
	var startMeta graph.CheckpointMetadata
	if err := json.Unmarshal(startCp.Metadata, &startMeta); err != nil {
		t.Fatal(err)
	}
	if startMeta.Source != "input" {
		t.Errorf("start checkpoint: expected source 'input', got %q", startMeta.Source)
	}
	if startMeta.Step != 1 {
		t.Errorf("start checkpoint: expected step 1, got %d", startMeta.Step)
	}
	expectedStartWrites := map[string]any{"count": float64(5)}
	if !reflect.DeepEqual(startMeta.Writes, expectedStartWrites) {
		t.Errorf("start checkpoint: expected writes %+v, got %+v", expectedStartWrites, startMeta.Writes)
	}

	// Verify node1 metadata: source "loop", step 2, writes Count = 42, Message = "hello from node1"
	var node1Meta graph.CheckpointMetadata
	if err := json.Unmarshal(node1Cp.Metadata, &node1Meta); err != nil {
		t.Fatal(err)
	}
	if node1Meta.Source != "loop" {
		t.Errorf("node1 checkpoint: expected source 'loop', got %q", node1Meta.Source)
	}
	if node1Meta.Step != 2 {
		t.Errorf("node1 checkpoint: expected step 2, got %d", node1Meta.Step)
	}
	expectedNode1Writes := map[string]any{"count": float64(42), "message": "hello from node1"}
	if !reflect.DeepEqual(node1Meta.Writes, expectedNode1Writes) {
		t.Errorf("node1 checkpoint: expected writes %+v, got %+v", expectedNode1Writes, node1Meta.Writes)
	}

	// Verify node2 metadata: source "loop", step 3, writes Count = 100
	var node2Meta graph.CheckpointMetadata
	if err := json.Unmarshal(node2Cp.Metadata, &node2Meta); err != nil {
		t.Fatal(err)
	}
	if node2Meta.Source != "loop" {
		t.Errorf("node2 checkpoint: expected source 'loop', got %q", node2Meta.Source)
	}
	if node2Meta.Step != 3 {
		t.Errorf("node2 checkpoint: expected step 3, got %d", node2Meta.Step)
	}
	expectedNode2Writes := map[string]any{"count": float64(100)}
	if !reflect.DeepEqual(node2Meta.Writes, expectedNode2Writes) {
		t.Errorf("node2 checkpoint: expected writes %+v, got %+v", expectedNode2Writes, node2Meta.Writes)
	}
}
