package decision_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/masterkeysrd/loom/decision"
	"github.com/masterkeysrd/loom/graph"
	"github.com/masterkeysrd/loom/stream"
)

type mockEdgeProvider struct {
	evaluateFunc func(ctx context.Context, req *decision.Request) (*decision.Response, error)
}

func (m *mockEdgeProvider) Name() string { return "mock-edge-provider" }

func (m *mockEdgeProvider) Evaluate(ctx context.Context, req *decision.Request) (*decision.Response, error) {
	if m.evaluateFunc != nil {
		return m.evaluateFunc(ctx, req)
	}
	return &decision.Response{
		Model:   req.Model,
		Answers: make(map[string]decision.Answer),
	}, nil
}

type testWorkflowState struct {
	Text       string `json:"text"`
	RouteTaken string `json:"route_taken"`
}

func (s testWorkflowState) Copy() testWorkflowState {
	return s
}

func TestPredicateEdge(t *testing.T) {
	t.Run("passing threshold", func(t *testing.T) {
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"check": {
							Name:        "check",
							Type:        decision.QuestionPredicate,
							Probability: 0.85,
						},
					},
				}, nil
			},
		}
		model, err := decision.NewModel(provider, "test-model", nil)
		if err != nil {
			t.Fatalf("failed to create model: %v", err)
		}

		edge := decision.NewConditionalEdge(
			model,
			"target_node",
			"Is this request valid?",
			0.7,
			func(s testWorkflowState) decision.Input {
				return decision.Text(s.Text)
			},
		)

		next, err := edge.NextNode(context.Background(), testWorkflowState{Text: "valid request"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if next != "target_node" {
			t.Fatalf("expected next node to be %q, got %q", "target_node", next)
		}
	})

	t.Run("failing threshold without else", func(t *testing.T) {
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"check": {
							Name:        "check",
							Type:        decision.QuestionPredicate,
							Probability: 0.4,
						},
					},
				}, nil
			},
		}
		model, err := decision.NewModel(provider, "test-model", nil)
		if err != nil {
			t.Fatalf("failed to create model: %v", err)
		}

		edge := decision.NewConditionalEdge(
			model,
			"target_node",
			"Is this request valid?",
			0.7,
			func(s testWorkflowState) decision.Input {
				return decision.Text(s.Text)
			},
		)

		next, err := edge.NextNode(context.Background(), testWorkflowState{Text: "invalid request"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if next != "" {
			t.Fatalf("expected empty next node, got %q", next)
		}
	})

	t.Run("failing threshold WithElse", func(t *testing.T) {
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"check": {
							Name:        "check",
							Type:        decision.QuestionPredicate,
							Probability: 0.5,
						},
					},
				}, nil
			},
		}
		model, err := decision.NewModel(provider, "test-model", nil)
		if err != nil {
			t.Fatalf("failed to create model: %v", err)
		}

		edge := decision.NewConditionalEdge(
			model,
			"target_node",
			"Is this request valid?",
			0.8,
			func(s testWorkflowState) decision.Input {
				return decision.Text(s.Text)
			},
		).WithElse("else_node")

		next, err := edge.NextNode(context.Background(), testWorkflowState{Text: "ambiguous"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if next != "else_node" {
			t.Fatalf("expected else node %q, got %q", "else_node", next)
		}
	})

	t.Run("refusal without else", func(t *testing.T) {
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"check": {
							Name:        "check",
							Type:        decision.QuestionPredicate,
							Refused:     true,
							Probability: 0.99,
						},
					},
				}, nil
			},
		}
		model, err := decision.NewModel(provider, "test-model", nil)
		if err != nil {
			t.Fatalf("failed to create model: %v", err)
		}

		edge := decision.NewConditionalEdge(
			model,
			"target_node",
			"Is this allowed?",
			0.5,
			func(s testWorkflowState) decision.Input {
				return decision.Text(s.Text)
			},
		)

		next, err := edge.NextNode(context.Background(), testWorkflowState{Text: "refused input"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if next != "" {
			t.Fatalf("expected empty next node on refusal without else, got %q", next)
		}
	})

	t.Run("refusal WithElse", func(t *testing.T) {
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"check": {
							Name:    "check",
							Type:    decision.QuestionPredicate,
							Refused: true,
						},
					},
				}, nil
			},
		}
		model, err := decision.NewModel(provider, "test-model", nil)
		if err != nil {
			t.Fatalf("failed to create model: %v", err)
		}

		edge := decision.NewConditionalEdge(
			model,
			"target_node",
			"Is this allowed?",
			0.5,
			func(s testWorkflowState) decision.Input {
				return decision.Text(s.Text)
			},
		).WithElse("blocked_node")

		next, err := edge.NextNode(context.Background(), testWorkflowState{Text: "refused input"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if next != "blocked_node" {
			t.Fatalf("expected %q on refusal with else, got %q", "blocked_node", next)
		}
	})

	t.Run("error handling", func(t *testing.T) {
		t.Run("nil model", func(t *testing.T) {
			edge := decision.NewConditionalEdge[testWorkflowState](
				nil,
				"target",
				"check",
				0.5,
				func(s testWorkflowState) decision.Input { return decision.Text(s.Text) },
			)
			_, err := edge.NextNode(context.Background(), testWorkflowState{})
			if err == nil {
				t.Fatal("expected error with nil model, got nil")
			}
		})

		t.Run("nil stateFn", func(t *testing.T) {
			provider := &mockEdgeProvider{}
			model, _ := decision.NewModel(provider, "test-model", nil)
			edge := decision.NewConditionalEdge[testWorkflowState](
				model,
				"target",
				"check",
				0.5,
				nil,
			)
			_, err := edge.NextNode(context.Background(), testWorkflowState{})
			if err == nil {
				t.Fatal("expected error with nil stateFn, got nil")
			}
		})

		t.Run("provider evaluation error", func(t *testing.T) {
			provider := &mockEdgeProvider{
				evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
					return nil, errors.New("upstream provider error")
				},
			}
			model, _ := decision.NewModel(provider, "test-model", nil)
			edge := decision.NewConditionalEdge(
				model,
				"target",
				"check",
				0.5,
				func(s testWorkflowState) decision.Input { return decision.Text(s.Text) },
			)
			_, err := edge.NextNode(context.Background(), testWorkflowState{})
			if err == nil {
				t.Fatal("expected error when provider fails, got nil")
			}
		})
	})
}

func TestChoiceRouteEdge(t *testing.T) {
	choices := []decision.Option{
		decision.Opt("billing", "Billing and invoices"),
		decision.Opt("tech", "Technical troubleshooting"),
	}
	routes := map[string]string{
		"billing": "handle_billing",
		"tech":    "handle_tech",
	}

	t.Run("routing by top choice", func(t *testing.T) {
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"choice": {
							Name:       "choice",
							Type:       decision.QuestionChoice,
							Choice:     "billing",
							Confidence: 0.95,
						},
					},
				}, nil
			},
		}
		model, err := decision.NewModel(provider, "test-model", nil)
		if err != nil {
			t.Fatalf("failed to create model: %v", err)
		}

		edge := decision.NewRouteEdge(
			model,
			"Categorize customer intent",
			choices,
			routes,
			func(s testWorkflowState) decision.Input {
				return decision.Text(s.Text)
			},
		).WithMinConfidence(0.8)

		next, err := edge.NextNode(context.Background(), testWorkflowState{Text: "I want a refund"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if next != "handle_billing" {
			t.Fatalf("expected %q, got %q", "handle_billing", next)
		}
	})

	t.Run("low confidence fallback", func(t *testing.T) {
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"choice": {
							Name:       "choice",
							Type:       decision.QuestionChoice,
							Choice:     "tech",
							Confidence: 0.45,
						},
					},
				}, nil
			},
		}
		model, err := decision.NewModel(provider, "test-model", nil)
		if err != nil {
			t.Fatalf("failed to create model: %v", err)
		}

		t.Run("with fallback configured", func(t *testing.T) {
			edge := decision.NewRouteEdge(
				model,
				"Categorize customer intent",
				choices,
				routes,
				func(s testWorkflowState) decision.Input {
					return decision.Text(s.Text)
				},
			).WithMinConfidence(0.7).WithFallback("handle_fallback")

			next, err := edge.NextNode(context.Background(), testWorkflowState{Text: "Not sure what I need"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if next != "handle_fallback" {
				t.Fatalf("expected fallback node %q, got %q", "handle_fallback", next)
			}
		})

		t.Run("without fallback configured", func(t *testing.T) {
			edge := decision.NewRouteEdge(
				model,
				"Categorize customer intent",
				choices,
				routes,
				func(s testWorkflowState) decision.Input {
					return decision.Text(s.Text)
				},
			).WithMinConfidence(0.7)

			_, err := edge.NextNode(context.Background(), testWorkflowState{Text: "Not sure what I need"})
			if err == nil {
				t.Fatal("expected error when confidence is low and fallback is unset, got nil")
			}
		})
	})

	t.Run("refusal fallback", func(t *testing.T) {
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"choice": {
							Name:    "choice",
							Type:    decision.QuestionChoice,
							Refused: true,
						},
					},
				}, nil
			},
		}
		model, err := decision.NewModel(provider, "test-model", nil)
		if err != nil {
			t.Fatalf("failed to create model: %v", err)
		}

		t.Run("with fallback configured", func(t *testing.T) {
			edge := decision.NewRouteEdge(
				model,
				"Categorize customer intent",
				choices,
				routes,
				func(s testWorkflowState) decision.Input {
					return decision.Text(s.Text)
				},
			).WithFallback("human_escalation")

			next, err := edge.NextNode(context.Background(), testWorkflowState{Text: "dangerous content"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if next != "human_escalation" {
				t.Fatalf("expected %q, got %q", "human_escalation", next)
			}
		})

		t.Run("without fallback configured", func(t *testing.T) {
			edge := decision.NewRouteEdge(
				model,
				"Categorize customer intent",
				choices,
				routes,
				func(s testWorkflowState) decision.Input {
					return decision.Text(s.Text)
				},
			)

			_, err := edge.NextNode(context.Background(), testWorkflowState{Text: "dangerous content"})
			if err == nil {
				t.Fatal("expected error on refusal when fallback is unset, got nil")
			}
		})
	})

	t.Run("unmapped choice", func(t *testing.T) {
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"choice": {
							Name:       "choice",
							Type:       decision.QuestionChoice,
							Choice:     "unknown_option",
							Confidence: 0.99,
						},
					},
				}, nil
			},
		}
		model, _ := decision.NewModel(provider, "test-model", nil)

		t.Run("with fallback configured", func(t *testing.T) {
			edge := decision.NewRouteEdge(
				model,
				"Categorize",
				choices,
				routes,
				func(s testWorkflowState) decision.Input { return decision.Text(s.Text) },
			).WithFallback("handle_fallback")

			next, err := edge.NextNode(context.Background(), testWorkflowState{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if next != "handle_fallback" {
				t.Fatalf("expected %q, got %q", "handle_fallback", next)
			}
		})

		t.Run("without fallback configured", func(t *testing.T) {
			edge := decision.NewRouteEdge(
				model,
				"Categorize",
				choices,
				routes,
				func(s testWorkflowState) decision.Input { return decision.Text(s.Text) },
			)

			_, err := edge.NextNode(context.Background(), testWorkflowState{})
			if err == nil {
				t.Fatal("expected error on unmapped choice without fallback, got nil")
			}
		})
	})

	t.Run("error handling", func(t *testing.T) {
		t.Run("nil model", func(t *testing.T) {
			edge := decision.NewRouteEdge[testWorkflowState](
				nil,
				"Categorize",
				choices,
				routes,
				func(s testWorkflowState) decision.Input { return decision.Text(s.Text) },
			)
			_, err := edge.NextNode(context.Background(), testWorkflowState{})
			if err == nil {
				t.Fatal("expected error with nil model, got nil")
			}
		})

		t.Run("nil stateFn", func(t *testing.T) {
			provider := &mockEdgeProvider{}
			model, _ := decision.NewModel(provider, "test-model", nil)
			edge := decision.NewRouteEdge[testWorkflowState](
				model,
				"Categorize",
				choices,
				routes,
				nil,
			)
			_, err := edge.NextNode(context.Background(), testWorkflowState{})
			if err == nil {
				t.Fatal("expected error with nil stateFn, got nil")
			}
		})

		t.Run("provider evaluation error", func(t *testing.T) {
			provider := &mockEdgeProvider{
				evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
					return nil, errors.New("upstream provider error")
				},
			}
			model, _ := decision.NewModel(provider, "test-model", nil)
			edge := decision.NewRouteEdge(
				model,
				"Categorize",
				choices,
				routes,
				func(s testWorkflowState) decision.Input { return decision.Text(s.Text) },
			)
			_, err := edge.NextNode(context.Background(), testWorkflowState{})
			if err == nil {
				t.Fatal("expected error when provider fails, got nil")
			}
		})
	})
}

func TestChoiceRouteEdge_GraphIntegration(t *testing.T) {
	choices := []decision.Option{
		decision.Opt("billing", "Billing questions"),
		decision.Opt("technical", "Technical support"),
	}
	routes := map[string]string{
		"billing":   "billing_node",
		"technical": "tech_node",
	}

	buildTestGraph := func(t *testing.T, edge *decision.ChoiceRouteEdge[testWorkflowState]) *graph.Graph[testWorkflowState] {
		builder := graph.New[testWorkflowState]().WithName("support_router_graph")

		builder.AddNode("classify", graph.NodeFunc(func(ctx context.Context, s testWorkflowState) (graph.Command[testWorkflowState], error) {
			return nil, nil // Passthrough
		}))

		builder.AddNode("billing_node", graph.NodeFunc(func(ctx context.Context, s testWorkflowState) (graph.Command[testWorkflowState], error) {
			return graph.Update[testWorkflowState](func(state testWorkflowState) testWorkflowState {
				state.RouteTaken = "billing"
				return state
			}), nil
		}))

		builder.AddNode("tech_node", graph.NodeFunc(func(ctx context.Context, s testWorkflowState) (graph.Command[testWorkflowState], error) {
			return graph.Update[testWorkflowState](func(state testWorkflowState) testWorkflowState {
				state.RouteTaken = "technical"
				return state
			}), nil
		}))

		builder.AddNode("fallback_node", graph.NodeFunc(func(ctx context.Context, s testWorkflowState) (graph.Command[testWorkflowState], error) {
			return graph.Update[testWorkflowState](func(state testWorkflowState) testWorkflowState {
				state.RouteTaken = "fallback"
				return state
			}), nil
		}))

		builder.AddEdge(graph.START, "classify")
		decision.AddRouteEdge(builder, "classify", edge)
		builder.AddEdge("billing_node", graph.END)
		builder.AddEdge("tech_node", graph.END)
		builder.AddEdge("fallback_node", graph.END)

		g, err := builder.Build()
		if err != nil {
			t.Fatalf("failed to build graph: %v", err)
		}
		return g
	}

	t.Run("successful route to billing", func(t *testing.T) {
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"choice": {
							Name:       "choice",
							Type:       decision.QuestionChoice,
							Choice:     "billing",
							Confidence: 0.92,
						},
					},
				}, nil
			},
		}
		model, err := decision.NewModel(provider, "test-model", nil)
		if err != nil {
			t.Fatalf("failed to create model: %v", err)
		}

		edge := decision.NewRouteEdge(
			model,
			"Route ticket",
			choices,
			routes,
			func(s testWorkflowState) decision.Input {
				return decision.Text(s.Text)
			},
		).WithFallback("fallback_node").WithMinConfidence(0.7)

		g := buildTestGraph(t, edge)
		initialInput := graph.Update[testWorkflowState](func(s testWorkflowState) testWorkflowState {
			s.Text = "Please send invoice"
			return s
		})

		snap, err := g.Execute(context.Background(), initialInput, nil)
		if err != nil {
			t.Fatalf("unexpected graph execution error: %v", err)
		}
		if snap.State.RouteTaken != "billing" {
			t.Fatalf("expected RouteTaken to be %q, got %q", "billing", snap.State.RouteTaken)
		}
	})

	t.Run("low confidence routes to fallback", func(t *testing.T) {
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"choice": {
							Name:       "choice",
							Type:       decision.QuestionChoice,
							Choice:     "technical",
							Confidence: 0.35,
						},
					},
				}, nil
			},
		}
		model, err := decision.NewModel(provider, "test-model", nil)
		if err != nil {
			t.Fatalf("failed to create model: %v", err)
		}

		edge := decision.NewRouteEdge(
			model,
			"Route ticket",
			choices,
			routes,
			func(s testWorkflowState) decision.Input {
				return decision.Text(s.Text)
			},
		).WithFallback("fallback_node").WithMinConfidence(0.7)

		g := buildTestGraph(t, edge)

		initialInput := graph.Update[testWorkflowState](func(s testWorkflowState) testWorkflowState {
			s.Text = "Something strange is happening"
			return s
		})

		snap, err := g.Execute(context.Background(), initialInput, nil)
		if err != nil {
			t.Fatalf("unexpected graph execution error: %v", err)
		}
		if snap.State.RouteTaken != "fallback" {
			t.Fatalf("expected RouteTaken to be %q, got %q", "fallback", snap.State.RouteTaken)
		}
	})

	t.Run("refusal routes to fallback", func(t *testing.T) {
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"choice": {
							Name:    "choice",
							Type:    decision.QuestionChoice,
							Refused: true,
						},
					},
				}, nil
			},
		}
		model, err := decision.NewModel(provider, "test-model", nil)
		if err != nil {
			t.Fatalf("failed to create model: %v", err)
		}

		edge := decision.NewRouteEdge(
			model,
			"Route ticket",
			choices,
			routes,
			func(s testWorkflowState) decision.Input {
				return decision.Text(s.Text)
			},
		).WithFallback("fallback_node").WithMinConfidence(0.7)

		g := buildTestGraph(t, edge)

		initialInput := graph.Update[testWorkflowState](func(s testWorkflowState) testWorkflowState {
			s.Text = "Refuse this prompt"
			return s
		})

		snap, err := g.Execute(context.Background(), initialInput, nil)
		if err != nil {
			t.Fatalf("unexpected graph execution error: %v", err)
		}
		if snap.State.RouteTaken != "fallback" {
			t.Fatalf("expected RouteTaken to be %q, got %q", "fallback", snap.State.RouteTaken)
		}
	})
}

func TestPredicateEdge_GraphIntegration(t *testing.T) {
	buildTestGraph := func(t *testing.T, edge *decision.PredicateEdge[testWorkflowState]) *graph.Graph[testWorkflowState] {
		builder := graph.New[testWorkflowState]().WithName("access_control_graph")

		builder.AddNode("check_auth", graph.NodeFunc(func(ctx context.Context, s testWorkflowState) (graph.Command[testWorkflowState], error) {
			return nil, nil // Passthrough
		}))

		builder.AddNode("allowed_node", graph.NodeFunc(func(ctx context.Context, s testWorkflowState) (graph.Command[testWorkflowState], error) {
			return graph.Update[testWorkflowState](func(state testWorkflowState) testWorkflowState {
				state.RouteTaken = "allowed"
				return state
			}), nil
		}))

		builder.AddNode("denied_node", graph.NodeFunc(func(ctx context.Context, s testWorkflowState) (graph.Command[testWorkflowState], error) {
			return graph.Update[testWorkflowState](func(state testWorkflowState) testWorkflowState {
				state.RouteTaken = "denied"
				return state
			}), nil
		}))

		builder.AddEdge(graph.START, "check_auth")
		decision.AddConditionalEdge(builder, "check_auth", edge)
		builder.AddEdge("allowed_node", graph.END)
		builder.AddEdge("denied_node", graph.END)

		g, err := builder.Build()
		if err != nil {
			t.Fatalf("failed to build graph: %v", err)
		}
		return g
	}

	t.Run("allowed when threshold passed", func(t *testing.T) {
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"check": {
							Name:        "check",
							Type:        decision.QuestionPredicate,
							Probability: 0.9,
						},
					},
				}, nil
			},
		}
		model, _ := decision.NewModel(provider, "test-model", nil)

		edge := decision.NewConditionalEdge(
			model,
			"allowed_node",
			"Is user authenticated?",
			0.7,
			func(s testWorkflowState) decision.Input {
				return decision.Text(s.Text)
			},
		).WithElse("denied_node")

		g := buildTestGraph(t, edge)
		snap, err := g.Execute(context.Background(), nil, nil)
		if err != nil {
			t.Fatalf("unexpected execution error: %v", err)
		}
		if snap.State.RouteTaken != "allowed" {
			t.Fatalf("expected %q, got %q", "allowed", snap.State.RouteTaken)
		}
	})

	t.Run("denied when threshold failed", func(t *testing.T) {
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"check": {
							Name:        "check",
							Type:        decision.QuestionPredicate,
							Probability: 0.3,
						},
					},
				}, nil
			},
		}
		model, _ := decision.NewModel(provider, "test-model", nil)

		edge := decision.NewConditionalEdge(
			model,
			"allowed_node",
			"Is user authenticated?",
			0.7,
			func(s testWorkflowState) decision.Input {
				return decision.Text(s.Text)
			},
		).WithElse("denied_node")

		g := buildTestGraph(t, edge)
		snap, err := g.Execute(context.Background(), nil, nil)
		if err != nil {
			t.Fatalf("unexpected execution error: %v", err)
		}
		if snap.State.RouteTaken != "denied" {
			t.Fatalf("expected %q, got %q", "denied", snap.State.RouteTaken)
		}
	})
}

func TestDecisionEdges_ValidationAndMermaid(t *testing.T) {
	provider := &mockEdgeProvider{}
	model, err := decision.NewModel(provider, "test-model", nil)
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	stateFn := func(s testWorkflowState) decision.Input {
		return decision.Text(s.Text)
	}

	t.Run("PredicateEdge validation", func(t *testing.T) {
		var nilEdge *decision.PredicateEdge[testWorkflowState]
		if err := nilEdge.Validate(); err == nil {
			t.Error("expected error for nil edge, got nil")
		}

		edgeNilModel := &decision.PredicateEdge[testWorkflowState]{
			Next:    "target",
			StateFn: stateFn,
		}
		if err := edgeNilModel.Validate(); err == nil || !strings.Contains(err.Error(), "model cannot be nil") {
			t.Errorf("expected model cannot be nil error, got: %v", err)
		}

		edgeNilStateFn := &decision.PredicateEdge[testWorkflowState]{
			Model: model,
			Next:  "target",
		}
		if err := edgeNilStateFn.Validate(); err == nil || !strings.Contains(err.Error(), "stateFn cannot be nil") {
			t.Errorf("expected stateFn cannot be nil error, got: %v", err)
		}

		edgeEmptyNext := &decision.PredicateEdge[testWorkflowState]{
			Model:   model,
			StateFn: stateFn,
		}
		if err := edgeEmptyNext.Validate(); err == nil || !strings.Contains(err.Error(), "next node cannot be empty") {
			t.Errorf("expected next node cannot be empty error, got: %v", err)
		}

		validEdge := decision.NewConditionalEdge(model, "pass_node", "check", 0.5, stateFn)
		if err := validEdge.Validate(); err != nil {
			t.Fatalf("unexpected validation error: %v", err)
		}
		trs := validEdge.Transitions()
		if len(trs) != 1 || trs[0].Target != "pass_node" || trs[0].Label != "pass" || !trs[0].Conditional {
			t.Errorf("unexpected transitions for single predicate: %+v", trs)
		}

		validEdgeWithElse := validEdge.WithElse("else_node")
		trsWithElse := validEdgeWithElse.Transitions()
		if len(trsWithElse) != 2 {
			t.Fatalf("expected 2 transitions, got %d", len(trsWithElse))
		}
		if trsWithElse[0].Target != "pass_node" || trsWithElse[0].Label != "pass" || !trsWithElse[0].Conditional {
			t.Errorf("unexpected transition[0]: %+v", trsWithElse[0])
		}
		if trsWithElse[1].Target != "else_node" || trsWithElse[1].Label != "else" || !trsWithElse[1].Conditional {
			t.Errorf("unexpected transition[1]: %+v", trsWithElse[1])
		}
	})

	t.Run("ChoiceRouteEdge validation", func(t *testing.T) {
		var nilEdge *decision.ChoiceRouteEdge[testWorkflowState]
		if err := nilEdge.Validate(); err == nil {
			t.Error("expected error for nil edge, got nil")
		}

		edgeNilModel := &decision.ChoiceRouteEdge[testWorkflowState]{
			Routes:  map[string]string{"opt": "node1"},
			StateFn: stateFn,
		}
		if err := edgeNilModel.Validate(); err == nil || !strings.Contains(err.Error(), "model cannot be nil") {
			t.Errorf("expected model cannot be nil error, got: %v", err)
		}

		edgeNilStateFn := &decision.ChoiceRouteEdge[testWorkflowState]{
			Model:  model,
			Routes: map[string]string{"opt": "node1"},
		}
		if err := edgeNilStateFn.Validate(); err == nil || !strings.Contains(err.Error(), "stateFn cannot be nil") {
			t.Errorf("expected stateFn cannot be nil error, got: %v", err)
		}

		edgeEmptyRoutes := &decision.ChoiceRouteEdge[testWorkflowState]{
			Model:   model,
			StateFn: stateFn,
			Routes:  nil,
		}
		if err := edgeEmptyRoutes.Validate(); err == nil || !strings.Contains(err.Error(), "routes map cannot be empty") {
			t.Errorf("expected routes map cannot be empty error, got: %v", err)
		}

		edgeEmptyTarget := &decision.ChoiceRouteEdge[testWorkflowState]{
			Model:   model,
			StateFn: stateFn,
			Routes:  map[string]string{"opt": ""},
		}
		if err := edgeEmptyTarget.Validate(); err == nil || !strings.Contains(err.Error(), "cannot be empty") {
			t.Errorf("expected route target cannot be empty error, got: %v", err)
		}

		routes := map[string]string{
			"tech":    "tech_node",
			"billing": "billing_node",
		}
		validEdge := decision.NewRouteEdge(model, "select category", nil, routes, stateFn).WithFallback("fallback_node")
		if err := validEdge.Validate(); err != nil {
			t.Fatalf("unexpected validation error: %v", err)
		}

		trs := validEdge.Transitions()
		if len(trs) != 3 {
			t.Fatalf("expected 3 transitions, got %d: %+v", len(trs), trs)
		}
		// Deterministically sorted: "billing", "tech", then fallback
		if trs[0].Target != "billing_node" || trs[0].Label != "billing" || trs[0].Conditional {
			t.Errorf("unexpected transition 0: %+v", trs[0])
		}
		if trs[1].Target != "tech_node" || trs[1].Label != "tech" || trs[1].Conditional {
			t.Errorf("unexpected transition 1: %+v", trs[1])
		}
		if trs[2].Target != "fallback_node" || trs[2].Label != "fallback" || !trs[2].Conditional {
			t.Errorf("unexpected transition 2: %+v", trs[2])
		}
	})

	t.Run("Builder validation with invalid target nodes", func(t *testing.T) {
		b := graph.New[testWorkflowState]()
		b.AddNode("start_node", graph.NodeFunc(func(ctx context.Context, s testWorkflowState) (graph.Command[testWorkflowState], error) {
			return nil, nil
		}))
		b.AddEdge(graph.START, "start_node")

		predEdge := decision.NewConditionalEdge(model, "non_existent_node", "check", 0.5, stateFn)
		decision.AddConditionalEdge(b, "start_node", predEdge)

		_, err := b.Build()
		if err == nil || !strings.Contains(err.Error(), "non_existent_node") {
			t.Fatalf("expected build error for missing target node, got: %v", err)
		}
	})

	t.Run("Mermaid visualization with decision edges", func(t *testing.T) {
		b := graph.New[testWorkflowState]()
		dummyNode := graph.NodeFunc(func(ctx context.Context, s testWorkflowState) (graph.Command[testWorkflowState], error) {
			return nil, nil
		})
		b.AddNode("n_pred", dummyNode)
		b.AddNode("n_pass", dummyNode)
		b.AddNode("n_else", dummyNode)
		b.AddNode("n_route", dummyNode)
		b.AddNode("n_billing", dummyNode)
		b.AddNode("n_tech", dummyNode)
		b.AddNode("n_fallback", dummyNode)

		b.AddEdge(graph.START, "n_pred")
		predEdge := decision.NewConditionalEdge(model, "n_pass", "check", 0.5, stateFn).WithElse("n_else")
		decision.AddConditionalEdge(b, "n_pred", predEdge)

		b.AddEdge("n_pass", "n_route")
		b.AddEdge("n_else", graph.END)

		routeEdge := decision.NewRouteEdge(model, "route", nil, map[string]string{
			"billing": "n_billing",
			"tech":    "n_tech",
		}, stateFn).WithFallback("n_fallback")
		decision.AddRouteEdge(b, "n_route", routeEdge)

		b.AddEdge("n_billing", graph.END)
		b.AddEdge("n_tech", graph.END)
		b.AddEdge("n_fallback", graph.END)

		g, err := b.Build()
		if err != nil {
			t.Fatalf("failed to build graph: %v", err)
		}

		mermaid := g.ToMermaid()
		expectedSnippets := []string{
			"n_pred -. [pass] .-> n_pass",
			"n_pred -. [else] .-> n_else",
			"n_route -- billing --> n_billing",
			"n_route -- tech --> n_tech",
			"n_route -. [fallback] .-> n_fallback",
		}

		for _, snippet := range expectedSnippets {
			if !strings.Contains(mermaid, snippet) {
				t.Errorf("expected mermaid to contain %q, but got:\n%s", snippet, mermaid)
			}
		}
	})
}

type testCtxKey struct{}

type mockTestStreamWriter struct {
	events []any
}

func (m *mockTestStreamWriter) Write(ctx context.Context, data any) error {
	m.events = append(m.events, data)
	return nil
}

func TestDecisionEdges_ContextPropagationInGraph(t *testing.T) {
	t.Run("PredicateEdge receives custom context value and stream.Writer", func(t *testing.T) {
		var receivedCtx context.Context
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				receivedCtx = ctx
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"check": {
							Name:        "check",
							Type:        decision.QuestionPredicate,
							Probability: 0.95,
						},
					},
				}, nil
			},
		}
		model, err := decision.NewModel(provider, "test-model", nil)
		if err != nil {
			t.Fatalf("failed to create model: %v", err)
		}

		b := graph.New[testWorkflowState]()
		b.AddNode("start_node", graph.NodeFunc(func(ctx context.Context, s testWorkflowState) (graph.Command[testWorkflowState], error) {
			return nil, nil
		}))
		b.AddNode("allowed_node", graph.NodeFunc(func(ctx context.Context, s testWorkflowState) (graph.Command[testWorkflowState], error) {
			return graph.Update[testWorkflowState](func(state testWorkflowState) testWorkflowState {
				state.RouteTaken = "allowed"
				return state
			}), nil
		}))
		b.AddEdge(graph.START, "start_node")
		predEdge := decision.NewConditionalEdge(model, "allowed_node", "is allowed?", 0.5, func(s testWorkflowState) decision.Input {
			return decision.Text(s.Text)
		})
		decision.AddConditionalEdge(b, "start_node", predEdge)
		b.AddEdge("allowed_node", graph.END)

		g, err := b.Build()
		if err != nil {
			t.Fatalf("build error: %v", err)
		}

		writer := &mockTestStreamWriter{}
		ctx := context.WithValue(context.Background(), testCtxKey{}, "custom-value-pred")
		ctx = stream.WithWriter(ctx, writer)

		snap, err := g.Execute(ctx, nil, nil)
		if err != nil {
			t.Fatalf("execute error: %v", err)
		}
		if snap.State.RouteTaken != "allowed" {
			t.Fatalf("expected allowed, got %q", snap.State.RouteTaken)
		}

		if receivedCtx == nil {
			t.Fatal("expected provider to receive context, but receivedCtx is nil")
		}
		if val, ok := receivedCtx.Value(testCtxKey{}).(string); !ok || val != "custom-value-pred" {
			t.Errorf("expected context value 'custom-value-pred', got %v", receivedCtx.Value(testCtxKey{}))
		}
		if sw, ok := stream.WriterFromContext(receivedCtx); !ok || sw != writer {
			t.Errorf("expected stream.Writer in context, got %v", sw)
		}
	})

	t.Run("ChoiceRouteEdge receives custom context value and stream.Writer", func(t *testing.T) {
		var receivedCtx context.Context
		provider := &mockEdgeProvider{
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				receivedCtx = ctx
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"choice": {
							Name:       "choice",
							Type:       decision.QuestionChoice,
							Choice:     "billing",
							Confidence: 0.95,
						},
					},
				}, nil
			},
		}
		model, err := decision.NewModel(provider, "test-model", nil)
		if err != nil {
			t.Fatalf("failed to create model: %v", err)
		}

		b := graph.New[testWorkflowState]()
		b.AddNode("classify_node", graph.NodeFunc(func(ctx context.Context, s testWorkflowState) (graph.Command[testWorkflowState], error) {
			return nil, nil
		}))
		b.AddNode("billing_node", graph.NodeFunc(func(ctx context.Context, s testWorkflowState) (graph.Command[testWorkflowState], error) {
			return graph.Update[testWorkflowState](func(state testWorkflowState) testWorkflowState {
				state.RouteTaken = "billing"
				return state
			}), nil
		}))
		b.AddEdge(graph.START, "classify_node")
		routeEdge := decision.NewRouteEdge(model, "classify", []decision.Option{
			decision.Opt("billing", "Billing"),
		}, map[string]string{
			"billing": "billing_node",
		}, func(s testWorkflowState) decision.Input {
			return decision.Text(s.Text)
		})
		decision.AddRouteEdge(b, "classify_node", routeEdge)
		b.AddEdge("billing_node", graph.END)

		g, err := b.Build()
		if err != nil {
			t.Fatalf("build error: %v", err)
		}

		writer := &mockTestStreamWriter{}
		ctx := context.WithValue(context.Background(), testCtxKey{}, "custom-value-choice")
		ctx = stream.WithWriter(ctx, writer)

		snap, err := g.Execute(ctx, nil, nil)
		if err != nil {
			t.Fatalf("execute error: %v", err)
		}
		if snap.State.RouteTaken != "billing" {
			t.Fatalf("expected billing, got %q", snap.State.RouteTaken)
		}

		if receivedCtx == nil {
			t.Fatal("expected provider to receive context, but receivedCtx is nil")
		}
		if val, ok := receivedCtx.Value(testCtxKey{}).(string); !ok || val != "custom-value-choice" {
			t.Errorf("expected context value 'custom-value-choice', got %v", receivedCtx.Value(testCtxKey{}))
		}
		if sw, ok := stream.WriterFromContext(receivedCtx); !ok || sw != writer {
			t.Errorf("expected stream.Writer in context, got %v", sw)
		}
	})

	t.Run("PredicateEdge receives canceled context during edge evaluation", func(t *testing.T) {
		var receivedErr error
		ctx, cancel := context.WithCancel(context.Background())

		provider := &mockEdgeProvider{
			evaluateFunc: func(c context.Context, req *decision.Request) (*decision.Response, error) {
				receivedErr = c.Err()
				if receivedErr != nil {
					return nil, receivedErr
				}
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"check": {Name: "check", Type: decision.QuestionPredicate, Probability: 0.9},
					},
				}, nil
			},
		}
		model, err := decision.NewModel(provider, "test-model", nil)
		if err != nil {
			t.Fatalf("failed to create model: %v", err)
		}

		b := graph.New[testWorkflowState]()
		b.AddNode("canceling_node", graph.NodeFunc(func(c context.Context, s testWorkflowState) (graph.Command[testWorkflowState], error) {
			cancel() // cancel before transition / edge evaluation
			return nil, nil
		}))
		b.AddNode("allowed_node", graph.NodeFunc(func(c context.Context, s testWorkflowState) (graph.Command[testWorkflowState], error) {
			return nil, nil
		}))
		b.AddEdge(graph.START, "canceling_node")
		predEdge := decision.NewConditionalEdge(model, "allowed_node", "is allowed?", 0.5, func(s testWorkflowState) decision.Input {
			return decision.Text(s.Text)
		})
		decision.AddConditionalEdge(b, "canceling_node", predEdge)
		b.AddEdge("allowed_node", graph.END)

		g, err := b.Build()
		if err != nil {
			t.Fatalf("build error: %v", err)
		}

		_, err = g.Execute(ctx, nil, nil)
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled error, got: %v", err)
		}
		if !errors.Is(receivedErr, context.Canceled) {
			t.Fatalf("expected provider to observe context.Canceled, got: %v", receivedErr)
		}
	})
}
