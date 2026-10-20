package graph_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
	if loc.CheckpointID != "" {
		cp, ok := m.checkpoints[loc.CheckpointID]
		if !ok {
			return nil, nil // Contract: return nil, nil when not found
		}
		return &cp, nil
	}
	// When CheckpointID is empty, find the most recent checkpoint matching ThreadID and CheckpointNS
	var latest *graph.Checkpoint
	for _, cp := range m.checkpoints {
		if cp.Location.ThreadID == loc.ThreadID && cp.Location.CheckpointNS == loc.CheckpointNS {
			if latest == nil || cp.Timestamp.After(latest.Timestamp) {
				cpCopy := cp
				latest = &cpCopy
			}
		}
	}
	return latest, nil
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

func TestWithMaxSteps_InfiniteLoop(t *testing.T) {
	builder := graph.New[TestState]().WithName("loop_graph").WithMaxSteps(5)

	builder.AddNode("loop", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.Update[TestState](func(state TestState) TestState {
			state.Count++
			return state
		}), nil
	}))

	builder.AddEdge(graph.START, "loop")
	builder.AddEdge("loop", "loop")

	g, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	if g.MaxSteps() != 5 {
		t.Fatalf("expected MaxSteps 5, got %d", g.MaxSteps())
	}

	snap, err := g.Execute(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, graph.ErrMaxStepsExceeded) {
		t.Fatalf("expected ErrMaxStepsExceeded, got: %v", err)
	}

	// 5 steps should have executed
	if snap.State.Count != 5 {
		t.Errorf("expected count 5, got %d", snap.State.Count)
	}
}

func TestWithMaxSteps_WithinLimit(t *testing.T) {
	builder := graph.New[TestState]().WithName("linear_graph").WithMaxSteps(3)

	builder.AddNode("a", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.Update[TestState](func(state TestState) TestState {
			state.Count += 1
			return state
		}), nil
	}))
	builder.AddNode("b", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.Update[TestState](func(state TestState) TestState {
			state.Count += 10
			return state
		}), nil
	}))
	builder.AddNode("c", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.Update[TestState](func(state TestState) TestState {
			state.Count += 100
			return state
		}), nil
	}))

	builder.AddEdge(graph.START, "a")
	builder.AddEdge("a", "b")
	builder.AddEdge("b", "c")
	builder.AddEdge("c", graph.END)

	g, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	// 3 nodes execute, exactly allowed by WithMaxSteps(3)
	snap, err := g.Execute(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if snap.State.Count != 111 {
		t.Errorf("expected count 111, got %d", snap.State.Count)
	}

	// SetMaxSteps to 2 and run again on a fresh execution
	g.SetMaxSteps(2)
	if g.MaxSteps() != 2 {
		t.Fatalf("expected MaxSteps 2, got %d", g.MaxSteps())
	}
	snap2, err := g.Execute(context.Background(), nil, nil)
	if !errors.Is(err, graph.ErrMaxStepsExceeded) {
		t.Fatalf("expected ErrMaxStepsExceeded with limit 2, got: %v", err)
	}
	// After 2 steps ("a" and "b"), count should be 11
	if snap2.State.Count != 11 {
		t.Errorf("expected count 11, got %d", snap2.State.Count)
	}
}

func TestContextCancellation_BeforeExecute(t *testing.T) {
	builder := graph.New[TestState]().WithName("cancel_graph")
	builder.AddNode("node1", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return nil, nil
	}))
	builder.AddEdge(graph.START, "node1")
	builder.AddEdge("node1", graph.END)

	g, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel context before Execute

	_, err = g.Execute(ctx, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestContextCancellation_DuringExecute(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	builder := graph.New[TestState]().WithName("cancel_during")
	builder.AddNode("canceler", graph.NodeFunc(func(c context.Context, s TestState) (graph.Command[TestState], error) {
		cancel() // Cancel during execution of first node
		return graph.Update[TestState](func(st TestState) TestState {
			st.Count = 1
			return st
		}), nil
	}))
	builder.AddNode("unreachable", graph.NodeFunc(func(c context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.Update[TestState](func(st TestState) TestState {
			st.Count = 999
			return st
		}), nil
	}))

	builder.AddEdge(graph.START, "canceler")
	builder.AddEdge("canceler", "unreachable")
	builder.AddEdge("unreachable", graph.END)

	g, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	snap, err := g.Execute(ctx, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
	if snap.State.Count != 1 {
		t.Errorf("expected count 1, unreachable node should not run, got %d", snap.State.Count)
	}
}

func TestMiddlewareOrderAndStartNotWrapped(t *testing.T) {
	var events []string
	var visitedNodes []string
	var mu sync.Mutex

	recordEvent := func(e string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}

	recordNode := func(n string) {
		mu.Lock()
		defer mu.Unlock()
		visitedNodes = append(visitedNodes, n)
	}

	mw1 := func(next graph.Node[TestState]) graph.Node[TestState] {
		return graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
			if execCtx, ok := graph.ExecutionCtxFromContext(ctx); ok {
				recordNode(execCtx.NodeName)
			}
			recordEvent("mw1_enter")
			cmd, err := next.Execute(ctx, s)
			recordEvent("mw1_exit")
			return cmd, err
		})
	}

	mw2 := func(next graph.Node[TestState]) graph.Node[TestState] {
		return graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
			recordEvent("mw2_enter")
			cmd, err := next.Execute(ctx, s)
			recordEvent("mw2_exit")
			return cmd, err
		})
	}

	builder := graph.New[TestState]().
		WithName("middleware_test").
		WithMiddleware(mw1, mw2)

	builder.AddNode("node1", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		recordEvent("node1_exec")
		return nil, nil
	}))
	builder.AddNode("node2", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		recordEvent("node2_exec")
		return nil, nil
	}))

	builder.AddEdge(graph.START, "node1")
	builder.AddEdge("node1", "node2")
	builder.AddEdge("node2", graph.END)

	g, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.Execute(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify START was not wrapped by middleware
	for _, n := range visitedNodes {
		if n == graph.START || n == graph.END {
			t.Errorf("virtual node %q was wrapped by middleware", n)
		}
	}
	expectedNodes := []string{"node1", "node2"}
	if !reflect.DeepEqual(visitedNodes, expectedNodes) {
		t.Errorf("expected visited nodes %+v, got %+v", expectedNodes, visitedNodes)
	}

	// Verify middleware execution order: mw1 is outermost, mw2 is inner
	expectedEvents := []string{
		"mw1_enter", "mw2_enter", "node1_exec", "mw2_exit", "mw1_exit",
		"mw1_enter", "mw2_enter", "node2_exec", "mw2_exit", "mw1_exit",
	}
	if !reflect.DeepEqual(events, expectedEvents) {
		t.Errorf("expected event order:\n%+v\ngot:\n%+v", expectedEvents, events)
	}
}

func TestChainMiddleware(t *testing.T) {
	var order []string
	mw1 := func(next graph.Node[TestState]) graph.Node[TestState] {
		return graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
			order = append(order, "1")
			return next.Execute(ctx, s)
		})
	}
	mw2 := func(next graph.Node[TestState]) graph.Node[TestState] {
		return graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
			order = append(order, "2")
			return next.Execute(ctx, s)
		})
	}

	baseNode := graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		order = append(order, "base")
		return nil, nil
	})

	chained := graph.ChainMiddleware(baseNode, mw1, nil, mw2)
	_, err := chained.Execute(context.Background(), TestState{})
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"1", "2", "base"}
	if !reflect.DeepEqual(order, expected) {
		t.Errorf("expected order %v, got %v", expected, order)
	}
}

func TestRetryMiddleware_TransientFailure(t *testing.T) {
	attempts := 0
	transientErr := errors.New("temporary network error")

	retryMW := graph.RetryMiddleware[TestState](
		graph.WithMaxAttempts(3),
		graph.WithInitialWait(1*time.Millisecond),
		graph.WithMaxWait(10*time.Millisecond),
		graph.WithMultiplier(2.0),
	)

	builder := graph.New[TestState]().
		WithName("retry_success").
		WithMiddleware(retryMW)

	builder.AddNode("flaky", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		attempts++
		if attempts < 3 {
			return nil, transientErr
		}
		return graph.Update[TestState](func(st TestState) TestState {
			st.Count = 42
			return st
		}), nil
	}))

	builder.AddEdge(graph.START, "flaky")
	builder.AddEdge("flaky", graph.END)

	g, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	snap, err := g.Execute(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("expected success on 3rd attempt, got: %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
	if snap.State.Count != 42 {
		t.Errorf("expected count 42, got %d", snap.State.Count)
	}
}

func TestRetryMiddleware_RespectsShouldRetry(t *testing.T) {
	fatalErr := errors.New("unrecoverable validation error")
	retryableErr := errors.New("rate limited")

	attempts := 0
	retryMW := graph.RetryMiddleware[TestState](
		graph.WithMaxAttempts(3),
		graph.WithInitialWait(1*time.Millisecond),
		graph.WithShouldRetry(func(err error) bool {
			return errors.Is(err, retryableErr)
		}),
	)

	builder := graph.New[TestState]().
		WithName("should_retry_graph").
		WithMiddleware(retryMW)

	builder.AddNode("failing", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		attempts++
		return nil, fatalErr
	}))
	builder.AddEdge(graph.START, "failing")
	builder.AddEdge("failing", graph.END)

	g, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.Execute(context.Background(), nil, nil)
	if !errors.Is(err, fatalErr) {
		t.Fatalf("expected fatalErr, got: %v", err)
	}
	// Should stop immediately on attempt 1 because ShouldRetry returned false
	if attempts != 1 {
		t.Errorf("expected exactly 1 attempt for fatal error, got %d", attempts)
	}
}

func TestRetryMiddleware_ContextCanceledAbort(t *testing.T) {
	// 1. Context canceled inside node execution
	t.Run("NodeReturnsCanceled", func(t *testing.T) {
		attempts := 0
		retryMW := graph.RetryMiddleware[TestState](
			graph.WithMaxAttempts(5),
			graph.WithInitialWait(1*time.Millisecond),
		)

		builder := graph.New[TestState]().WithMiddleware(retryMW)
		builder.AddNode("cancel_node", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
			attempts++
			return nil, context.Canceled
		}))
		builder.AddEdge(graph.START, "cancel_node")
		builder.AddEdge("cancel_node", graph.END)

		g, err := builder.Build()
		if err != nil {
			t.Fatal(err)
		}

		_, err = g.Execute(context.Background(), nil, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
		if attempts != 1 {
			t.Errorf("expected 1 attempt, got %d", attempts)
		}
	})

	// 2. Context canceled while waiting for backoff
	t.Run("ContextCanceledDuringBackoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		attempts := 0

		retryMW := graph.RetryMiddleware[TestState](
			graph.WithMaxAttempts(5),
			graph.WithInitialWait(500*time.Millisecond),
		)

		builder := graph.New[TestState]().WithMiddleware(retryMW)
		builder.AddNode("flaky_node", graph.NodeFunc(func(c context.Context, s TestState) (graph.Command[TestState], error) {
			attempts++
			go func() {
				time.Sleep(20 * time.Millisecond)
				cancel()
			}()
			return nil, errors.New("try again")
		}))
		builder.AddEdge(graph.START, "flaky_node")
		builder.AddEdge("flaky_node", graph.END)

		g, err := builder.Build()
		if err != nil {
			t.Fatal(err)
		}

		_, err = g.Execute(ctx, nil, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
		if attempts != 1 {
			t.Errorf("expected 1 attempt before cancellation during backoff, got %d", attempts)
		}
	})
}

func TestRetryMiddleware_ExceedsMaxAttempts(t *testing.T) {
	attempts := 0
	persistentErr := errors.New("downstream service unavailable")

	retryMW := graph.RetryMiddleware[TestState](
		graph.WithMaxAttempts(3),
		graph.WithInitialWait(1*time.Millisecond),
	)

	builder := graph.New[TestState]().WithMiddleware(retryMW)
	builder.AddNode("failing_node", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		attempts++
		return nil, persistentErr
	}))
	builder.AddEdge(graph.START, "failing_node")
	builder.AddEdge("failing_node", graph.END)

	g, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.Execute(context.Background(), nil, nil)
	if !errors.Is(err, persistentErr) {
		t.Fatalf("expected persistentErr, got: %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestSubgraph_AsNode_SameState(t *testing.T) {
	childBuilder := graph.New[TestState]().WithName("child_graph")
	childBuilder.AddNode("c1", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.Update[TestState](func(state TestState) TestState {
			state.Count += 10
			state.Message += "c1"
			return state
		}), nil
	}))
	childBuilder.AddNode("c2", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.Update[TestState](func(state TestState) TestState {
			state.Count += 5
			state.Message += "->c2"
			return state
		}), nil
	}))
	childBuilder.AddEdge(graph.START, "c1")
	childBuilder.AddEdge("c1", "c2")
	childBuilder.AddEdge("c2", graph.END)

	childGraph, err := childBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	parentBuilder := graph.New[TestState]().WithName("parent_graph")
	parentBuilder.AddNode("pre", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.Update[TestState](func(state TestState) TestState {
			state.Count = 1
			state.Message = "start->"
			return state
		}), nil
	}))
	parentBuilder.AddNode("subgraph", childGraph.AsNode())
	parentBuilder.AddNode("post", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.Update[TestState](func(state TestState) TestState {
			state.Count *= 2
			state.Message += "->done"
			return state
		}), nil
	}))
	parentBuilder.AddEdge(graph.START, "pre")
	parentBuilder.AddEdge("pre", "subgraph")
	parentBuilder.AddEdge("subgraph", "post")
	parentBuilder.AddEdge("post", graph.END)

	parentGraph, err := parentBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	snap, err := parentGraph.Execute(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snap.IsDone() {
		t.Fatalf("expected graph to be done")
	}
	// pre: 1, child: +10 +5 = 16, post: 16*2 = 32
	if snap.State.Count != 32 {
		t.Errorf("expected count 32, got %d", snap.State.Count)
	}
	expectedMsg := "start->c1->c2->done"
	if snap.State.Message != expectedMsg {
		t.Errorf("expected message %q, got %q", expectedMsg, snap.State.Message)
	}
}

type ParentState struct {
	User       string `json:"user"`
	ChildTotal int    `json:"child_total"`
}

func (p ParentState) Copy() ParentState { return p }

type ChildState struct {
	Items []int `json:"items"`
	Sum   int   `json:"sum"`
}

func (c ChildState) Copy() ChildState {
	cp := c
	if len(c.Items) > 0 {
		cp.Items = slices.Clone(c.Items)
	}
	return cp
}

func TestSubgraph_AsNode_MapNode_DifferentState(t *testing.T) {
	childBuilder := graph.New[ChildState]().WithName("child_calc")
	childBuilder.AddNode("calc", graph.NodeFunc(func(ctx context.Context, s ChildState) (graph.Command[ChildState], error) {
		return graph.Update[ChildState](func(state ChildState) ChildState {
			sum := 0
			for _, item := range state.Items {
				sum += item
			}
			state.Sum = sum
			return state
		}), nil
	}))
	childBuilder.AddEdge(graph.START, "calc")
	childBuilder.AddEdge("calc", graph.END)

	childGraph, err := childBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	parentBuilder := graph.New[ParentState]().WithName("parent_graph")
	parentBuilder.AddNode("setup", graph.NodeFunc(func(ctx context.Context, s ParentState) (graph.Command[ParentState], error) {
		return graph.Update[ParentState](func(p ParentState) ParentState {
			p.User = "Alice"
			return p
		}), nil
	}))

	subNode := graph.MapNode(
		childGraph.AsNode(),
		func(p ParentState) ChildState {
			return ChildState{Items: []int{10, 20, 30}}
		},
		func(p ParentState, c ChildState) ParentState {
			p.ChildTotal = c.Sum
			return p
		},
	)
	parentBuilder.AddNode("subgraph", subNode)
	parentBuilder.AddNode("finalize", graph.NodeFunc(func(ctx context.Context, s ParentState) (graph.Command[ParentState], error) {
		return graph.Update[ParentState](func(p ParentState) ParentState {
			p.ChildTotal += 5
			return p
		}), nil
	}))

	parentBuilder.AddEdge(graph.START, "setup")
	parentBuilder.AddEdge("setup", "subgraph")
	parentBuilder.AddEdge("subgraph", "finalize")
	parentBuilder.AddEdge("finalize", graph.END)

	parentGraph, err := parentBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	snap, err := parentGraph.Execute(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snap.IsDone() {
		t.Fatalf("expected graph to be done")
	}
	if snap.State.User != "Alice" {
		t.Errorf("expected user Alice, got %q", snap.State.User)
	}
	if snap.State.ChildTotal != 65 {
		t.Errorf("expected total 65 (60+5), got %d", snap.State.ChildTotal)
	}
}

func TestSubgraph_InterruptPropagation(t *testing.T) {
	childBuilder := graph.New[TestState]().WithName("child_interrupt")
	childBuilder.AddNode("step1", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.Update[TestState](func(state TestState) TestState {
			state.Count = 100
			state.Message = "before_interrupt"
			return state
		}), nil
	}))
	childBuilder.AddNode("interrupter", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.InterruptUpdate[TestState](func(state TestState) TestState {
			state.Count = 150
			state.Message += "->interrupted"
			return state
		}), nil
	}))
	childBuilder.AddNode("step3", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.Update[TestState](func(state TestState) TestState {
			state.Count += 1000
			return state
		}), nil
	}))
	childBuilder.AddEdge(graph.START, "step1")
	childBuilder.AddEdge("step1", "interrupter")
	childBuilder.AddEdge("interrupter", "step3")
	childBuilder.AddEdge("step3", graph.END)

	childGraph, err := childBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	parentBuilder := graph.New[TestState]().WithName("parent_interrupt")
	parentBuilder.AddNode("sub", childGraph.AsNode())
	parentBuilder.AddNode("after", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.Update[TestState](func(state TestState) TestState {
			state.Count += 5000
			return state
		}), nil
	}))
	parentBuilder.AddEdge(graph.START, "sub")
	parentBuilder.AddEdge("sub", "after")
	parentBuilder.AddEdge("after", graph.END)

	parentGraph, err := parentBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	snap, err := parentGraph.Execute(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.IsDone() {
		t.Fatalf("expected graph to pause on interrupt, but IsDone() was true")
	}
	if snap.State.Count != 150 {
		t.Errorf("expected count 150 from InterruptUpdate, got %d", snap.State.Count)
	}
	if snap.State.Message != "before_interrupt->interrupted" {
		t.Errorf("expected message 'before_interrupt->interrupted', got %q", snap.State.Message)
	}
	if !slices.Equal(snap.Next, []string{"sub"}) {
		t.Errorf("expected next node to be [sub], got %v", snap.Next)
	}
}

type ParallelState struct {
	A         string `json:"a"`
	B         string `json:"b"`
	C         string `json:"c"`
	Joined    string `json:"joined"`
	JoinCount int    `json:"join_count"`
}

func (p ParallelState) Copy() ParallelState { return p }

func TestParallelExecution_FanOutFanIn(t *testing.T) {
	builder := graph.New[ParallelState]().WithName("fan_out_fan_in")

	start := time.Now()
	builder.AddNode("branchA", graph.NodeFunc(func(ctx context.Context, s ParallelState) (graph.Command[ParallelState], error) {
		time.Sleep(20 * time.Millisecond)
		return graph.Update[ParallelState](func(state ParallelState) ParallelState {
			state.A = "apple"
			return state
		}), nil
	}))
	builder.AddNode("branchB", graph.NodeFunc(func(ctx context.Context, s ParallelState) (graph.Command[ParallelState], error) {
		time.Sleep(20 * time.Millisecond)
		return graph.Update[ParallelState](func(state ParallelState) ParallelState {
			state.B = "banana"
			return state
		}), nil
	}))
	builder.AddNode("branchC", graph.NodeFunc(func(ctx context.Context, s ParallelState) (graph.Command[ParallelState], error) {
		time.Sleep(20 * time.Millisecond)
		return graph.Update[ParallelState](func(state ParallelState) ParallelState {
			state.C = "cherry"
			return state
		}), nil
	}))
	builder.AddNode("join", graph.NodeFunc(func(ctx context.Context, s ParallelState) (graph.Command[ParallelState], error) {
		return graph.Update[ParallelState](func(state ParallelState) ParallelState {
			state.Joined = state.A + "," + state.B + "," + state.C
			state.JoinCount++
			return state
		}), nil
	}))

	builder.AddEdge(graph.START, "branchA")
	builder.AddEdge(graph.START, "branchB")
	builder.AddEdge(graph.START, "branchC")
	builder.AddEdge("branchA", "join")
	builder.AddEdge("branchB", "join")
	builder.AddEdge("branchC", "join")
	builder.AddEdge("join", graph.END)

	g, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	snap, err := g.Execute(context.Background(), nil, nil)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snap.IsDone() {
		t.Fatalf("expected graph to be done")
	}
	if snap.State.A != "apple" || snap.State.B != "banana" || snap.State.C != "cherry" {
		t.Errorf("unexpected branch states: %+v", snap.State)
	}
	if snap.State.Joined != "apple,banana,cherry" {
		t.Errorf("expected joined 'apple,banana,cherry', got %q", snap.State.Joined)
	}
	if snap.State.JoinCount != 1 {
		t.Errorf("expected join node to execute exactly 1 time, got %d", snap.State.JoinCount)
	}
	if duration >= 150*time.Millisecond {
		t.Logf("warning: execution took %v", duration)
	}
}

func TestParallelExecution_ErrorPropagation(t *testing.T) {
	builder := graph.New[TestState]().WithName("parallel_error")
	expectedErr := errors.New("boom in branch")

	builder.AddNode("branchOk", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		time.Sleep(10 * time.Millisecond)
		return graph.Update[TestState](func(state TestState) TestState {
			state.Count = 1
			return state
		}), nil
	}))
	builder.AddNode("branchFail", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		time.Sleep(10 * time.Millisecond)
		return nil, expectedErr
	}))

	builder.AddEdge(graph.START, "branchOk")
	builder.AddEdge(graph.START, "branchFail")
	builder.AddEdge("branchOk", graph.END)
	builder.AddEdge("branchFail", graph.END)

	g, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.Execute(context.Background(), nil, nil)
	if err == nil {
		t.Fatalf("expected error from branchFail, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error to wrap expectedErr, got: %v", err)
	}
}

func TestParallelExecution_MaxSteps(t *testing.T) {
	builder := graph.New[ParallelState]().WithName("parallel_max_steps").WithMaxSteps(2)

	builder.AddNode("b1", graph.NodeFunc(func(ctx context.Context, s ParallelState) (graph.Command[ParallelState], error) {
		return nil, nil
	}))
	builder.AddNode("b2", graph.NodeFunc(func(ctx context.Context, s ParallelState) (graph.Command[ParallelState], error) {
		return nil, nil
	}))
	builder.AddNode("b3", graph.NodeFunc(func(ctx context.Context, s ParallelState) (graph.Command[ParallelState], error) {
		return nil, nil
	}))

	builder.AddEdge(graph.START, "b1")
	builder.AddEdge(graph.START, "b2")
	builder.AddEdge(graph.START, "b3")
	builder.AddEdge("b1", graph.END)
	builder.AddEdge("b2", graph.END)
	builder.AddEdge("b3", graph.END)

	g, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.Execute(context.Background(), nil, nil)
	if err == nil {
		t.Fatalf("expected ErrMaxStepsExceeded, got nil")
	}
	if !errors.Is(err, graph.ErrMaxStepsExceeded) {
		t.Fatalf("expected ErrMaxStepsExceeded, got: %v", err)
	}
}

func TestParallelExecution_Interrupt(t *testing.T) {
	builder := graph.New[ParallelState]().WithName("parallel_interrupt")

	builder.AddNode("b1", graph.NodeFunc(func(ctx context.Context, s ParallelState) (graph.Command[ParallelState], error) {
		return graph.Update[ParallelState](func(state ParallelState) ParallelState {
			state.A = "from_b1"
			return state
		}), nil
	}))
	builder.AddNode("b2_interrupt", graph.NodeFunc(func(ctx context.Context, s ParallelState) (graph.Command[ParallelState], error) {
		return graph.InterruptUpdate[ParallelState](func(state ParallelState) ParallelState {
			state.B = "from_b2_interrupted"
			return state
		}), nil
	}))

	builder.AddEdge(graph.START, "b1")
	builder.AddEdge(graph.START, "b2_interrupt")
	builder.AddEdge("b1", graph.END)
	builder.AddEdge("b2_interrupt", graph.END)

	g, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	snap, err := g.Execute(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.IsDone() {
		t.Fatalf("expected graph to pause on interrupt")
	}
	if snap.State.A != "from_b1" {
		t.Errorf("expected A to be 'from_b1', got %q", snap.State.A)
	}
	if snap.State.B != "from_b2_interrupted" {
		t.Errorf("expected B to be 'from_b2_interrupted', got %q", snap.State.B)
	}
}

func TestParallelExecution_Stream(t *testing.T) {
	builder := graph.New[ParallelState]().WithName("parallel_stream")

	builder.AddNode("nodeA", graph.NodeFunc(func(ctx context.Context, s ParallelState) (graph.Command[ParallelState], error) {
		if rt, ok := graph.RuntimeFromContext(ctx); ok && rt.Stream != nil {
			_ = rt.Stream.Write(ctx, "event_from_A")
		}
		return graph.Update[ParallelState](func(state ParallelState) ParallelState {
			state.A = "doneA"
			return state
		}), nil
	}))

	builder.AddNode("nodeB", graph.NodeFunc(func(ctx context.Context, s ParallelState) (graph.Command[ParallelState], error) {
		if rt, ok := graph.RuntimeFromContext(ctx); ok && rt.Stream != nil {
			_ = rt.Stream.Write(ctx, "event_from_B")
		}
		return graph.Update[ParallelState](func(state ParallelState) ParallelState {
			state.B = "doneB"
			return state
		}), nil
	}))

	builder.AddEdge(graph.START, "nodeA")
	builder.AddEdge(graph.START, "nodeB")
	builder.AddEdge("nodeA", graph.END)
	builder.AddEdge("nodeB", graph.END)

	g, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	seq, err := g.Stream(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	var eventCount int
	for _, err := range seq {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		eventCount++
	}
	if eventCount == 0 {
		t.Errorf("expected stream events, got 0")
	}
}

func TestSubgraph_LocationScoping(t *testing.T) {
	var observedLoc graph.Location

	childBuilder := graph.New[TestState]().WithName("child")
	childBuilder.AddNode("child_node", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		if rt, ok := graph.RuntimeFromContext(ctx); ok {
			observedLoc = rt.Location
		}
		return nil, nil
	}))
	childBuilder.AddEdge(graph.START, "child_node")
	childBuilder.AddEdge("child_node", graph.END)

	childGraph, err := childBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	parentBuilder := graph.New[TestState]().WithName("parent")
	parentBuilder.AddNode("nested_subgraph", childGraph.AsNode())
	parentBuilder.AddEdge(graph.START, "nested_subgraph")
	parentBuilder.AddEdge("nested_subgraph", graph.END)

	parentGraph, err := parentBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	initialLoc := &graph.Location{
		ThreadID:     "thread-abc",
		CheckpointNS: "parent_ns",
	}

	_, err = parentGraph.Execute(context.Background(), nil, initialLoc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if observedLoc.ThreadID != "thread-abc" {
		t.Errorf("expected ThreadID thread-abc, got %q", observedLoc.ThreadID)
	}

	// Format: "parent_ns|nested_subgraph:<task_id>"
	expectedPrefix := "parent_ns" + graph.NSSep + "nested_subgraph" + graph.NSEnd
	if !strings.HasPrefix(observedLoc.CheckpointNS, expectedPrefix) {
		t.Fatalf("expected CheckpointNS prefix %q, got %q", expectedPrefix, observedLoc.CheckpointNS)
	}
	taskID := strings.TrimPrefix(observedLoc.CheckpointNS, expectedPrefix)
	if _, err := uuid.Parse(taskID); err != nil {
		t.Errorf("expected valid uuid for taskID, got %q (err: %v)", taskID, err)
	}

	// 2-level nested subgraphs: should produce "sub1:<task_id_1>|sub2:<task_id_2>"
	var leafLoc graph.Location
	leafBuilder := graph.New[TestState]().WithName("leaf_graph")
	leafBuilder.AddNode("leaf", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		if rt, ok := graph.RuntimeFromContext(ctx); ok {
			leafLoc = rt.Location
		}
		return nil, nil
	}))
	leafBuilder.AddEdge(graph.START, "leaf")
	leafBuilder.AddEdge("leaf", graph.END)
	leafGraph, err := leafBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	midBuilder := graph.New[TestState]().WithName("mid_graph")
	midBuilder.AddNode("sub2", leafGraph.AsNode())
	midBuilder.AddEdge(graph.START, "sub2")
	midBuilder.AddEdge("sub2", graph.END)
	midGraph, err := midBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	topBuilder := graph.New[TestState]().WithName("top_graph")
	topBuilder.AddNode("sub1", midGraph.AsNode())
	topBuilder.AddEdge(graph.START, "sub1")
	topBuilder.AddEdge("sub1", graph.END)
	topGraph, err := topBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	_, err = topGraph.Execute(context.Background(), nil, &graph.Location{ThreadID: "t-nested", CheckpointNS: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parts := strings.Split(leafLoc.CheckpointNS, graph.NSSep)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts separated by %q, got %d in %q", graph.NSSep, len(parts), leafLoc.CheckpointNS)
	}
	if !strings.HasPrefix(parts[0], "sub1"+graph.NSEnd) {
		t.Errorf("expected parts[0] to start with 'sub1:', got %q", parts[0])
	}
	if _, err := uuid.Parse(strings.TrimPrefix(parts[0], "sub1"+graph.NSEnd)); err != nil {
		t.Errorf("expected valid uuid in parts[0], got %q", parts[0])
	}
	if !strings.HasPrefix(parts[1], "sub2"+graph.NSEnd) {
		t.Errorf("expected parts[1] to start with 'sub2:', got %q", parts[1])
	}
	if _, err := uuid.Parse(strings.TrimPrefix(parts[1], "sub2"+graph.NSEnd)); err != nil {
		t.Errorf("expected valid uuid in parts[1], got %q", parts[1])
	}
}

type LoopState struct {
	Iteration int      `json:"iteration"`
	Locations []string `json:"locations"`
}

func (s LoopState) Copy() LoopState {
	cp := s
	if len(s.Locations) > 0 {
		cp.Locations = slices.Clone(s.Locations)
	}
	return cp
}

func TestSubgraph_InterruptResumeSameTaskID(t *testing.T) {
	cp := &mockCheckpointer{
		checkpoints: make(map[string]graph.Checkpoint),
	}

	var childRuns []string

	childBuilder := graph.New[TestState]().WithName("child").WithCheckpointer(cp)
	childBuilder.AddNode("step1", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		if rt, ok := graph.RuntimeFromContext(ctx); ok {
			childRuns = append(childRuns, rt.Location.CheckpointNS)
		}
		return graph.Update[TestState](func(st TestState) TestState {
			st.Count = 10
			return st
		}), nil
	}))
	childBuilder.AddNode("pause", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		if s.Count < 20 {
			return graph.InterruptUpdate[TestState](func(st TestState) TestState {
				st.Count = 20
				return st
			}), nil
		}
		return nil, nil
	}))
	childBuilder.AddNode("step2", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		if rt, ok := graph.RuntimeFromContext(ctx); ok {
			childRuns = append(childRuns, rt.Location.CheckpointNS)
		}
		return graph.Update[TestState](func(st TestState) TestState {
			st.Count = 30
			return st
		}), nil
	}))
	childBuilder.AddEdge(graph.START, "step1")
	childBuilder.AddEdge("step1", "pause")
	childBuilder.AddEdge("pause", "step2")
	childBuilder.AddEdge("step2", graph.END)

	childGraph, err := childBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	parentBuilder := graph.New[TestState]().WithName("parent").WithCheckpointer(cp)
	parentBuilder.AddNode("sub", childGraph.AsNode())
	parentBuilder.AddNode("done", graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return graph.Update[TestState](func(st TestState) TestState {
			st.Count += 100
			return st
		}), nil
	}))
	parentBuilder.AddEdge(graph.START, "sub")
	parentBuilder.AddEdge("sub", "done")
	parentBuilder.AddEdge("done", graph.END)

	parentGraph, err := parentBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	initLoc := &graph.Location{ThreadID: "t-resume", CheckpointNS: ""}
	snap1, err := parentGraph.Execute(context.Background(), nil, initLoc)
	if err != nil {
		t.Fatalf("first run error: %v", err)
	}
	if snap1.IsDone() {
		t.Fatalf("expected first run to be paused on interrupt")
	}
	if len(childRuns) != 1 {
		t.Fatalf("expected 1 child run, got %d", len(childRuns))
	}
	firstNS := childRuns[0]

	// Resume parent graph from snap1.Location
	snap2, err := parentGraph.Execute(context.Background(), nil, &snap1.Location)
	if err != nil {
		t.Fatalf("resume run error: %v", err)
	}
	if !snap2.IsDone() {
		t.Fatalf("expected resumed run to finish, got Next: %v", snap2.Next)
	}
	if snap2.State.Count != 130 {
		t.Errorf("expected final count 130, got %d", snap2.State.Count)
	}

	if len(childRuns) != 2 {
		t.Fatalf("expected 2 child runs after resume, got %d", len(childRuns))
	}
	secondNS := childRuns[1]

	if !strings.HasPrefix(firstNS, "sub"+graph.NSEnd) {
		t.Errorf("expected firstNS to start with 'sub:', got %q", firstNS)
	}
	if firstNS != secondNS {
		t.Errorf("expected exact same CheckpointNS on resume, got initial %q != resume %q", firstNS, secondNS)
	}
}

func TestSubgraph_LoopDifferentTaskID(t *testing.T) {
	childBuilder := graph.New[LoopState]().WithName("child")
	childBuilder.AddNode("record", graph.NodeFunc(func(ctx context.Context, s LoopState) (graph.Command[LoopState], error) {
		if rt, ok := graph.RuntimeFromContext(ctx); ok {
			loc := rt.Location.CheckpointNS
			return graph.Update[LoopState](func(st LoopState) LoopState {
				st.Locations = append(st.Locations, loc)
				return st
			}), nil
		}
		return nil, nil
	}))
	childBuilder.AddEdge(graph.START, "record")
	childBuilder.AddEdge("record", graph.END)

	childGraph, err := childBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	parentBuilder := graph.New[LoopState]().WithName("parent")
	parentBuilder.AddNode("sub", childGraph.AsNode())
	parentBuilder.AddNode("increment", graph.NodeFunc(func(ctx context.Context, s LoopState) (graph.Command[LoopState], error) {
		return graph.Update[LoopState](func(st LoopState) LoopState {
			st.Iteration++
			return st
		}), nil
	}))

	parentBuilder.AddEdge(graph.START, "sub")
	parentBuilder.AddEdge("sub", "increment")
	parentBuilder.AddConditionalEdge("increment", "sub", func(s LoopState) bool {
		return s.Iteration < 2
	})
	parentBuilder.AddConditionalEdge("increment", graph.END, func(s LoopState) bool {
		return s.Iteration >= 2
	})

	parentGraph, err := parentBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	snap, err := parentGraph.Execute(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snap.IsDone() {
		t.Fatalf("expected graph to complete")
	}
	if len(snap.State.Locations) != 2 {
		t.Fatalf("expected 2 recorded locations across loop, got %d", len(snap.State.Locations))
	}
	loc1 := snap.State.Locations[0]
	loc2 := snap.State.Locations[1]

	if !strings.HasPrefix(loc1, "sub"+graph.NSEnd) {
		t.Errorf("expected loc1 to start with 'sub:', got %q", loc1)
	}
	if !strings.HasPrefix(loc2, "sub"+graph.NSEnd) {
		t.Errorf("expected loc2 to start with 'sub:', got %q", loc2)
	}
	if loc1 == loc2 {
		t.Errorf("expected distinct CheckpointNS across loop iterations, got %q == %q", loc1, loc2)
	}
}

func TestBuilder_InvalidNodeName(t *testing.T) {
	dummyNode := graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return nil, nil
	})

	// Pipe '|' character should fail
	b1 := graph.New[TestState]().AddNode("invalid|node", dummyNode)
	if _, err := b1.Build(); err == nil {
		t.Errorf("expected error for node name containing '|', got nil")
	} else if !strings.Contains(err.Error(), "cannot contain '|' or ':'") {
		t.Errorf("expected error message to mention '|' or ':', got %v", err)
	}

	// Colon ':' character should fail
	b2 := graph.New[TestState]().AddNode("invalid:node", dummyNode)
	if _, err := b2.Build(); err == nil {
		t.Errorf("expected error for node name containing ':', got nil")
	} else if !strings.Contains(err.Error(), "cannot contain '|' or ':'") {
		t.Errorf("expected error message to mention '|' or ':', got %v", err)
	}

	// Slash '/' character should be allowed (e.g. tools/search)
	b3 := graph.New[TestState]().
		AddNode("tools/search", dummyNode).
		AddEdge(graph.START, "tools/search").
		AddEdge("tools/search", graph.END)
	if _, err := b3.Build(); err != nil {
		t.Errorf("expected slash '/' to be allowed in node name, got error: %v", err)
	}
}

func TestBuilder_CompileTimeValidation(t *testing.T) {
	dummyNode := graph.NodeFunc(func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return nil, nil
	})

	t.Run("empty node name", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("", dummyNode).
			AddEdge(graph.START, "").
			AddEdge("", graph.END)
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "node name cannot be empty") {
			t.Fatalf("expected 'node name cannot be empty', got: %v", err)
		}
	})

	t.Run("reserved START node name", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode(graph.START, dummyNode).
			AddEdge(graph.START, graph.END)
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "is reserved") {
			t.Fatalf("expected 'is reserved', got: %v", err)
		}
	})

	t.Run("reserved END node name", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode(graph.END, dummyNode).
			AddEdge(graph.START, graph.END)
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "is reserved") {
			t.Fatalf("expected 'is reserved', got: %v", err)
		}
	})

	t.Run("nil node", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("nil_node", nil).
			AddEdge(graph.START, "nil_node").
			AddEdge("nil_node", graph.END)
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "cannot be nil") {
			t.Fatalf("expected 'cannot be nil', got: %v", err)
		}
	})

	t.Run("missing outgoing edge from START", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("n1", dummyNode).
			AddEdge("n1", graph.END)
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "graph must have at least one outgoing edge from START") {
			t.Fatalf("expected 'graph must have at least one outgoing edge from START', got: %v", err)
		}
	})

	t.Run("outgoing edge from END", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("n1", dummyNode).
			AddEdge(graph.START, "n1").
			AddEdge("n1", graph.END).
			AddEdge(graph.END, "n1")
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "END node cannot have outgoing edges") {
			t.Fatalf("expected 'END node cannot have outgoing edges', got: %v", err)
		}
	})

	t.Run("user node with no outgoing edges", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("n1", dummyNode).
			AddNode("dead_end", dummyNode).
			AddEdge(graph.START, "n1").
			AddEdge("n1", graph.END)
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "node \"dead_end\" has no outgoing edges") {
			t.Fatalf("expected 'node \"dead_end\" has no outgoing edges', got: %v", err)
		}
	})

	t.Run("edge originates from unknown node", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("n1", dummyNode).
			AddEdge(graph.START, "n1").
			AddEdge("n1", graph.END).
			AddEdge("ghost", "n1")
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "edge originates from unknown node \"ghost\"") {
			t.Fatalf("expected 'edge originates from unknown node \"ghost\"', got: %v", err)
		}
	})

	t.Run("SimpleEdge targeting unknown node", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("n1", dummyNode).
			AddEdge(graph.START, "n1").
			AddEdge("n1", "unknown_node")
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "targets unknown node \"unknown_node\"") {
			t.Fatalf("expected 'targets unknown node \"unknown_node\"', got: %v", err)
		}
	})

	t.Run("SimpleEdge targeting START", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("n1", dummyNode).
			AddEdge(graph.START, "n1").
			AddEdge("n1", graph.START)
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "cannot target START") {
			t.Fatalf("expected 'cannot target START', got: %v", err)
		}
	})

	t.Run("ConditionalEdge targeting unknown node", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("n1", dummyNode).
			AddEdge(graph.START, "n1").
			AddConditionalEdge("n1", "unknown_node", func(s TestState) bool { return true })
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "targets unknown node \"unknown_node\"") {
			t.Fatalf("expected 'targets unknown node \"unknown_node\"', got: %v", err)
		}
	})

	t.Run("ConditionalEdge targeting START", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("n1", dummyNode).
			AddEdge(graph.START, "n1").
			AddConditionalEdge("n1", graph.START, func(s TestState) bool { return true })
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "cannot target START") {
			t.Fatalf("expected 'cannot target START', got: %v", err)
		}
	})

	t.Run("ConditionalEdge with nil condition", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("n1", dummyNode).
			AddEdge(graph.START, "n1").
			AddConditionalEdge("n1", graph.END, nil)
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "conditional edge condition cannot be nil") {
			t.Fatalf("expected 'conditional edge condition cannot be nil', got: %v", err)
		}
	})

	t.Run("RouteEdge targeting unknown node", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("n1", dummyNode).
			AddEdge(graph.START, "n1").
			AddRouteEdge("n1", func(s TestState) (string, error) { return "a", nil }, map[string]string{
				"a": "unknown_node",
			})
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "targets unknown node \"unknown_node\"") {
			t.Fatalf("expected 'targets unknown node \"unknown_node\"', got: %v", err)
		}
	})

	t.Run("RouteEdge targeting START", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("n1", dummyNode).
			AddEdge(graph.START, "n1").
			AddRouteEdge("n1", func(s TestState) (string, error) { return "a", nil }, map[string]string{
				"a": graph.START,
			})
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "cannot target START") {
			t.Fatalf("expected 'cannot target START', got: %v", err)
		}
	})

	t.Run("RouteEdge with nil router function", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("n1", dummyNode).
			AddEdge(graph.START, "n1").
			AddRouteEdge("n1", nil, map[string]string{
				"a": graph.END,
			})
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "route edge router function cannot be nil") {
			t.Fatalf("expected 'route edge router function cannot be nil', got: %v", err)
		}
	})

	t.Run("RouteEdge with empty routes map", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("n1", dummyNode).
			AddEdge(graph.START, "n1").
			AddRouteEdge("n1", func(s TestState) (string, error) { return "a", nil }, map[string]string{})
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "route edge routes map cannot be empty") {
			t.Fatalf("expected 'route edge routes map cannot be empty', got: %v", err)
		}
	})

	t.Run("RouteEdge with empty target", func(t *testing.T) {
		b := graph.New[TestState]().
			AddNode("n1", dummyNode).
			AddEdge(graph.START, "n1").
			AddRouteEdge("n1", func(s TestState) (string, error) { return "a", nil }, map[string]string{
				"a": "",
			})
		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "cannot be empty") {
			t.Fatalf("expected target cannot be empty, got: %v", err)
		}
	})
}

func TestSubgraph_MapNode_InterruptPropagation(t *testing.T) {
	childBuilder := graph.New[ChildState]().WithName("child_map_interrupt")
	childBuilder.AddNode("interrupter", graph.NodeFunc(func(ctx context.Context, s ChildState) (graph.Command[ChildState], error) {
		return graph.InterruptUpdate[ChildState](func(c ChildState) ChildState {
			c.Sum = 777
			return c
		}), nil
	}))
	childBuilder.AddEdge(graph.START, "interrupter")
	childBuilder.AddEdge("interrupter", graph.END)

	childGraph, err := childBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	subNode := graph.MapNode(
		childGraph.AsNode(),
		func(p ParentState) ChildState {
			return ChildState{Items: []int{1, 2, 3}}
		},
		func(p ParentState, c ChildState) ParentState {
			p.ChildTotal = c.Sum
			return p
		},
	)

	parentBuilder := graph.New[ParentState]().WithName("parent_map_interrupt")
	parentBuilder.AddNode("mapped", subNode)
	parentBuilder.AddEdge(graph.START, "mapped")
	parentBuilder.AddEdge("mapped", graph.END)

	parentGraph, err := parentBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}

	snap, err := parentGraph.Execute(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.IsDone() {
		t.Fatalf("expected graph to pause on interrupt from child MapNode")
	}
	if snap.State.ChildTotal != 777 {
		t.Errorf("expected ChildTotal 777, got %d", snap.State.ChildTotal)
	}
}
