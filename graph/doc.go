// Package graph provides a graph-based workflow engine for defining and
// executing AI agent workflows.
//
// # Core Concepts
//
// A [Graph] is a directed, possibly-cyclic network of nodes connected by
// conditional edges. Each [Node] receives the current State, performs its
// work (e.g. an LLM call, a tool invocation, or a branching decision), and
// returns a [Command] describing the state mutation to apply before the next
// node runs.
//
// Graphs are built with [Builder]:
//
//	g, err := graph.New[MyState]().
//	    WithName("planner").
//	    WithCheckpointer(cp).
//	    AddNode("think", thinkNode).
//	    AddNode("act", actNode).
//	    AddEdge(graph.START, "think").
//	    AddConditionalEdge("think", "act",   func(s MyState) bool { return s.ShouldAct }).
//	    AddConditionalEdge("think", graph.END, func(s MyState) bool { return !s.ShouldAct }).
//	    AddEdge("act", graph.END).
//	    Build()
//
// # Execution
//
// [Graph.Execute] runs the graph synchronously and returns a [Snapshot] that
// represents the execution state at completion (or at an interrupt).
// [Graph.Stream] wraps Execute and exposes token-level output as an iterator
// of [StreamEvent] values, enabling real-time streaming to HTTP clients.
//
// # Checkpointing
//
// When a [Checkpointer] is attached, the graph saves a [Checkpoint] after
// every node transition. Callers can resume a paused graph by passing the
// [Location] from a previous [Snapshot] to Execute or Stream.
//
// # Interrupts
//
// A node signals a voluntary pause by returning [InterrupCmd]. The graph
// stops immediately after saving the checkpoint and returns control to the
// caller. Execution can be resumed later with a new [Command] applied on top
// of the saved state.
package graph
