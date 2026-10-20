package graph

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/masterkeysrd/loom/store"
)

// Builder constructs a [Graph] using a fluent API.
// Call [New] to obtain a zero-value builder, configure it with the With*/Add*
// methods, and finally call [Builder.Build] to compile the graph.
type Builder[S State[S]] struct {
	name         string
	nodes        map[string]Node[S]
	edges        map[string][]Edge[S]
	checkpointer Checkpointer
	store        store.Store
	maxSteps     int
	middleware   []Middleware[S]
}

// New returns a new [Builder] for a graph whose State type is State.
// The graph is given the name "default"; override it with [Builder.WithName].
func New[S State[S]]() *Builder[S] {
	return &Builder[S]{
		name:         "default",
		nodes:        make(map[string]Node[S]),
		edges:        make(map[string][]Edge[S]),
		checkpointer: NoopCheckpointer{},
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
	if checkpointer == nil {
		checkpointer = NoopCheckpointer{}
	}
	b.checkpointer = checkpointer
	return b
}

// WithStore attaches a [store.Store] to the graph.
// When set, nodes can retrieve the store via [store.FromContext].
func (b *Builder[State]) WithStore(s store.Store) *Builder[State] {
	b.store = s
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

// WithMaxSteps sets the maximum number of steps allowed per execution run.
func (b *Builder[State]) WithMaxSteps(maxSteps int) *Builder[State] {
	b.maxSteps = maxSteps
	return b
}

// WithMiddleware adds one or more node middlewares to the graph.
func (b *Builder[State]) WithMiddleware(mws ...Middleware[State]) *Builder[State] {
	b.middleware = append(b.middleware, mws...)
	return b
}

// Build compiles the builder configuration into a runnable [Graph].
// It validates node names, node definitions, outgoing edges, and targets,
// and implicitly injects the virtual [START] and [END] nodes before returning.
func (b *Builder[State]) Build() (*Graph[State], error) {
	// 1. Validate User Nodes
	nodeNames := make([]string, 0, len(b.nodes))
	for name := range b.nodes {
		nodeNames = append(nodeNames, name)
	}
	slices.Sort(nodeNames)

	for _, name := range nodeNames {
		node := b.nodes[name]
		if name == "" {
			return nil, errors.New("node name cannot be empty")
		}
		if name == START || name == END {
			return nil, fmt.Errorf("node name %q is reserved", name)
		}
		if strings.ContainsAny(name, "|:") {
			return nil, fmt.Errorf("invalid node name %q: node names cannot contain '|' or ':'", name)
		}
		if node == nil {
			return nil, fmt.Errorf("node %q cannot be nil", name)
		}
	}

	// 2. Validate START and END Edges
	if len(b.edges[START]) == 0 {
		return nil, errors.New("graph must have at least one outgoing edge from START")
	}
	if len(b.edges[END]) > 0 {
		return nil, errors.New("END node cannot have outgoing edges")
	}

	// 3. Clone b.nodes and b.edges before injecting START and END
	nodes := maps.Clone(b.nodes)
	nodes[START] = &startNode[State]{}
	nodes[END] = &endNode[State]{}

	edges := make(map[string][]Edge[State], len(b.edges))
	for k, v := range b.edges {
		edges[k] = slices.Clone(v)
	}

	// 4. Validate Outgoing Edges for All User Nodes
	for _, name := range nodeNames {
		if len(b.edges[name]) == 0 {
			return nil, fmt.Errorf("node %q has no outgoing edges", name)
		}
	}

	// 5. Validate All Edges & Targets
	fromNodes := make([]string, 0, len(b.edges))
	for from := range b.edges {
		fromNodes = append(fromNodes, from)
	}
	slices.Sort(fromNodes)

	for _, from := range fromNodes {
		edgeList := b.edges[from]
		if _, ok := nodes[from]; !ok {
			return nil, fmt.Errorf("edge originates from unknown node %q", from)
		}
		for _, edge := range edgeList {
			if edge == nil {
				return nil, fmt.Errorf("edge from node %q cannot be nil", from)
			}
			if ev, ok := edge.(edgeValidator); ok {
				if err := ev.validate(); err != nil {
					return nil, fmt.Errorf("invalid edge from node %q: %w", from, err)
				}
				targets := ev.targets()
				slices.Sort(targets)
				for _, target := range targets {
					if target == START {
						return nil, fmt.Errorf("edge from node %q cannot target START", from)
					}
					if _, ok := nodes[target]; !ok {
						return nil, fmt.Errorf("edge from node %q targets unknown node %q", from, target)
					}
				}
			}
		}
	}

	return &Graph[State]{
		name:         b.name,
		nodes:        nodes,
		edges:        edges,
		checkpointer: b.checkpointer,
		store:        b.store,
		maxSteps:     b.maxSteps,
		middleware:   slices.Clone(b.middleware),
	}, nil
}
