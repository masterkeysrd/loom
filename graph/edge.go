package graph

import (
	"errors"
	"fmt"
)

// Edge represents a directed connection from one node to another, optionally
// guarded by a condition that determines whether the edge is active for a given state.
type Edge[State any] interface {
	NextNode(state State) (string, error)
}

type edgeValidator interface {
	validate() error
	targets() []string
}

// SimpleEdge is a basic [Edge] implementation that unconditionally points to a single next node.
type SimpleEdge[State any] struct {
	Next string
}

func (e SimpleEdge[State]) NextNode(state State) (string, error) {
	return e.Next, nil
}

func (e SimpleEdge[State]) validate() error {
	if e.Next == "" {
		return errors.New("edge target cannot be empty")
	}
	return nil
}

func (e SimpleEdge[State]) targets() []string {
	return []string{e.Next}
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

func (e ConditionalEdge[State]) validate() error {
	if e.Next == "" {
		return errors.New("conditional edge target cannot be empty")
	}
	if e.Condition == nil {
		return errors.New("conditional edge condition cannot be nil")
	}
	return nil
}

func (e ConditionalEdge[State]) targets() []string {
	return []string{e.Next}
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

func (e RouteEdge[State, Key]) validate() error {
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

func (e RouteEdge[State, Key]) targets() []string {
	targets := make([]string, 0, len(e.Routes))
	for _, target := range e.Routes {
		targets = append(targets, target)
	}
	return targets
}
