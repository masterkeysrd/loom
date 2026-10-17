package decision_test

import (
	"context"
	"errors"
	"testing"

	"github.com/masterkeysrd/loom/decision"
	"github.com/masterkeysrd/loom/graph"
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

		next, err := edge.NextNode(testWorkflowState{Text: "valid request"})
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

		next, err := edge.NextNode(testWorkflowState{Text: "invalid request"})
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

		next, err := edge.NextNode(testWorkflowState{Text: "ambiguous"})
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

		next, err := edge.NextNode(testWorkflowState{Text: "refused input"})
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

		next, err := edge.NextNode(testWorkflowState{Text: "refused input"})
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
			_, err := edge.NextNode(testWorkflowState{})
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
			_, err := edge.NextNode(testWorkflowState{})
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
			_, err := edge.NextNode(testWorkflowState{})
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

		next, err := edge.NextNode(testWorkflowState{Text: "I want a refund"})
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

			next, err := edge.NextNode(testWorkflowState{Text: "Not sure what I need"})
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

			_, err := edge.NextNode(testWorkflowState{Text: "Not sure what I need"})
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

			next, err := edge.NextNode(testWorkflowState{Text: "dangerous content"})
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

			_, err := edge.NextNode(testWorkflowState{Text: "dangerous content"})
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

			next, err := edge.NextNode(testWorkflowState{})
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

			_, err := edge.NextNode(testWorkflowState{})
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
			_, err := edge.NextNode(testWorkflowState{})
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
			_, err := edge.NextNode(testWorkflowState{})
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
			_, err := edge.NextNode(testWorkflowState{})
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
