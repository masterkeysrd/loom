package decision_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/masterkeysrd/loom/decision"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/stream"
	"github.com/masterkeysrd/loom/telemetry"
)

type mockExtension struct {
	id string
}

func (m mockExtension) ExtensionID() string {
	return m.id
}

type mockStreamWriter struct {
	events []stream.Event
}

func (w *mockStreamWriter) Write(ctx context.Context, data any) error {
	if evt, ok := data.(stream.Event); ok {
		w.events = append(w.events, evt)
	}
	return nil
}

type mockDecisionProvider struct {
	name         string
	evaluateFunc func(ctx context.Context, req *decision.Request) (*decision.Response, error)
	profiles     map[string]profile.Profile
}

func (m *mockDecisionProvider) Name() string {
	return m.name
}

func (m *mockDecisionProvider) Evaluate(ctx context.Context, req *decision.Request) (*decision.Response, error) {
	if m.evaluateFunc != nil {
		return m.evaluateFunc(ctx, req)
	}
	return &decision.Response{
		Model:   req.Model,
		Answers: make(map[string]decision.Answer),
	}, nil
}

func (m *mockDecisionProvider) GetProfile(id string) (profile.Profile, bool) {
	if m.profiles == nil {
		return profile.Profile{}, false
	}
	p, ok := m.profiles[id]
	return p, ok
}

type simpleMockProvider struct {
	name string
}

func (s *simpleMockProvider) Name() string { return s.name }
func (s *simpleMockProvider) Evaluate(ctx context.Context, req *decision.Request) (*decision.Response, error) {
	return &decision.Response{Model: req.Model}, nil
}

func TestNewModel(t *testing.T) {
	t.Run("nil provider error", func(t *testing.T) {
		m, err := decision.NewModel(nil, "model-1", nil)
		if err == nil {
			t.Fatal("expected error for nil provider, got nil")
		}
		if m != nil {
			t.Fatal("expected nil model")
		}
	})

	t.Run("empty name error", func(t *testing.T) {
		p := &simpleMockProvider{name: "test"}
		m, err := decision.NewModel(p, "", nil)
		if err == nil {
			t.Fatal("expected error for empty name, got nil")
		}
		if m != nil {
			t.Fatal("expected nil model")
		}
	})

	t.Run("without profile provider", func(t *testing.T) {
		p := &simpleMockProvider{name: "test"}
		m, err := decision.NewModel(p, "test-model", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Name() != "test-model" {
			t.Errorf("expected model name 'test-model', got %q", m.Name())
		}
		if m.Profile() != nil {
			t.Errorf("expected nil profile, got %+v", m.Profile())
		}
		if m.Provider() != p {
			t.Errorf("expected provider %v, got %v", p, m.Provider())
		}
		if m.Config() == nil {
			t.Fatal("expected non-nil config")
		}
	})

	t.Run("with profile provider", func(t *testing.T) {
		p := &mockDecisionProvider{
			name: "profile-p",
			profiles: map[string]profile.Profile{
				"smart-model": {
					ID:   "smart-model",
					Name: "Smart Model",
					Pricing: profile.Pricing{
						Input:  2.5,
						Output: 10.0,
					},
				},
			},
		}
		m, err := decision.NewModel(p, "smart-model", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Profile() == nil {
			t.Fatal("expected profile to be found, got nil")
		}
		if m.Profile().Name != "Smart Model" {
			t.Errorf("expected profile name 'Smart Model', got %q", m.Profile().Name)
		}
	})
}

func TestModel_CloneAndOptions(t *testing.T) {
	p := &simpleMockProvider{name: "test"}
	m1, err := decision.NewModel(p, "test-model", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ext := mockExtension{id: "ext1"}
	m2 := m1.WithExtension(ext).WithOption("key1", "val1")

	// Ensure m1 was not modified
	if m1.Config().Extensions != nil && len(m1.Config().Extensions) > 0 {
		t.Error("m1 config was modified when cloning m2")
	}
	if m1.Config().Options != nil && len(m1.Config().Options) > 0 {
		t.Error("m1 options was modified when cloning m2")
	}

	// Verify m2 has options
	if m2.Config().Extensions["ext1"] != ext {
		t.Errorf("expected extension ext1 in m2")
	}
	if m2.Config().Options["key1"] != "val1" {
		t.Errorf("expected key1=val1 in m2 options")
	}

	// WithConfig
	newCfg := decision.ModelConfig{
		Options: map[string]any{"override": 123},
	}
	m3 := m2.WithConfig(newCfg)
	if m3.Config().Options["override"] != 123 {
		t.Errorf("expected override=123 in m3")
	}
	if _, ok := m3.Config().Options["key1"]; ok {
		t.Error("m3 should not have key1 after WithConfig")
	}
}

func TestModel_Evaluate(t *testing.T) {
	t.Run("empty questions error", func(t *testing.T) {
		p := &simpleMockProvider{name: "test"}
		m, _ := decision.NewModel(p, "test-model", nil)
		_, err := m.Evaluate(context.Background(), decision.Text("hello"))
		if err == nil {
			t.Fatal("expected error with empty questions, got nil")
		}
	})

	t.Run("provider error propagation", func(t *testing.T) {
		expectedErr := errors.New("backend failed")
		p := &mockDecisionProvider{
			name: "err-p",
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return nil, expectedErr
			},
		}
		m, _ := decision.NewModel(p, "err-model", nil)
		_, err := m.Evaluate(context.Background(), decision.Text("input"), decision.Predicate("q", "instructions"))
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("successful evaluation and stream event", func(t *testing.T) {
		var receivedReq *decision.Request
		p := &mockDecisionProvider{
			name: "eval-p",
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				receivedReq = req
				return &decision.Response{
					Model: req.Model,
					Answers: map[string]decision.Answer{
						"is_urgent": {
							Name:        "is_urgent",
							Type:        decision.QuestionPredicate,
							Probability: 0.95,
							Confidence:  0.9,
						},
					},
					Metrics: &message.TokenMetrics{
						TotalTokens: 150,
						Tokens: message.TokenDetails{
							Input:  100,
							Output: 50,
						},
					},
				}, nil
			},
			profiles: map[string]profile.Profile{
				"eval-model": {
					ID:   "eval-model",
					Name: "Evaluator Model",
					Pricing: profile.Pricing{
						Input:  1.0, // $1 per million => 1000 nano per token
						Output: 2.0, // $2 per million => 2000 nano per token
					},
				},
			},
		}

		m, _ := decision.NewModel(p, "eval-model", nil)
		writer := &mockStreamWriter{}
		ctx := stream.WithWriter(context.Background(), writer)

		ext := mockExtension{id: "test-ext"}
		resp, err := m.EvaluateWithOptions(ctx, decision.Text("customer email"),
			[]decision.Question{decision.Predicate("is_urgent", "Check urgency")},
			decision.WithExtensionOption(ext),
			decision.WithCallOption("custom_opt", "test_val"),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if receivedReq.Extensions["test-ext"] != ext {
			t.Error("expected call option extension to be passed to request")
		}
		if receivedReq.Options["custom_opt"] != "test_val" {
			t.Error("expected custom_opt to be passed in request options")
		}

		ans, ok := resp.Answer("is_urgent")
		if !ok {
			t.Fatal("expected is_urgent answer")
		}
		if !ans.Passed(0.9) {
			t.Errorf("expected is_urgent to pass threshold 0.9")
		}

		// Check cost calculation
		if resp.Metrics == nil || resp.Metrics.TotalCost == 0 {
			t.Errorf("expected cost to be calculated via profile pricing")
		}
		expectedCost := message.Cost(100*1000 + 50*2000) // 100,000 + 100,000 = 200,000 nano
		if resp.Metrics.TotalCost != expectedCost {
			t.Errorf("expected total cost %v, got %v", expectedCost, resp.Metrics.TotalCost)
		}

		// Check stream event emission
		if len(writer.events) != 1 {
			t.Fatalf("expected 1 stream event, got %d", len(writer.events))
		}
		if writer.events[0].Name != "on_decision" {
			t.Errorf("expected stream event name 'on_decision', got %q", writer.events[0].Name)
		}
		if writer.events[0].Data != resp {
			t.Errorf("expected event data to match response")
		}
	})
}

func TestConvenienceMethods(t *testing.T) {
	p := &mockDecisionProvider{
		name: "test-p",
		evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
			if len(req.Questions) != 1 {
				return nil, errors.New("expected exactly 1 question")
			}
			q := req.Questions[0]
			resp := &decision.Response{
				Model:   req.Model,
				Answers: make(map[string]decision.Answer),
			}
			switch q.Name {
			case "check":
				resp.Answers["check"] = decision.Answer{
					Name:        "check",
					Type:        decision.QuestionPredicate,
					Probability: 0.88,
					Confidence:  0.8,
				}
			case "choice":
				resp.Answers["choice"] = decision.Answer{
					Name:   "choice",
					Type:   decision.QuestionChoice,
					Choice: "optA",
					ChoiceProbabilities: []decision.OptionProbability{
						{Value: "optA", Probability: 0.75},
						{Value: "optB", Probability: 0.25},
					},
				}
			case "score":
				resp.Answers["score"] = decision.Answer{
					Name:  "score",
					Type:  decision.QuestionScore,
					Score: 2.5,
					ScoreProbabilities: []decision.LevelProbability{
						{Index: 0, Label: "low", Probability: 0.1},
						{Index: 1, Label: "medium", Probability: 0.3},
						{Index: 2, Label: "high", Probability: 0.6},
					},
				}
			}
			return resp, nil
		},
	}

	m, err := decision.NewModel(p, "quick-model", nil)
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	t.Run("Check", func(t *testing.T) {
		ans, err := m.Check(context.Background(), decision.Text("Is it valid?"), "Return true if valid")
		if err != nil {
			t.Fatalf("unexpected Check error: %v", err)
		}
		if ans.Name != "check" || !ans.Passed(0.8) {
			t.Errorf("unexpected check answer: %+v", ans)
		}
	})

	t.Run("Choose", func(t *testing.T) {
		choices := []decision.Option{
			decision.Opt("optA", "Option A"),
			decision.Opt("optB", "Option B"),
		}
		ans, err := m.Choose(context.Background(), decision.Text("Select one"), "Pick best option", choices)
		if err != nil {
			t.Fatalf("unexpected Choose error: %v", err)
		}
		if ans.Choice != "optA" || ans.Prob("optA") != 0.75 {
			t.Errorf("unexpected choice answer: %+v", ans)
		}
	})

	t.Run("Rate", func(t *testing.T) {
		levels := []decision.Level{
			decision.Lvl("low", "Low intensity"),
			decision.Lvl("medium", "Medium intensity"),
			decision.Lvl("high", "High intensity"),
		}
		ans, err := m.Rate(context.Background(), decision.Text("Rate intensity"), "Provide a rating", levels)
		if err != nil {
			t.Fatalf("unexpected Rate error: %v", err)
		}
		if ans.Score != 2.5 {
			t.Errorf("expected score 2.5, got %v", ans.Score)
		}
	})

	t.Run("Missing Answer Errors", func(t *testing.T) {
		emptyP := &mockDecisionProvider{
			name: "empty-p",
			evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
				return &decision.Response{Answers: make(map[string]decision.Answer)}, nil
			},
		}
		emptyM, _ := decision.NewModel(emptyP, "empty-model", nil)

		if _, err := emptyM.Check(context.Background(), decision.Text("x"), "test"); err == nil {
			t.Error("expected error on missing check answer")
		}
		if _, err := emptyM.Choose(context.Background(), decision.Text("x"), "test", []decision.Option{decision.Opt("a", "b")}); err == nil {
			t.Error("expected error on missing choice answer")
		}
		if _, err := emptyM.Rate(context.Background(), decision.Text("x"), "test", []decision.Level{decision.Lvl("a", "b")}); err == nil {
			t.Error("expected error on missing score answer")
		}
	})
}

func TestMiddlewareExecutionOrder(t *testing.T) {
	p := &simpleMockProvider{name: "test"}
	m, _ := decision.NewModel(p, "test-model", nil)

	var executionLog []string

	mw1 := func(next decision.Evaluator) decision.Evaluator {
		return func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
			executionLog = append(executionLog, "mw1_before")
			resp, err := next(ctx, req)
			executionLog = append(executionLog, "mw1_after")
			return resp, err
		}
	}

	mw2 := func(next decision.Evaluator) decision.Evaluator {
		return func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
			executionLog = append(executionLog, "mw2_before")
			resp, err := next(ctx, req)
			executionLog = append(executionLog, "mw2_after")
			return resp, err
		}
	}

	m = m.WithMiddleware(mw1, mw2)
	_, err := m.Evaluate(context.Background(), decision.Text("test"), decision.Predicate("q", "instructions"))
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	expectedOrder := []string{
		"mw1_before",
		"mw2_before",
		"mw2_after",
		"mw1_after",
	}

	if !reflect.DeepEqual(executionLog, expectedOrder) {
		t.Errorf("expected execution order %v, got %v", expectedOrder, executionLog)
	}
}

func TestAnswerMethods(t *testing.T) {
	t.Run("Passed", func(t *testing.T) {
		a1 := decision.Answer{Probability: 0.85, Refused: false}
		if !a1.Passed(0.8) {
			t.Error("expected Passed(0.8) to be true for prob 0.85")
		}
		if a1.Passed(0.9) {
			t.Error("expected Passed(0.9) to be false for prob 0.85")
		}
		if !a1.Passed(0.85) {
			t.Error("expected Passed(0.85) to be true for prob 0.85 (boundary)")
		}

		aRefused := decision.Answer{Probability: 0.99, Refused: true}
		if aRefused.Passed(0.5) {
			t.Error("expected Passed to be false when Refused is true")
		}
	})

	t.Run("Prob", func(t *testing.T) {
		ans := decision.Answer{
			ChoiceProbabilities: []decision.OptionProbability{
				{Value: "red", Probability: 0.6},
				{Value: "blue", Probability: 0.3},
				{Value: "green", Probability: 0.1},
			},
		}
		if ans.Prob("red") != 0.6 {
			t.Errorf("expected 0.6 for red, got %v", ans.Prob("red"))
		}
		if ans.Prob("blue") != 0.3 {
			t.Errorf("expected 0.3 for blue, got %v", ans.Prob("blue"))
		}
		if ans.Prob("yellow") != 0.0 {
			t.Errorf("expected 0.0 for yellow, got %v", ans.Prob("yellow"))
		}
	})

	t.Run("NormalizedScore", func(t *testing.T) {
		emptyAns := decision.Answer{Score: 2.0}
		if emptyAns.NormalizedScore() != 0.0 {
			t.Errorf("expected 0.0 for empty score probabilities")
		}

		singleLevel := decision.Answer{
			Score: 1.0,
			ScoreProbabilities: []decision.LevelProbability{
				{Index: 0, Label: "only", Probability: 1.0},
			},
		}
		if singleLevel.NormalizedScore() != 0.0 {
			t.Errorf("expected 0.0 for 1 score level")
		}

		// 3 levels (indices 0, 1, 2 => max index is 2)
		threeLevels := decision.Answer{
			Score: 1.5,
			ScoreProbabilities: []decision.LevelProbability{
				{Index: 0, Label: "low", Probability: 0.1},
				{Index: 1, Label: "mid", Probability: 0.4},
				{Index: 2, Label: "high", Probability: 0.5},
			},
		}
		if threeLevels.NormalizedScore() != 0.75 { // 1.5 / 2 = 0.75
			t.Errorf("expected 0.75, got %v", threeLevels.NormalizedScore())
		}
	})

	t.Run("TopLevel", func(t *testing.T) {
		emptyAns := decision.Answer{}
		if _, ok := emptyAns.TopLevel(); ok {
			t.Error("expected TopLevel to return false for empty score probabilities")
		}

		ans := decision.Answer{
			ScoreProbabilities: []decision.LevelProbability{
				{Index: 0, Label: "low", Probability: 0.2},
				{Index: 1, Label: "mid", Probability: 0.55},
				{Index: 2, Label: "high", Probability: 0.25},
			},
		}
		top, ok := ans.TopLevel()
		if !ok {
			t.Fatal("expected TopLevel to return true")
		}
		if top.Label != "mid" || top.Probability != 0.55 {
			t.Errorf("unexpected top level: %+v", top)
		}
	})

	t.Run("Response_Answer", func(t *testing.T) {
		var nilResp *decision.Response
		if _, ok := nilResp.Answer("test"); ok {
			t.Error("expected false on nil Response")
		}

		emptyResp := &decision.Response{}
		if _, ok := emptyResp.Answer("test"); ok {
			t.Error("expected false on nil Answers map")
		}

		resp := &decision.Response{
			Answers: map[string]decision.Answer{
				"found": {Name: "found"},
			},
		}
		ans, ok := resp.Answer("found")
		if !ok || ans.Name != "found" {
			t.Errorf("unexpected answer: %+v, ok=%v", ans, ok)
		}
		if _, ok := resp.Answer("missing"); ok {
			t.Error("expected false for missing answer")
		}
	})
}

func TestConfidenceFromProbabilities(t *testing.T) {
	t.Run("empty or single item", func(t *testing.T) {
		if c := decision.ConfidenceFromProbabilities(nil); c != 0.0 {
			t.Errorf("expected 0.0 for nil, got %v", c)
		}
		if c := decision.ConfidenceFromProbabilities([]float64{}); c != 0.0 {
			t.Errorf("expected 0.0 for empty, got %v", c)
		}
		if c := decision.ConfidenceFromProbabilities([]float64{1.0}); c != 1.0 {
			t.Errorf("expected 1.0 for single probability, got %v", c)
		}
	})

	t.Run("uniform distribution yields zero confidence", func(t *testing.T) {
		c2 := decision.ConfidenceFromProbabilities([]float64{0.5, 0.5})
		if math.Abs(c2-0.0) > 1e-6 {
			t.Errorf("expected 0.0 for uniform binary, got %v", c2)
		}

		c4 := decision.ConfidenceFromProbabilities([]float64{0.25, 0.25, 0.25, 0.25})
		if math.Abs(c4-0.0) > 1e-6 {
			t.Errorf("expected 0.0 for uniform 4-way, got %v", c4)
		}
	})

	t.Run("deterministic distribution yields 1.0 confidence", func(t *testing.T) {
		c := decision.ConfidenceFromProbabilities([]float64{1.0, 0.0, 0.0})
		if math.Abs(c-1.0) > 1e-6 {
			t.Errorf("expected 1.0 for deterministic, got %v", c)
		}
	})

	t.Run("intermediate probabilities", func(t *testing.T) {
		c := decision.ConfidenceFromProbabilities([]float64{0.9, 0.1})
		if c <= 0.0 || c >= 1.0 {
			t.Errorf("expected confidence between 0 and 1, got %v", c)
		}
	})

	t.Run("all zeros or invalid", func(t *testing.T) {
		c := decision.ConfidenceFromProbabilities([]float64{0.0, 0.0})
		if c != 0.0 {
			t.Errorf("expected 0.0 for all zeros, got %v", c)
		}
	})
}

func TestConstructors(t *testing.T) {
	t.Run("Input constructors", func(t *testing.T) {
		inText := decision.Text("sample prompt")
		if inText.State != "sample prompt" {
			t.Errorf("expected state 'sample prompt', got %v", inText.State)
		}

		inState := decision.State(map[string]int{"count": 42})
		if m, ok := inState.State.(map[string]int); !ok || m["count"] != 42 {
			t.Errorf("expected state with count=42, got %v", inState.State)
		}

		img := message.ImageBlock{URL: "https://example.com/test.png"}
		inMultimodal := inText.WithImages(img)
		if len(inMultimodal.Images) != 1 || inMultimodal.Images[0].URL != "https://example.com/test.png" {
			t.Errorf("expected 1 image, got %+v", inMultimodal.Images)
		}
	})

	t.Run("Question constructors", func(t *testing.T) {
		p := decision.Predicate("is_valid", "instructions").WithCriteria("true criteria", "false criteria")
		if p.Name != "is_valid" || p.Type != decision.QuestionPredicate {
			t.Errorf("unexpected predicate: %+v", p)
		}
		if p.TrueDescription != "true criteria" || p.FalseDescription != "false criteria" {
			t.Errorf("unexpected criteria on predicate: %+v", p)
		}

		c := decision.Choice("category", "choose one",
			decision.Opt("v1", "desc 1"),
			decision.Opt("v2", "desc 2"),
		)
		if c.Name != "category" || c.Type != decision.QuestionChoice || len(c.Choices) != 2 {
			t.Errorf("unexpected choice question: %+v", c)
		}

		s := decision.Score("quality", "score quality",
			decision.Lvl("poor", "very low"),
			decision.Lvl("good", "acceptable"),
			decision.Lvl("great", "high"),
		)
		if s.Name != "quality" || s.Type != decision.QuestionScore || len(s.Levels) != 3 {
			t.Errorf("unexpected score question: %+v", s)
		}
	})
}

func TestModel_WithInitialConfig(t *testing.T) {
	p := &simpleMockProvider{name: "test"}
	ext := mockExtension{id: "init-ext"}
	cfg := &decision.ModelConfig{
		Extensions: map[string]decision.Extension{"init-ext": ext},
		Options:    map[string]any{"opt1": "val1"},
	}
	m, err := decision.NewModel(p, "test-model", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.Config().Extensions["init-ext"] != ext {
		t.Errorf("expected extension init-ext in model config")
	}
	if m.Config().Options["opt1"] != "val1" {
		t.Errorf("expected option opt1 in model config")
	}

	// Verify mutating original cfg doesn't mutate model config
	cfg.Options["opt1"] = "mutated"
	if m.Config().Options["opt1"] != "val1" {
		t.Errorf("model config was mutated externally")
	}
}

func TestTelemetryMiddleware(t *testing.T) {
	providers := []string{"openai", "anthropic", "google-genai", "unknown-provider"}
	for _, pName := range providers {
		t.Run(pName, func(t *testing.T) {
			p := &mockDecisionProvider{
				name: pName,
				evaluateFunc: func(ctx context.Context, req *decision.Request) (*decision.Response, error) {
					return &decision.Response{
						Model: "res-model",
						Answers: map[string]decision.Answer{
							"q1": {Name: "q1", Probability: 1.0},
						},
						Metrics: &message.TokenMetrics{
							TotalTokens: 20,
							Tokens: message.TokenDetails{
								Input:      10,
								Output:     10,
								Reasoning:  2,
								CacheRead:  3,
								CacheWrite: 4,
							},
						},
					}, nil
				},
			}

			m, _ := decision.NewModel(p, "test-m", nil)
			ctx := telemetry.WithContentRecording(context.Background())
			ctx = context.WithValue(ctx, telemetry.KeyLoomThreadID, "thread-123")

			_, err := m.Evaluate(ctx, decision.Text("input text"), decision.Predicate("q1", "do check"))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
