package graph

// Builder constructs a [Graph] using a fluent API.
// Call [New] to obtain a zero-value builder, configure it with the With*/Add*
// methods, and finally call [Builder.Build] to compile the graph.
type Builder[S State[S]] struct {
	name         string
	nodes        map[string]Node[S]
	edges        map[string][]Edge[S]
	checkpointer Checkpointer
}

// New returns a new [Builder] for a graph whose State type is State.
// The graph is given the name "default"; override it with [Builder.WithName].
func New[S State[S]]() *Builder[S] {
	return &Builder[S]{
		name:  "default",
		nodes: make(map[string]Node[S]),
		edges: make(map[string][]Edge[S]),
	}
}

// WithName sets the human-readable name of the graph.
// The name surfaces in [StreamEvent] and log output to distinguish
// multiple graphs running in the same process.
func (b *Builder[State]) WithName(name string) *Builder[State] {
	b.name = name
	return b
}

// WithCheckpointer attaches a [Checkpointer] to the graph.
// When set, the graph persists a [Snapshot] to durable storage after each node
// and can be resumed later via [Graph.Execute] with a [Location].
func (b *Builder[State]) WithCheckpointer(checkpointer Checkpointer) *Builder[State] {
	b.checkpointer = checkpointer
	return b
}

// AddNode registers a named node in the graph.
// The name must be unique within the graph and is referenced by [Builder.AddEdge]
// and [Builder.AddConditionalEdge] to define the execution flow.
func (b *Builder[State]) AddNode(name string, node Node[State]) *Builder[State] {
	b.nodes[name] = node
	return b
}

// AddEdge adds an unconditional directed edge from node from to node to.
// The destination node always runs after the source node finishes.
func (b *Builder[State]) AddEdge(from string, to string) *Builder[State] {
	b.edges[from] = append(b.edges[from], SimpleEdge[State]{
		Next: to,
	})
	return b
}

// AddConditionalEdge adds a conditional directed edge from node from to node to.
// The destination node runs only when condition returns true for the current state,
// enabling branching logic (fan-out, routing, loops, etc.).
func (b *Builder[State]) AddConditionalEdge(from string, to string, condition func(State) bool) *Builder[State] {
	b.edges[from] = append(b.edges[from], ConditionalEdge[State]{
		Next:      to,
		Condition: condition,
	})
	return b
}

// AddRouteEdge adds a routed directed edge from node from to node to.
// The destination node runs only when the router returns true for the current state,
// enabling dynamic routing logic where the next node is determined at runtime.
func (b *Builder[State]) AddRouteEdge(from string, router func(State) (string, error), routes map[string]string) *Builder[State] {
	b.edges[from] = append(b.edges[from], RouteEdge[State, string]{
		Routes:  routes,
		RouteFn: router,
	})
	return b
}

// AddRouteEdge adds a routed directed edge from node from to node to.
// The destination node runs only when the router returns true for the current state,
// enabling dynamic routing logic where the next node is determined at runtime.
func AddRouteEdge[S State[S], Key comparable](b *Builder[S], from string, router func(S) (Key, error), routes map[Key]string) *Builder[S] {
	b.edges[from] = append(b.edges[from], RouteEdge[S, Key]{
		Routes:  routes,
		RouteFn: router,
	})
	return b
}

// Build compiles the builder configuration into a runnable [Graph].
// It implicitly injects the virtual [START] and [END] nodes before returning.
func (b *Builder[State]) Build() (*Graph[State], error) {
	// Add start and end nodes if they don't exist
	b.nodes[START] = &startNode[State]{}
	b.nodes[END] = &endNode[State]{}

	return &Graph[State]{
		name:         b.name,
		nodes:        b.nodes,
		edges:        b.edges,
		checkpointer: b.checkpointer,
	}, nil
}
