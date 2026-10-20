package graph

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// Transition describes a possible static transition from an [Edge] for
// compile-time graph validation and visualization (such as Mermaid diagrams).
type Transition struct {
	Target      string // Destination node name (or END)
	Label       string // Optional edge label (e.g. route key or condition name)
	Conditional bool   // True for conditional/dashed transitions
}

// Edge represents a directed connection from one node to another, optionally
// guarded by a condition or router that determines whether the edge is active for a given state.
type Edge[State any] interface {
	NextNode(ctx context.Context, state State) (string, error)
	Validate() error
	Transitions() []Transition
}

// SimpleEdge is a basic [Edge] implementation that unconditionally points to a single next node.
type SimpleEdge[State any] struct {
	Next string
}

func (e SimpleEdge[State]) NextNode(ctx context.Context, state State) (string, error) {
	return e.Next, nil
}

func (e SimpleEdge[State]) Validate() error {
	if e.Next == "" {
		return errors.New("edge target cannot be empty")
	}
	return nil
}

func (e SimpleEdge[State]) Transitions() []Transition {
	return []Transition{{Target: e.Next}}
}

// ConditionalEdge is a simple [Edge] implementation that points to a single next node
// and evaluates a boolean condition to determine if that node should be executed next.
type ConditionalEdge[State any] struct {
	Next      string
	Condition func(State) bool
}

func (e ConditionalEdge[State]) NextNode(ctx context.Context, state State) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if e.Condition(state) {
		return e.Next, nil
	}
	return "", nil // Condition not met, no next node
}

func (e ConditionalEdge[State]) Validate() error {
	if e.Next == "" {
		return errors.New("conditional edge target cannot be empty")
	}
	if e.Condition == nil {
		return errors.New("conditional edge condition cannot be nil")
	}
	return nil
}

func (e ConditionalEdge[State]) Transitions() []Transition {
	return []Transition{{Target: e.Next, Label: "conditional", Conditional: true}}
}

// RouteEdge is an [Edge] implementation that supports dynamic routing to multiple possible next nodes.
// It uses a user-provided function to determine the route key based on the current state,
// and then looks up the corresponding next node in a routes map.
type RouteEdge[State any, Key comparable] struct {
	Routes  map[Key]string
	RouteFn func(State) (Key, error)
}

func (e RouteEdge[State, Key]) NextNode(ctx context.Context, state State) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(e.Routes) == 0 {
		return "", fmt.Errorf("edge routes map is empty")
	}

	if e.RouteFn == nil {
		return "", fmt.Errorf("edge route function is nil")
	}

	routeKey, err := e.RouteFn(state)
	if err != nil {
		return "", fmt.Errorf("failed to resolve edge route: %w", err)
	}

	next, ok := e.Routes[routeKey]
	if !ok {
		return "", fmt.Errorf("no route found for key: %v", routeKey)
	}

	return next, nil
}

func (e RouteEdge[State, Key]) Validate() error {
	if len(e.Routes) == 0 {
		return errors.New("route edge routes map cannot be empty")
	}
	if e.RouteFn == nil {
		return errors.New("route edge router function cannot be nil")
	}
	for k, target := range e.Routes {
		if target == "" {
			return fmt.Errorf("route edge target for key %v cannot be empty", k)
		}
	}
	return nil
}

func (e RouteEdge[State, Key]) Transitions() []Transition {
	keys := make([]Key, 0, len(e.Routes))
	for k := range e.Routes {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b Key) int {
		sa, sb := fmt.Sprint(a), fmt.Sprint(b)
		if sa < sb {
			return -1
		}
		if sa > sb {
			return 1
		}
		return 0
	})
	trs := make([]Transition, 0, len(keys))
	for _, k := range keys {
		trs = append(trs, Transition{
			Target:      e.Routes[k],
			Label:       fmt.Sprint(k),
			Conditional: false,
		})
	}
	return trs
}
