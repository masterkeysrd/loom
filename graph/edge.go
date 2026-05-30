package graph

import "fmt"

// Edge represents a directed connection from one node to another, optionally
// guarded by a condition that determines whether the edge is active for a given state.
type Edge[State any] interface {
	NextNode(state State) (string, error)
}

// SimpleEdge is a basic [Edge] implementation that unconditionally points to a single next node.
type SimpleEdge[State any] struct {
	Next string
}

func (e SimpleEdge[State]) NextNode(state State) (string, error) {
	return e.Next, nil
}

// ConditionalEdge is a simple [Edge] implementation that points to a single next node
// and evaluates a boolean condition to determine if that node should be executed next.
type ConditionalEdge[State any] struct {
	Next      string
	Condition func(State) bool
}

func (e ConditionalEdge[State]) NextNode(state State) (string, error) {
	if e.Condition(state) {
		return e.Next, nil
	}
	return "", nil // Condition not met, no next node
}

// RouteEdge is an [Edge] implementation that supports dynamic routing to multiple possible next nodes.
// It uses a user-provided function to determine the route key based on the current state,
// and then looks up the corresponding next node in a routes map.
type RouteEdge[State any, Key comparable] struct {
	Routes  map[Key]string
	RouteFn func(State) (Key, error)
}

func (e RouteEdge[State, Key]) NextNode(state State) (string, error) {
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
