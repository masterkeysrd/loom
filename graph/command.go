package graph

// Command represents an action that can be applied to a state to produce a new state.
type Command[State any] interface {
	Apply(State) State
}

// Update is a function-based [Command] that applies an arbitrary transformation
// to the state. It is the most common way to return state mutations from a node.
type Update[State any] func(State) State

// Apply implements [Command] by calling the underlying function.
func (f Update[State]) Apply(s State) State {
	return f(s)
}

// Interruptable represents a command that can interrupt a node's execution.
type Interruptable interface {
	Interrupt()
}

// InterrupCmd is the concrete [Command] implementation that pauses graph
// execution. When a node returns an InterrupCmd, [Graph.Execute] stops
// after saving the current checkpoint and returns the snapshot to the caller
// so execution can be resumed later with a new input.
type InterrupCmd[State any] struct {
}

// Interrupt constructs an interrupt command for a graph whose state type is any.
func Interrupt() InterrupCmd[any] {
	return InterrupCmd[any]{}
}

// Apply implements [Command]; it leaves the state unchanged.
func (c InterrupCmd[State]) Apply(s State) State {
	return s
}

// Interrupt implements [Interruptable], marking this command as an interrupt.
func (c InterrupCmd[State]) Interrupt() {}
