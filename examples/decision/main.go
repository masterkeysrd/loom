package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/masterkeysrd/loom/decision"
	"github.com/masterkeysrd/loom/graph"
	"github.com/masterkeysrd/loom/llm"
	loomollama "github.com/masterkeysrd/loom/llm/ollama"
	loomopenai "github.com/masterkeysrd/loom/llm/openai"
	"github.com/masterkeysrd/loom/telemetry"
)

// TicketState models a customer support ticket progressing through triage.
// It implements graph.State[TicketState].
type TicketState struct {
	ID          string `json:"id"`
	Message     string `json:"message"`
	Department  string `json:"department,omitempty"`
	Urgent      bool   `json:"urgent,omitempty"`
	Status      string `json:"status,omitempty"`
	ActionTaken string `json:"action_taken,omitempty"`
}

// Copy creates a deep copy to satisfy graph.State[TicketState].
func (s TicketState) Copy() TicketState {
	return s
}

func main() {
	var (
		providerFlag string
		modelFlag    string
		llmFlag      bool
	)
	flag.StringVar(&providerFlag, "provider", "ollama", "Provider to use: ollama or openai")
	flag.StringVar(&modelFlag, "model", "nimble", "Model to use (e.g., nimble, clef, clef-flash, gpt-6-luna, or any LLM with -llm)")
	flag.BoolVar(&llmFlag, "llm", false, "Use decision.NewFromLLM adapter with a standard chat LLM instead of native decision API")
	flag.Parse()

	// Enable content recording to see raw messages and decision payloads in Loom Studio
	ctx := telemetry.WithContentRecording(context.Background())

	// Initialize Telemetry
	shutdown, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName: "decision-example",
		Insecure:    true,
	})
	if err != nil {
		log.Printf("Warning: failed to initialize telemetry: %v", err)
	} else {
		defer shutdown(ctx)
	}

	var (
		llmProvider      llm.Provider
		decisionProvider decision.Provider
	)

	switch strings.ToLower(providerFlag) {
	case "ollama":
		p, err := loomollama.NewDefaultProvider()
		if err != nil {
			log.Fatalf("failed to create Ollama provider: %v", err)
		}
		llmProvider = p
		decisionProvider = p
	case "openai":
		p, err := loomopenai.NewDefaultProvider()
		if err != nil {
			log.Fatalf("failed to create OpenAI provider: %v", err)
		}
		llmProvider = p
		decisionProvider = p
	default:
		log.Fatalf("unsupported provider %q: must be 'ollama' or 'openai'", providerFlag)
	}

	var decider *decision.Model
	if llmFlag {
		llmModel, err := llm.NewModel(llmProvider, modelFlag, nil)
		if err != nil {
			log.Fatalf("failed to create LLM model: %v", err)
		}
		decider, err = decision.NewFromLLM(llmModel, nil)
		if err != nil {
			log.Fatalf("failed to create decision model from LLM: %v", err)
		}
	} else {
		decider, err = decision.NewModel(decisionProvider, modelFlag, nil)
		if err != nil {
			log.Fatalf("failed to create native decision model: %v", err)
		}
	}

	mode := "Native Decision API"
	if llmFlag {
		mode = "LLM Adapter (decision.NewFromLLM)"
	}

	fmt.Println("=================================================================")
	fmt.Printf("Loom Probabilistic Decision Engine Example\n")
	fmt.Printf("Provider: %s | Model: %s | Mode: %s\n", providerFlag, modelFlag, mode)
	fmt.Println("=================================================================")
	fmt.Println()

	// -----------------------------------------------------------------
	// Part 1: Batch Decision Evaluation (decider.Evaluate)
	// -----------------------------------------------------------------
	fmt.Println("=================================================================")
	fmt.Println("PART 1: Batch Decision Evaluation (decider.Evaluate)")
	fmt.Println("=================================================================")

	sampleTicket := TicketState{
		ID:      "TCK-1042",
		Message: "Our checkout has returned 500 errors since 9am and customers cannot complete purchases.",
	}
	fmt.Printf("Evaluating Ticket [%s]: %q\n\n", sampleTicket.ID, sampleTicket.Message)

	// Questions to evaluate simultaneously in a single model call
	qUrgent := decision.Predicate("is_urgent", "Does this ticket require immediate attention within the hour?")

	qDepartment := decision.Choice("department", "Which team should handle this ticket?",
		decision.Opt("billing", "Payments, invoices, and refunds"),
		decision.Opt("engineering", "Software bugs, outages, and 500 errors"),
		decision.Opt("account", "Login and access issues"),
	)

	qSeverity := decision.Score("severity", "How severe is this issue?",
		decision.Lvl("Routine", "Cosmetic or minor question"),
		decision.Lvl("Degraded", "Partial feature issue with workaround"),
		decision.Lvl("Critical", "Production outage blocking customers"),
	)

	resp, err := decider.Evaluate(ctx, decision.State(sampleTicket), qUrgent, qDepartment, qSeverity)
	if err != nil {
		log.Fatalf("Evaluation failed: %v", err)
	}

	// 1. Urgency Predicate
	if ans, ok := resp.Answer("is_urgent"); ok {
		fmt.Printf("1. Predicate Question: %q\n", ans.Name)
		fmt.Printf("   Probability: %.2f (Passed >= 0.80: %t)\n\n", ans.Probability, ans.Passed(0.80))
	}

	// 2. Department Choice
	if ans, ok := resp.Answer("department"); ok {
		fmt.Printf("2. Choice Question: %q\n", ans.Name)
		fmt.Printf("   Top Choice:  %q (Confidence: %.2f)\n", ans.Choice, ans.Confidence)
		fmt.Println("   Option Probabilities:")
		for _, cp := range ans.ChoiceProbabilities {
			fmt.Printf("     - %-12s: %.2f\n", cp.Value, cp.Probability)
		}
		fmt.Println()
	}

	// 3. Severity Score
	if ans, ok := resp.Answer("severity"); ok {
		topLvl, _ := ans.TopLevel()
		fmt.Printf("3. Score Question: %q\n", ans.Name)
		fmt.Printf("   Expected Score:   %.2f (Normalized: %.2f)\n", ans.Score, ans.NormalizedScore())
		fmt.Printf("   Top Level:        %q (Probability: %.2f)\n", topLvl.Label, topLvl.Probability)
		fmt.Println("   Level Distribution:")
		for _, lp := range ans.ScoreProbabilities {
			fmt.Printf("     - [%d] %-10s: %.2f\n", lp.Index, lp.Label, lp.Probability)
		}
		fmt.Println()
	}

	// -----------------------------------------------------------------
	// Part 2: Graph Routing with decision.AddRouteEdge and decision.AddConditionalEdge
	// -----------------------------------------------------------------
	fmt.Println("=================================================================")
	fmt.Println("PART 2: Graph Routing with AddRouteEdge and AddConditionalEdge")
	fmt.Println("=================================================================")

	builder := graph.New[TicketState]()

	// Define Workflow Nodes
	builder.AddNode("BillingNode", graph.NodeFunc[TicketState](func(ctx context.Context, state TicketState) (graph.Command[TicketState], error) {
		fmt.Printf("  -> [BillingNode] Processing ticket %s for billing/refund handling\n", state.ID)
		return graph.Update[TicketState](func(s TicketState) TicketState {
			s.Department = "billing"
			s.Status = "Assigned to Billing"
			return s
		}), nil
	}))

	builder.AddNode("EngineeringNode", graph.NodeFunc[TicketState](func(ctx context.Context, state TicketState) (graph.Command[TicketState], error) {
		fmt.Printf("  -> [EngineeringNode] Investigating technical defect for ticket %s\n", state.ID)
		return graph.Update[TicketState](func(s TicketState) TicketState {
			s.Department = "engineering"
			s.Status = "Assigned to Engineering"
			return s
		}), nil
	}))

	builder.AddNode("HumanTriage", graph.NodeFunc[TicketState](func(ctx context.Context, state TicketState) (graph.Command[TicketState], error) {
		fmt.Printf("  -> [HumanTriage] Confidence low or unmapped department; ticket %s routed to human queue\n", state.ID)
		return graph.Update[TicketState](func(s TicketState) TicketState {
			s.Department = "human_triage"
			s.Status = "Queued for Manual Review"
			return s
		}), nil
	}))

	builder.AddNode("IncidentPageNode", graph.NodeFunc[TicketState](func(ctx context.Context, state TicketState) (graph.Command[TicketState], error) {
		fmt.Printf("  -> [IncidentPageNode] EMERGENCY: Urgent outage detected! Paging on-call engineer for ticket %s\n", state.ID)
		return graph.Update[TicketState](func(s TicketState) TicketState {
			s.Urgent = true
			s.ActionTaken = "PAGED_ONCALL_ENGINEER"
			return s
		}), nil
	}))

	// Setup Choice Route Edge from START:
	// Routes to BillingNode or EngineeringNode.
	// Low confidence (< 0.60) or unmapped choices (like "account") fall back to HumanTriage.
	deptChoices := []decision.Option{
		decision.Opt("billing", "Payments, invoices, and refunds"),
		decision.Opt("engineering", "Software bugs, outages, and 500 errors"),
		decision.Opt("account", "Login and access issues"),
	}

	routeEdge := decision.NewRouteEdge[TicketState](
		decider,
		"Which team should handle this ticket?",
		deptChoices,
		map[string]string{
			"billing":     "BillingNode",
			"engineering": "EngineeringNode",
		},
		func(s TicketState) decision.Input {
			return decision.State(s)
		},
	).WithMinConfidence(0.60).WithFallback("HumanTriage")

	decision.AddRouteEdge(builder, graph.START, routeEdge)

	// Setup Conditional Edge after EngineeringNode:
	// If urgency predicate passes >= 0.80, route to IncidentPageNode; otherwise route to END.
	urgentEdge := decision.NewConditionalEdge[TicketState](
		decider,
		"IncidentPageNode",
		"Does this ticket require immediate attention within the hour?",
		0.80,
		func(s TicketState) decision.Input {
			return decision.State(s)
		},
	).WithElse(graph.END)

	decision.AddConditionalEdge(builder, "EngineeringNode", urgentEdge)

	// Leaf nodes exit to END
	builder.AddEdge("BillingNode", graph.END)
	builder.AddEdge("HumanTriage", graph.END)
	builder.AddEdge("IncidentPageNode", graph.END)

	// Build compiled graph
	g, err := builder.WithName("TicketRouter").Build()
	if err != nil {
		log.Fatalf("Failed to build graph: %v", err)
	}

	fmt.Println("--- Mermaid Workflow Diagram ---")
	fmt.Println(g.ToMermaid())
	fmt.Println("\n--- View Diagram in Browser ---")
	fmt.Println(g.MermaidURL())
	fmt.Println("--------------------------------")

	// Execute on sample tickets representing each decision path
	sampleTickets := []TicketState{
		{
			ID:      "TCK-1042",
			Message: "Our checkout has returned 500 errors since 9am and customers cannot complete purchases.",
		},
		{
			ID:      "TCK-2015",
			Message: "Footer copyright year typo: shows 2025 instead of 2026 on settings screen.",
		},
		{
			ID:      "TCK-3088",
			Message: "We were charged twice for invoice INV-9901. Please issue a refund for the duplicate payment.",
		},
		{
			ID:      "TCK-4004",
			Message: "User account locked out after losing 2FA authenticator app on new phone.",
		},
	}

	fmt.Println("\n--- Executing Graph on Sample Tickets ---")
	for _, t := range sampleTickets {
		fmt.Printf("\n[Ticket %s] Input: %q\n", t.ID, t.Message)

		inputCmd := graph.Update[TicketState](func(s TicketState) TicketState {
			return t
		})

		snap, err := g.Execute(ctx, inputCmd, nil)
		if err != nil {
			log.Fatalf("Execution failed for %s: %v", t.ID, err)
		}

		fmt.Printf("  Final State: Department=%q, Status=%q, Urgent=%t, Action=%q\n",
			snap.State.Department, snap.State.Status, snap.State.Urgent, snap.State.ActionTaken)
	}

	fmt.Println("\nAll decisions and workflows completed successfully.")
}
