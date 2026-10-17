package decision

import (
	"context"
	"fmt"

	"github.com/masterkeysrd/loom/graph"
)

var (
	_ graph.Edge[any] = (*PredicateEdge[any])(nil)
	_ graph.Edge[any] = (*ChoiceRouteEdge[any])(nil)
)

// PredicateEdge evaluates a binary decision predicate question against the workflow state
// and directs execution to Next if the predicate passes, or ElseNode otherwise.
type PredicateEdge[State any] struct {
	Model        *Model
	Next         string
	ElseNode     string
	Instructions string
	Threshold    float64
	StateFn      func(State) Input
	Opts         []CallOption
}

// NewConditionalEdge creates a new [PredicateEdge] that directs to next if the evaluated
// predicate meets or exceeds threshold.
func NewConditionalEdge[State any](
	model *Model,
	next string,
	instructions string,
	threshold float64,
	stateFn func(State) Input,
) *PredicateEdge[State] {
	return &PredicateEdge[State]{
		Model:        model,
		Next:         next,
		Instructions: instructions,
		Threshold:    threshold,
		StateFn:      stateFn,
	}
}

// WithElse sets the destination node when the predicate fails the threshold or is refused.
func (e *PredicateEdge[State]) WithElse(elseNode string) *PredicateEdge[State] {
	e.ElseNode = elseNode
	return e
}

// WithOptions appends call options for the decision evaluation.
func (e *PredicateEdge[State]) WithOptions(opts ...CallOption) *PredicateEdge[State] {
	e.Opts = append(e.Opts, opts...)
	return e
}

// NextNode evaluates the binary predicate against state and determines the next node.
func (e *PredicateEdge[State]) NextNode(state State) (string, error) {
	if e.Model == nil {
		return "", fmt.Errorf("decision: model cannot be nil")
	}
	if e.StateFn == nil {
		return "", fmt.Errorf("decision: stateFn cannot be nil")
	}

	ans, err := e.Model.Check(context.Background(), e.StateFn(state), e.Instructions, e.Opts...)
	if err != nil {
		return "", fmt.Errorf("decision: predicate check failed: %w", err)
	}
	if ans == nil {
		return "", fmt.Errorf("decision: missing answer from predicate check")
	}

	if ans.Passed(e.Threshold) {
		return e.Next, nil
	}
	return e.ElseNode, nil
}

// TargetRoutes returns an identity mapping of all destination nodes that this edge may route to.
func (e *PredicateEdge[State]) TargetRoutes() map[string]string {
	targets := make(map[string]string, 2)
	if e.Next != "" {
		targets[e.Next] = e.Next
	}
	if e.ElseNode != "" {
		targets[e.ElseNode] = e.ElseNode
	}
	return targets
}

// AddConditionalEdge registers a [PredicateEdge] with a [graph.Builder] from node from.
func AddConditionalEdge[S graph.State[S]](b *graph.Builder[S], from string, edge *PredicateEdge[S]) *graph.Builder[S] {
	return b.AddRouteEdge(from, edge.NextNode, edge.TargetRoutes())
}

// ChoiceRouteEdge evaluates a categorical choice question against the workflow state
// and directs execution to the destination node mapped to the chosen option.
type ChoiceRouteEdge[State any] struct {
	Model         *Model
	Instructions  string
	Choices       []Option
	Routes        map[string]string
	FallbackNode  string
	MinConfidence float64
	StateFn       func(State) Input
	Opts          []CallOption
}

// NewRouteEdge creates a new [ChoiceRouteEdge] that evaluates choices and routes to the mapped target.
func NewRouteEdge[State any](
	model *Model,
	instructions string,
	choices []Option,
	routes map[string]string,
	stateFn func(State) Input,
) *ChoiceRouteEdge[State] {
	return &ChoiceRouteEdge[State]{
		Model:        model,
		Instructions: instructions,
		Choices:      choices,
		Routes:       routes,
		StateFn:      stateFn,
	}
}

// WithMinConfidence sets the minimum confidence required to accept the evaluated choice.
// If confidence is below this value, routing falls back to FallbackNode.
func (e *ChoiceRouteEdge[State]) WithMinConfidence(minConfidence float64) *ChoiceRouteEdge[State] {
	e.MinConfidence = minConfidence
	return e
}

// WithFallback sets the destination node when the choice is refused, confidence is below
// MinConfidence, or the selected choice is not found in Routes.
func (e *ChoiceRouteEdge[State]) WithFallback(fallbackNode string) *ChoiceRouteEdge[State] {
	e.FallbackNode = fallbackNode
	return e
}

// WithOptions appends call options for the decision evaluation.
func (e *ChoiceRouteEdge[State]) WithOptions(opts ...CallOption) *ChoiceRouteEdge[State] {
	e.Opts = append(e.Opts, opts...)
	return e
}

// NextNode evaluates the choice question against state and returns the mapped destination node.
func (e *ChoiceRouteEdge[State]) NextNode(state State) (string, error) {
	if e.Model == nil {
		return "", fmt.Errorf("decision: model cannot be nil")
	}
	if e.StateFn == nil {
		return "", fmt.Errorf("decision: stateFn cannot be nil")
	}

	ans, err := e.Model.Choose(context.Background(), e.StateFn(state), e.Instructions, e.Choices, e.Opts...)
	if err != nil {
		return "", fmt.Errorf("decision: choice evaluation failed: %w", err)
	}
	if ans == nil {
		return "", fmt.Errorf("decision: missing answer from choice evaluation")
	}

	if ans.Refused {
		if e.FallbackNode != "" {
			return e.FallbackNode, nil
		}
		return "", fmt.Errorf("decision: choice evaluation was refused")
	}

	if ans.Confidence < e.MinConfidence {
		if e.FallbackNode != "" {
			return e.FallbackNode, nil
		}
		return "", fmt.Errorf("decision: choice confidence %.4f is below minimum %.4f", ans.Confidence, e.MinConfidence)
	}

	target, ok := e.Routes[ans.Choice]
	if !ok {
		if e.FallbackNode != "" {
			return e.FallbackNode, nil
		}
		return "", fmt.Errorf("decision: no route found for choice %q", ans.Choice)
	}

	return target, nil
}

// TargetRoutes returns an identity mapping of all destination nodes that this edge may route to.
func (e *ChoiceRouteEdge[State]) TargetRoutes() map[string]string {
	targets := make(map[string]string, len(e.Routes)+1)
	for _, target := range e.Routes {
		targets[target] = target
	}
	if e.FallbackNode != "" {
		targets[e.FallbackNode] = e.FallbackNode
	}
	return targets
}

// AddRouteEdge registers a [ChoiceRouteEdge] with a [graph.Builder] from node from.
func AddRouteEdge[S graph.State[S]](b *graph.Builder[S], from string, edge *ChoiceRouteEdge[S]) *graph.Builder[S] {
	return b.AddRouteEdge(from, edge.NextNode, edge.TargetRoutes())
}
