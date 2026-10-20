package graph

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

const (
	NSSep = "|"
	NSEnd = ":"
)

func computeTaskID(threadID, checkpointNS string, step int, nodeName string) string {
	seed := fmt.Sprintf("%s|%s|%d|%s", threadID, checkpointNS, step, nodeName)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed)).String()
}

// AsNode returns a [Node] that executes the graph as a subgraph inside a parent graph.
// It scopes checkpoints to a nested namespace based on the parent's location, node name, and task ID,
// and preserves interrupt semantics if the subgraph pauses before completion.
func (g *Graph[State]) AsNode() Node[State] {
	return NodeFunc[State](func(ctx context.Context, state State) (Command[State], error) {
		var childLoc *Location
		if rt, ok := RuntimeFromContext(ctx); ok {
			ns := rt.Location.CheckpointNS
			if rt.NodeName != "" {
				seg := rt.NodeName
				if rt.TaskID != "" {
					seg = rt.NodeName + NSEnd + rt.TaskID
				}
				if ns != "" {
					ns = ns + NSSep + seg
				} else {
					ns = seg
				}
			}
			childLoc = &Location{
				ThreadID:     rt.Location.ThreadID,
				CheckpointNS: ns,
			}
		}

		snap, err := g.Execute(ctx, Update[State](func(_ State) State { return state.Copy() }), childLoc)
		if err != nil {
			return nil, err
		}

		if !snap.IsDone() {
			return InterruptUpdate[State](func(_ State) State { return snap.State.Copy() }), nil
		}

		return Update[State](func(_ State) State { return snap.State.Copy() }), nil
	})
}

// MapNode adapts a Node[C] that operates on child state C into a Node[P]
// that operates on parent state P using the provided state conversion functions.
// It preserves interrupt semantics if the underlying node returns an Interruptable command.
func MapNode[P State[P], C State[C]](
	node Node[C],
	toChild func(P) C,
	fromChild func(P, C) P,
) Node[P] {
	return NodeFunc[P](func(ctx context.Context, parentState P) (Command[P], error) {
		childState := toChild(parentState.Copy())
		cmd, err := node.Execute(ctx, childState.Copy())
		if err != nil {
			return nil, err
		}

		updatedChild := childState
		if cmd != nil {
			updatedChild = cmd.Apply(childState)
		}

		if _, isInterrupt := cmd.(Interruptable); isInterrupt {
			return InterruptUpdate[P](func(p P) P {
				return fromChild(p, updatedChild.Copy())
			}), nil
		}

		if cmd == nil {
			return nil, nil
		}

		return Update[P](func(p P) P {
			return fromChild(p, updatedChild.Copy())
		}), nil
	})
}
