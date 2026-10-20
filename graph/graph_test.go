package graph_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
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
