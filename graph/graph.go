package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/google/uuid"
	internalctx "github.com/masterkeysrd/loom/internal/context"
	"github.com/masterkeysrd/loom/store"
	"github.com/masterkeysrd/loom/stream"
	"github.com/masterkeysrd/loom/telemetry"
)

// START is the reserved name for the virtual entry node of every graph.
// Execution always begins here, regardless of which real node is first.
const START = "__START__"

// END is the reserved name for the virtual terminal node of every graph.
// When the next-node list resolves to END, the graph is considered done.
const END = "__END__"

// Graph is the runtime engine for a compiled workflow.
// It owns the node registry, the edge table, and an optional checkpointer,
// and it drives execution step-by-step through those structures.
//
// Create a Graph via [Builder.Build]; never instantiate it directly.
type Graph[S State[S]] struct {
	name         string
	nodes        map[string]Node[S]
	edges        map[string][]Edge[S]
	checkpointer Checkpointer
	store        store.Store
}

// Name returns the name of the graph.
func (g *Graph[S]) Name() string {
	return g.name
}

// Checkpointer returns the checkpointer of the graph.
func (g *Graph[S]) Checkpointer() Checkpointer {
	return g.checkpointer
}

// SetCheckpointer sets the checkpointer of the graph.
func (g *Graph[S]) SetCheckpointer(cp Checkpointer) {
	g.checkpointer = cp
}

// Store returns the store of the graph.
func (g *Graph[S]) Store() store.Store {
	return g.store
}

// SetStore sets the store of the graph.
func (g *Graph[S]) SetStore(s store.Store) {
	g.store = s
}

// Execute runs the graph to completion (or until an interrupt) and returns
// the resulting [Snapshot].
//
// If loc is non-nil, the graph resumes from the checkpoint identified by that
// [Location]; otherwise execution starts from [START]. The optional input
// [Command] is applied on top of the loaded (or zero-value) state before the
// first node runs.
//
// Execute is synchronous. For streaming token-by-token output, use [Graph.Stream].
func (g *Graph[State]) Execute(ctx context.Context, input Command[State], loc *Location) (Snapshot[State], error) {
	snapshot := Snapshot[State]{}

	if loc != nil {
		snapshot.Location = *loc

		snap, err := g.Load(ctx, *loc)
		if err != nil && err != ErrCheckpointNotFound {
			return Snapshot[State]{}, fmt.Errorf("failed to load checkpoint: %w", err)
		}

		if snap != nil {
			snapshot = snap.Copy()
		}
	}

	// When the snapshot is loaded and is done we reset it from the start keeping the
	// state, this allows to re-run the graph from the start with the same state.
	if snapshot.IsDone() {
		snapshot.Next = []string{START}
	}

	currentStep := 0
	if len(snapshot.Metadata) > 0 {
		var meta CheckpointMetadata
		if err := json.Unmarshal(snapshot.Metadata, &meta); err == nil {
			currentStep = meta.Step
		}
	}

	var initialBeforeState State
	if any(snapshot.State) != nil {
		initialBeforeState = snapshot.State.Copy()
	}

	if input != nil {
		// Make a copy of the state before applying the input command, to prevent
		snapshot.State = input.Apply(snapshot.State.Copy())
	}

	rec := newRecorder(g.name)
	ctx, rec = rec.startGraph(ctx, snapshot.Location)
	defer func() { rec.endGraph(ctx, nil) }() // Normal end if no error returned early

	isFirstLoop := true

	for !snapshot.IsDone() {
		nodes, err := g.getNodes(snapshot.Next)
		if err != nil {
			return snapshot, err
		}

		// For the moment only support one node.
		if len(nodes) > 1 {
			return snapshot, fmt.Errorf("multiple nodes to execute at once is not supported yet")
		}

		nodeName := snapshot.Next[0]
		node := nodes[0]

		// Start node execution span using the pre-transition location
		nodeCtx, span := rec.startNode(ctx, nodeName, snapshot.Location)
		startTime := time.Now()

		execCtx := WithExecutionCtx(nodeCtx, ExecutionCtx{
			GraphName: g.name,
			NodeName:  nodeName,
			Location:  snapshot.Location,
		})

		execCtx = internalctx.WithState(execCtx, snapshot.State)
		if g.store != nil {
			execCtx = store.WithStore(execCtx, g.store)
		}
		var sw stream.Writer
		if w, ok := stream.WriterFromContext(execCtx); ok {
			sw = w
		}

		rt := &Runtime{
			RunID:    snapshot.Location.CheckpointID,
			NodeName: nodeName,
			Step:     currentStep,
			Location: snapshot.Location,
			State:    snapshot.State,
			Store:    g.store,
			Stream:   sw,
		}
		execCtx = WithRuntime(execCtx, rt)

		cmd, err := node.Execute(execCtx, snapshot.State.Copy())
		if err != nil {
			rec.endNode(ctx, nodeName, span, startTime, err)
			return snapshot, fmt.Errorf("failed to execute node: %w", err)
		}

		var interrupt bool
		var beforeState State
		if isFirstLoop && currentStep == 0 {
			beforeState = initialBeforeState
		} else {
			beforeState = snapshot.State.Copy()
		}
		isFirstLoop = false

		snapshot, interrupt, err = g.transition(snapshot, cmd)
		if err != nil {
			rec.endNode(ctx, nodeName, span, startTime, err)
			return snapshot, fmt.Errorf("failed to transition to next nodes: %w", err)
		}

		// Update the span's checkpoint attribute to the post-transition checkpoint ID
		span.SetAttributes(telemetry.WithLoomCheckpoint(snapshot.Location.CheckpointID))

		// End the node execution span normally
		rec.endNode(ctx, nodeName, span, startTime, nil)

		currentStep++
		source := "loop"
		if nodeName == START {
			source = "input"
		}

		if err := g.recordCheckpoint(ctx, &snapshot, beforeState, nodeName, source, currentStep); err != nil {
			return snapshot, fmt.Errorf("failed to store checkpoint: %w", err)
		}

		if interrupt {
			break
		}
	}

	return snapshot, nil
}

// Stream is the streaming variant of [Graph.Execute].
//
// It returns an iterator that yields [StreamEvent] values as the graph runs.
// LLM token chunks are surfaced as EventLLMChunk events so callers can forward
// them to clients in real time. The final event is either "completed" or
// "interrupted", each carrying the current state as its Data field.
func (g *Graph[State]) Stream(ctx context.Context, input Command[State], loc *Location) (iter.Seq2[StreamEvent, error], error) {
	return func(yield func(StreamEvent, error) bool) {
		adapter := &streamAdapter{
			eventYield: yield,
		}

		// Inject the unified writer
		execCtx := stream.WithWriter(ctx, adapter)

		snapshot, err := g.Execute(execCtx, input, loc)
		if err != nil {
			yield(StreamEvent{
				Graph: g.name,
				Event: "error",
				Data:  err,
			}, err)
			return
		}

		if snapshot.IsDone() {
			yield(StreamEvent{
				Graph: g.name,
				Event: EventCompleted,
				Data:  snapshot,
			}, nil)
		} else {
			yield(StreamEvent{
				Graph: g.name,
				Event: EventInterrupted,
				Data:  snapshot,
			}, nil)
		}
	}, nil
}

// Load retrieves a specific point-in-time snapshot without executing any nodes.
// This is essential for UI rendering, history auditing, and time-travel debugging.
func (g *Graph[State]) Load(ctx context.Context, loc Location) (*Snapshot[State], error) {
	if g.checkpointer == nil {
		return nil, errors.New("graph does not have a checkpointer configured")
	}

	checkpoint, err := g.checkpointer.Load(ctx, loc)
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoint: %w", err)
	}

	if checkpoint == nil {
		return nil, ErrCheckpointNotFound
	}

	data, err := json.Marshal(checkpoint.State)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal checkpoint state map: %w", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal checkpoint state: %w", err)
	}

	return &Snapshot[State]{
		State:      state,
		Next:       checkpoint.Next,
		Location:   checkpoint.Location,
		Parent:     checkpoint.Parent,
		CreateTime: checkpoint.Timestamp,
		Metadata:   checkpoint.Metadata,
	}, nil
}

// getNodes resolves a slice of node names into the corresponding [Node] values
// registered in the graph, returning an error if any name is missing.
func (g *Graph[State]) getNodes(names []string) ([]Node[State], error) {
	var nodes []Node[State]
	for _, name := range names {
		node, ok := g.nodes[name]
		if !ok {
			return nil, fmt.Errorf("node %q not found in graph", name)
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// transition applies a [Command] to the current [Snapshot], mints a new
// checkpoint location, and resolves the next set of nodes via the edge table.
// It returns the updated snapshot, a boolean indicating whether execution
// should pause (true when cmd is [Interruptable]), and any error encountered.
func (g *Graph[State]) transition(current Snapshot[State], cmd Command[State]) (Snapshot[State], bool, error) {
	current = current.Copy() // Avoid mutating the input snapshot

	if cmd != nil {
		current.State = cmd.Apply(current.State) // copy is not needed because the snapshot state is already copied above
	}

	parent := current.Location
	current.Parent = &parent
	current.CreateTime = time.Now()

	checkpointID, err := uuid.NewV7()
	if err != nil {
		return current, false, fmt.Errorf("failed to generate checkpoint ID: %w", err)
	}

	current.Location = Location{
		ThreadID:     parent.ThreadID,
		CheckpointNS: parent.CheckpointNS,
		CheckpointID: checkpointID.String(),
	}

	_, interrupt := cmd.(Interruptable)
	if interrupt {
		return current, true, nil
	}

	current.Next, err = g.getNextNodes(current.Next, current.State)
	if err != nil {
		return current, false, fmt.Errorf("failed to get next nodes: %w", err)
	}

	return current, false, nil
}

// getNextNodes evaluates the outgoing edges for each node in current and
// returns the names of all nodes whose conditions are satisfied by state.
// Edges pointing to [END] are excluded from the result.
func (g *Graph[State]) getNextNodes(current []string, state State) ([]string, error) {
	var next []string
	for _, name := range current {
		edges, ok := g.edges[name]
		if !ok {
			return nil, fmt.Errorf("edges for node %q not found in graph", name)
		}

		for _, edge := range edges {
			nextNode, err := edge.NextNode(state)
			if err != nil {
				return nil, fmt.Errorf("failed to evaluate edge from node %q: %w", name, err)
			}
			if nextNode == END {
				continue
			}
			if nextNode != "" {
				next = append(next, nextNode)
			}
		}
	}

	return next, nil
}

// CheckpointSavedEvent is the event name emitted after a checkpoint has been
// successfully persisted. The event Data is the [Location] of the new checkpoint.
const CheckpointSavedEvent = "on_checkpoint_saved"

// recordCheckpoint persists the given snapshot via the configured
// [Checkpointer]. If no checkpointer is set, the call is a no-op.
// After persisting, it emits a [CheckpointSavedEvent] on the [stream.Writer]
// stored in ctx (if any).
func (g *Graph[State]) recordCheckpoint(ctx context.Context, snapshot *Snapshot[State], beforeState State, nodeName string, source string, step int) error {
	if g.checkpointer == nil {
		return nil // No checkpointer, skip storing
	}

	beforeChannels, err := DecomposeState(beforeState)
	if err != nil {
		return fmt.Errorf("failed to decompose before state: %w", err)
	}
	afterChannels, err := DecomposeState(snapshot.State)
	if err != nil {
		return fmt.Errorf("failed to decompose after state: %w", err)
	}

	writes := make(map[string]any)
	for name, afterData := range afterChannels {
		beforeData, exists := beforeChannels[name]
		if !exists || string(beforeData) != string(afterData) {
			var val any
			if err := json.Unmarshal(afterData, &val); err != nil {
				return err
			}
			writes[name] = val
		}
	}

	meta := CheckpointMetadata{
		Node:   nodeName,
		Source: source,
		Writes: writes,
		Step:   step,
	}

	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint metadata: %w", err)
	}

	snapshot.Metadata = metaBytes

	checkpoint := Checkpoint{
		Location:  snapshot.Location,
		Parent:    snapshot.Parent,
		State:     afterChannels,
		Next:      snapshot.Next,
		Timestamp: snapshot.CreateTime,
		Metadata:  metaBytes,
	}

	if err := g.checkpointer.Record(ctx, checkpoint); err != nil {
		return err
	}

	// Notify stream consumers that a new checkpoint has been saved.
	if sw, ok := stream.WriterFromContext(ctx); ok {
		if err := sw.Write(ctx, stream.Event{Name: CheckpointSavedEvent, Data: snapshot.Location}); err != nil {
			return fmt.Errorf("failed to emit checkpoint saved event: %w", err)
		}
	}

	return nil
}

// Node is the unit of work inside a graph.
// Each Node receives the current State, performs its logic (e.g. an LLM call,
// a database lookup, or a branching decision), and returns a [Command] that
// describes how the state should be updated before the next node runs.
// Node is the execution unit of a graph. Each node receives the current State,
// performs arbitrary work (LLM calls, tool invocations, etc.), and returns a
// [Command] that describes how to update the state before the next node runs.
type Node[State any] interface {
	Execute(context.Context, State) (Command[State], error)
}

// NodeFunc wraps a plain function into a [Node], allowing lightweight nodes
// to be defined inline without declaring a named type.
// NodeFunc wraps a plain function into a [Node], allowing inline node
// definitions without declaring a named type.
func NodeFunc[State any](fn func(context.Context, State) (Command[State], error)) Node[State] {
	return NodeFn[State](fn)
}

// NodeFn is the function type that backs [NodeFunc].
type NodeFn[State any] func(context.Context, State) (Command[State], error)

// Execute implements [Node] by calling the underlying function.
func (f NodeFn[State]) Execute(ctx context.Context, state State) (Command[State], error) {
	return f(ctx, state)
}

type startNode[State any] struct{}

func (n *startNode[State]) Execute(ctx context.Context, state State) (Command[State], error) {
	return nil, nil // No command, just pass through the state
}

type endNode[State any] struct{}

func (n *endNode[State]) Execute(ctx context.Context, state State) (Command[State], error) {
	return nil, nil // No command, just pass through the state
}

type State[T any] interface {
	Copy() T
}
