package decision_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/masterkeysrd/loom/decision"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
)

type mockLLMProvider struct {
	name            string
	profiles        map[string]llm.ModelProfile
	lastRequest     *llm.Request
	responseHandler func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error)
}

func (m *mockLLMProvider) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock-llm"
}

func (m *mockLLMProvider) Stream(ctx context.Context, req *llm.Request) (llm.StreamResponse, error) {
	m.lastRequest = req
	text := "{}"
	var metrics *message.TokenMetrics
	if m.responseHandler != nil {
		t, met, err := m.responseHandler(ctx, req)
		if err != nil {
			return nil, err
		}
		text = t
		metrics = met
	}

	return func(yield func(message.AssistantChunk, error) bool) {
		chunk := message.AssistantChunk{
			Content: message.Content{
				&message.TextBlock{Text: text},
			},
			Metrics: metrics,
		}
		yield(chunk, nil)
	}, nil
}

func (m *mockLLMProvider) ListProfiles() []llm.ModelProfile {
	var list []llm.ModelProfile
	for _, p := range m.profiles {
		list = append(list, p)
	}
	return list
}

func (m *mockLLMProvider) GetProfile(id string) (llm.ModelProfile, bool) {
	if m.profiles == nil {
		return llm.ModelProfile{}, false
	}
	p, ok := m.profiles[id]
	return p, ok
}

func (m *mockLLMProvider) SearchProfiles(query string) []llm.ModelProfile {
	return nil
}

func (m *mockLLMProvider) OverrideProfile(id string, profile llm.ModelProfile) {
	if m.profiles == nil {
		m.profiles = make(map[string]llm.ModelProfile)
	}
	m.profiles[id] = profile
}

func TestLLM_NilModel(t *testing.T) {
	t.Run("NewLLMAdapter nil", func(t *testing.T) {
		adapter, err := decision.NewLLMAdapter(nil)
		if err == nil {
			t.Fatal("expected error for nil model, got nil")
		}
		if adapter != nil {
			t.Fatal("expected nil adapter")
		}
	})

	t.Run("NewFromLLM nil", func(t *testing.T) {
		m, err := decision.NewFromLLM(nil, nil)
		if err == nil {
			t.Fatal("expected error for nil model, got nil")
		}
		if m != nil {
			t.Fatal("expected nil model")
		}
	})
}

func TestLLM_Predicate(t *testing.T) {
	p := &mockLLMProvider{
		name: "test-provider",
		responseHandler: func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error) {
			jsonResp := `{
				"answers": {
					"is_valid": {
						"refused": false,
						"probability": 0.85
					}
				}
			}`
			return jsonResp, nil, nil
		},
	}

	model, err := llm.NewModel(p, "test-model", nil)
	if err != nil {
		t.Fatalf("failed to create llm model: %v", err)
	}

	adapter, err := decision.NewLLMAdapter(model)
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	if adapter.Name() != "test-provider" {
		t.Errorf("expected provider name 'test-provider', got %q", adapter.Name())
	}

	req := &decision.Request{
		Input: decision.Text("check user submission"),
		Questions: []decision.Question{
			decision.Predicate("is_valid", "Determine if input is valid").
				WithCriteria("Input has all required fields", "Input is missing required fields"),
		},
	}

	resp, err := adapter.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	ans, ok := resp.Answer("is_valid")
	if !ok {
		t.Fatal("missing answer for 'is_valid'")
	}

	if ans.Refused {
		t.Error("expected refused to be false")
	}
	if math.Abs(ans.Probability-0.85) > 1e-6 {
		t.Errorf("expected probability 0.85, got %f", ans.Probability)
	}
	if !ans.Passed(0.8) {
		t.Error("expected Passed(0.8) to be true")
	}
	if ans.Passed(0.9) {
		t.Error("expected Passed(0.9) to be false")
	}
	if ans.Confidence <= 0 {
		t.Errorf("expected positive confidence, got %f", ans.Confidence)
	}

	// Test probability clamping:
	t.Run("clamping", func(t *testing.T) {
		p.responseHandler = func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error) {
			return `{"answers": {"is_valid": {"refused": false, "probability": 1.5}}}`, nil, nil
		}
		resp, err := adapter.Evaluate(context.Background(), req)
		if err != nil {
			t.Fatalf("Evaluate error: %v", err)
		}
		ans, _ := resp.Answer("is_valid")
		if ans.Probability != 1.0 {
			t.Errorf("expected clamped probability 1.0, got %f", ans.Probability)
		}

		p.responseHandler = func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error) {
			return `{"answers": {"is_valid": {"refused": false, "probability": -0.5}}}`, nil, nil
		}
		resp, err = adapter.Evaluate(context.Background(), req)
		if err != nil {
			t.Fatalf("Evaluate error: %v", err)
		}
		ans, _ = resp.Answer("is_valid")
		if ans.Probability != 0.0 {
			t.Errorf("expected clamped probability 0.0, got %f", ans.Probability)
		}
	})
}

func TestLLM_Choice(t *testing.T) {
	t.Run("choice with explicit choice set", func(t *testing.T) {
		p := &mockLLMProvider{
			responseHandler: func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error) {
				return `{
					"answers": {
						"department": {
							"refused": false,
							"choice": "support",
							"probabilities": {
								"billing": 0.1,
								"support": 0.7,
								"sales": 0.2
							}
						}
					}
				}`, nil, nil
			},
		}

		model, _ := llm.NewModel(p, "test-model", nil)
		adapter, err := decision.NewLLMAdapter(model)
		if err != nil {
			t.Fatalf("adapter error: %v", err)
		}

		q := decision.Choice("department", "Select appropriate department",
			decision.Opt("billing", "Billing and invoices"),
			decision.Opt("support", "Technical assistance"),
			decision.Opt("sales", "New purchases and upgrades"),
		)

		req := &decision.Request{
			Input:     decision.Text("I need help debugging my server error"),
			Questions: []decision.Question{q},
		}

		resp, err := adapter.Evaluate(context.Background(), req)
		if err != nil {
			t.Fatalf("Evaluate error: %v", err)
		}

		ans, ok := resp.Answer("department")
		if !ok {
			t.Fatal("missing answer for 'department'")
		}

		if ans.Choice != "support" {
			t.Errorf("expected choice 'support', got %q", ans.Choice)
		}

		if len(ans.ChoiceProbabilities) != 3 {
			t.Fatalf("expected 3 choice probabilities, got %d", len(ans.ChoiceProbabilities))
		}

		// Verify ordering matches q.Choices
		if ans.ChoiceProbabilities[0].Value != "billing" || math.Abs(ans.ChoiceProbabilities[0].Probability-0.1) > 1e-6 {
			t.Errorf("unexpected billing prob: %v", ans.ChoiceProbabilities[0])
		}
		if ans.ChoiceProbabilities[1].Value != "support" || math.Abs(ans.ChoiceProbabilities[1].Probability-0.7) > 1e-6 {
			t.Errorf("unexpected support prob: %v", ans.ChoiceProbabilities[1])
		}
		if ans.ChoiceProbabilities[2].Value != "sales" || math.Abs(ans.ChoiceProbabilities[2].Probability-0.2) > 1e-6 {
			t.Errorf("unexpected sales prob: %v", ans.ChoiceProbabilities[2])
		}

		expectedConf := decision.ConfidenceFromProbabilities([]float64{0.1, 0.7, 0.2})
		if math.Abs(ans.Confidence-expectedConf) > 1e-6 {
			t.Errorf("expected confidence %f, got %f", expectedConf, ans.Confidence)
		}
		if ans.Prob("support") != 0.7 {
			t.Errorf("expected ans.Prob('support') = 0.7, got %f", ans.Prob("support"))
		}
	})

	t.Run("choice with omitted choice picks highest probability", func(t *testing.T) {
		p := &mockLLMProvider{
			responseHandler: func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error) {
				return `{
					"answers": {
						"priority": {
							"refused": false,
							"probabilities": {
								"low": 0.2,
								"medium": 0.5,
								"high": 0.3
							}
						}
					}
				}`, nil, nil
			},
		}

		model, _ := llm.NewModel(p, "test-model", nil)
		adapter, _ := decision.NewLLMAdapter(model)

		q := decision.Choice("priority", "Determine priority",
			decision.Opt("low", "Low"),
			decision.Opt("medium", "Medium"),
			decision.Opt("high", "High"),
		)

		resp, err := adapter.Evaluate(context.Background(), &decision.Request{
			Input:     decision.Text("Normal request"),
			Questions: []decision.Question{q},
		})
		if err != nil {
			t.Fatalf("Evaluate error: %v", err)
		}

		ans, _ := resp.Answer("priority")
		if ans.Choice != "medium" {
			t.Errorf("expected choice 'medium' picked by highest probability, got %q", ans.Choice)
		}
	})

	t.Run("choice probabilities normalized", func(t *testing.T) {
		p := &mockLLMProvider{
			responseHandler: func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error) {
				return `{
					"answers": {
						"env": {
							"refused": false,
							"choice": "prod",
							"probabilities": {
								"dev": 10,
								"stage": 20,
								"prod": 70
							}
						}
					}
				}`, nil, nil
			},
		}

		model, _ := llm.NewModel(p, "test-model", nil)
		adapter, _ := decision.NewLLMAdapter(model)

		q := decision.Choice("env", "Environment",
			decision.Opt("dev", "Dev"),
			decision.Opt("stage", "Stage"),
			decision.Opt("prod", "Prod"),
		)

		resp, err := adapter.Evaluate(context.Background(), &decision.Request{
			Input:     decision.Text("Deploy"),
			Questions: []decision.Question{q},
		})
		if err != nil {
			t.Fatalf("Evaluate error: %v", err)
		}

		ans, _ := resp.Answer("env")
		if math.Abs(ans.Prob("prod")-0.7) > 1e-6 {
			t.Errorf("expected normalized prob for prod 0.7, got %f", ans.Prob("prod"))
		}
		if math.Abs(ans.Prob("dev")-0.1) > 1e-6 {
			t.Errorf("expected normalized prob for dev 0.1, got %f", ans.Prob("dev"))
		}
	})
}

func TestLLM_Score(t *testing.T) {
	levels := []decision.Level{
		decision.Lvl("junior", "0-2 years"),
		decision.Lvl("mid", "3-5 years"),
		decision.Lvl("senior", "6+ years"),
	}
	q := decision.Score("experience", "Rate developer experience", levels...)

	t.Run("array probabilities", func(t *testing.T) {
		p := &mockLLMProvider{
			responseHandler: func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error) {
				return `{
					"answers": {
						"experience": {
							"refused": false,
							"probabilities": [0.1, 0.3, 0.6]
						}
					}
				}`, nil, nil
			},
		}

		model, _ := llm.NewModel(p, "test-model", nil)
		adapter, _ := decision.NewLLMAdapter(model)

		resp, err := adapter.Evaluate(context.Background(), &decision.Request{
			Input:     decision.Text("Candidate resume"),
			Questions: []decision.Question{q},
		})
		if err != nil {
			t.Fatalf("Evaluate error: %v", err)
		}

		ans, ok := resp.Answer("experience")
		if !ok {
			t.Fatal("missing answer for 'experience'")
		}

		if len(ans.ScoreProbabilities) != 3 {
			t.Fatalf("expected 3 score probabilities, got %d", len(ans.ScoreProbabilities))
		}

		// Probability-weighted average: 0*0.1 + 1*0.3 + 2*0.6 = 1.5
		expectedScore := 0.0*0.1 + 1.0*0.3 + 2.0*0.6
		if math.Abs(ans.Score-expectedScore) > 1e-6 {
			t.Errorf("expected score %f, got %f", expectedScore, ans.Score)
		}

		expectedConf := decision.ConfidenceFromProbabilities([]float64{0.1, 0.3, 0.6})
		if math.Abs(ans.Confidence-expectedConf) > 1e-6 {
			t.Errorf("expected confidence %f, got %f", expectedConf, ans.Confidence)
		}

		top, ok := ans.TopLevel()
		if !ok || top.Label != "senior" {
			t.Errorf("expected top level 'senior', got %v", top)
		}
	})

	t.Run("object probabilities by label", func(t *testing.T) {
		p := &mockLLMProvider{
			responseHandler: func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error) {
				return `{
					"answers": {
						"experience": {
							"refused": false,
							"probabilities": {
								"junior": 0.2,
								"mid": 0.8,
								"senior": 0.0
							}
						}
					}
				}`, nil, nil
			},
		}

		model, _ := llm.NewModel(p, "test-model", nil)
		adapter, _ := decision.NewLLMAdapter(model)

		resp, err := adapter.Evaluate(context.Background(), &decision.Request{
			Input:     decision.Text("Candidate resume"),
			Questions: []decision.Question{q},
		})
		if err != nil {
			t.Fatalf("Evaluate error: %v", err)
		}

		ans, _ := resp.Answer("experience")
		// 0*0.2 + 1*0.8 + 2*0.0 = 0.8
		expectedScore := 0.8
		if math.Abs(ans.Score-expectedScore) > 1e-6 {
			t.Errorf("expected score %f, got %f", expectedScore, ans.Score)
		}
	})

	t.Run("object probabilities by index string", func(t *testing.T) {
		p := &mockLLMProvider{
			responseHandler: func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error) {
				return `{
					"answers": {
						"experience": {
							"refused": false,
							"probabilities": {
								"0": 0.5,
								"1": 0.5,
								"2": 0.0
							}
						}
					}
				}`, nil, nil
			},
		}

		model, _ := llm.NewModel(p, "test-model", nil)
		adapter, _ := decision.NewLLMAdapter(model)

		resp, err := adapter.Evaluate(context.Background(), &decision.Request{
			Input:     decision.Text("Candidate resume"),
			Questions: []decision.Question{q},
		})
		if err != nil {
			t.Fatalf("Evaluate error: %v", err)
		}

		ans, _ := resp.Answer("experience")
		expectedScore := 0.5
		if math.Abs(ans.Score-expectedScore) > 1e-6 {
			t.Errorf("expected score %f, got %f", expectedScore, ans.Score)
		}
	})
}

func TestLLM_Refusal(t *testing.T) {
	p := &mockLLMProvider{
		responseHandler: func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error) {
			return `{
				"answers": {
					"pred": { "refused": true, "probability": 0.0 },
					"ch": { "refused": true, "choice": "a", "probabilities": {"a": 0.5, "b": 0.5} },
					"sc": { "refused": true, "probabilities": [0.5, 0.5] }
				}
			}`, nil, nil
		},
	}

	model, _ := llm.NewModel(p, "test-model", nil)
	adapter, _ := decision.NewLLMAdapter(model)

	req := &decision.Request{
		Input: decision.Text("Sensitive content"),
		Questions: []decision.Question{
			decision.Predicate("pred", "Is safe?"),
			decision.Choice("ch", "Type", decision.Opt("a", "A"), decision.Opt("b", "B")),
			decision.Score("sc", "Rating", decision.Lvl("low", "Low"), decision.Lvl("high", "High")),
		},
	}

	resp, err := adapter.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}

	predAns, _ := resp.Answer("pred")
	if !predAns.Refused {
		t.Error("expected pred to be refused")
	}
	if predAns.Passed(0.0) {
		t.Error("expected Passed() to be false when refused")
	}
	if predAns.Confidence != 0 {
		t.Errorf("expected confidence 0 on refusal, got %f", predAns.Confidence)
	}

	chAns, _ := resp.Answer("ch")
	if !chAns.Refused {
		t.Error("expected ch to be refused")
	}
	if chAns.Confidence != 0 {
		t.Errorf("expected confidence 0 on refusal, got %f", chAns.Confidence)
	}

	scAns, _ := resp.Answer("sc")
	if !scAns.Refused {
		t.Error("expected sc to be refused")
	}
	if scAns.Confidence != 0 {
		t.Errorf("expected confidence 0 on refusal, got %f", scAns.Confidence)
	}
}

func TestLLM_ImagePassing(t *testing.T) {
	p := &mockLLMProvider{
		responseHandler: func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error) {
			return `{"answers": {"has_cat": {"refused": false, "probability": 0.99}}}`, nil, nil
		},
	}

	model, _ := llm.NewModel(p, "vision-model", nil)
	adapter, _ := decision.NewLLMAdapter(model)

	img1 := message.ImageBlock{
		Data:     []byte{0x89, 0x50, 0x4E, 0x47},
		MIMEType: "image/png",
	}
	img2 := message.ImageBlock{
		URL: "https://example.com/photo.jpg",
	}

	input := decision.Text("Examine this photo").WithImages(img1, img2)
	q := decision.Predicate("has_cat", "Does the image contain a cat?")

	resp, err := adapter.Evaluate(context.Background(), &decision.Request{
		Input:     input,
		Questions: []decision.Question{q},
	})
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}

	ans, _ := resp.Answer("has_cat")
	if ans.Probability < 0.9 {
		t.Errorf("expected high probability, got %f", ans.Probability)
	}

	// Verify images were passed to the LLM Request
	if p.lastRequest == nil {
		t.Fatal("provider did not receive request")
	}
	if len(p.lastRequest.Messages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(p.lastRequest.Messages))
	}

	userMsg, ok := p.lastRequest.Messages[1].(*message.User)
	if !ok {
		t.Fatalf("expected second message to be User, got %T", p.lastRequest.Messages[1])
	}

	blocks := userMsg.GetContent()
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks (text + 2 images), got %d", len(blocks))
	}

	if blocks[0].Kind() != message.BlockKindText {
		t.Errorf("first block expected text, got %s", blocks[0].Kind())
	}
	if blocks[1].Kind() != message.BlockKindImage {
		t.Errorf("second block expected image, got %s", blocks[1].Kind())
	}
	imgBlock1 := blocks[1].(*message.ImageBlock)
	if imgBlock1.MIMEType != "image/png" {
		t.Errorf("expected MIMEType image/png, got %s", imgBlock1.MIMEType)
	}

	if blocks[2].Kind() != message.BlockKindImage {
		t.Errorf("third block expected image, got %s", blocks[2].Kind())
	}
	imgBlock2 := blocks[2].(*message.ImageBlock)
	if imgBlock2.URL != "https://example.com/photo.jpg" {
		t.Errorf("expected URL https://example.com/photo.jpg, got %s", imgBlock2.URL)
	}
}

func TestLLM_MetricsPreservation(t *testing.T) {
	expectedMetrics := &message.TokenMetrics{
		TotalTokens: 350,
		Tokens: message.TokenDetails{
			Input:  250,
			Output: 100,
		},
	}

	profile := llm.ModelProfile{
		ID:   "smart-model",
		Name: "smart-model",
		Pricing: llm.Pricing{
			Input:  10.0, // per 1M tokens
			Output: 30.0,
		},
	}

	p := &mockLLMProvider{
		profiles: map[string]llm.ModelProfile{
			"smart-model": profile,
		},
		responseHandler: func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error) {
			return `{"answers": {"test": {"refused": false, "probability": 0.5}}}`, expectedMetrics, nil
		},
	}

	model, _ := llm.NewModel(p, "smart-model", nil)

	t.Run("preservation on LLMAdapter.Evaluate", func(t *testing.T) {
		adapter, _ := decision.NewLLMAdapter(model)
		resp, err := adapter.Evaluate(context.Background(), &decision.Request{
			Model:     "smart-model",
			Input:     decision.Text("Context"),
			Questions: []decision.Question{decision.Predicate("test", "Test")},
		})
		if err != nil {
			t.Fatalf("Evaluate error: %v", err)
		}

		if resp.Metrics == nil {
			t.Fatal("expected Metrics to be preserved, got nil")
		}
		if resp.Metrics.TotalTokens != 350 {
			t.Errorf("expected 350 total tokens, got %d", resp.Metrics.TotalTokens)
		}
		if resp.Metrics.Tokens.Input != 250 {
			t.Errorf("expected 250 input tokens, got %d", resp.Metrics.Tokens.Input)
		}
		if resp.Metrics.Tokens.Output != 100 {
			t.Errorf("expected 100 output tokens, got %d", resp.Metrics.Tokens.Output)
		}
	})

	t.Run("cost calculation through decision.NewFromLLM", func(t *testing.T) {
		decModel, err := decision.NewFromLLM(model, nil)
		if err != nil {
			t.Fatalf("NewFromLLM error: %v", err)
		}

		resp, err := decModel.Evaluate(context.Background(), decision.Text("Context"), decision.Predicate("test", "Test"))
		if err != nil {
			t.Fatalf("Evaluate error: %v", err)
		}

		if resp.Metrics == nil {
			t.Fatal("expected Metrics to be preserved")
		}
		if resp.Metrics.TotalCost <= 0 {
			t.Errorf("expected positive total cost from profile pricing, got %v", resp.Metrics.TotalCost)
		}
	})
}

func TestLLM_NewFromLLM_ConvenienceMethods(t *testing.T) {
	p := &mockLLMProvider{
		responseHandler: func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error) {
			// Check question type from questions
			switch req.ResponseSchema.Properties["answers"].Properties["check"] != nil {
			case true:
				return `{"answers": {"check": {"refused": false, "probability": 0.92}}}`, nil, nil
			}
			if req.ResponseSchema.Properties["answers"].Properties["choice"] != nil {
				return `{"answers": {"choice": {"refused": false, "choice": "b", "probabilities": {"a": 0.2, "b": 0.8}}}}`, nil, nil
			}
			if req.ResponseSchema.Properties["answers"].Properties["score"] != nil {
				return `{"answers": {"score": {"refused": false, "probabilities": [0.1, 0.9]}}}`, nil, nil
			}
			return `{}`, nil, nil
		},
	}

	model, _ := llm.NewModel(p, "test-model", nil)
	decModel, err := decision.NewFromLLM(model, nil)
	if err != nil {
		t.Fatalf("NewFromLLM error: %v", err)
	}

	ctx := context.Background()

	t.Run("Check", func(t *testing.T) {
		ans, err := decModel.Check(ctx, decision.Text("Hello"), "Is valid greeting?")
		if err != nil {
			t.Fatalf("Check error: %v", err)
		}
		if math.Abs(ans.Probability-0.92) > 1e-6 {
			t.Errorf("expected 0.92, got %f", ans.Probability)
		}
	})

	t.Run("Choose", func(t *testing.T) {
		choices := []decision.Option{decision.Opt("a", "Option A"), decision.Opt("b", "Option B")}
		ans, err := decModel.Choose(ctx, decision.Text("Select"), "Choose one", choices)
		if err != nil {
			t.Fatalf("Choose error: %v", err)
		}
		if ans.Choice != "b" {
			t.Errorf("expected choice 'b', got %q", ans.Choice)
		}
	})

	t.Run("Rate", func(t *testing.T) {
		levels := []decision.Level{decision.Lvl("poor", "Poor"), decision.Lvl("good", "Good")}
		ans, err := decModel.Rate(ctx, decision.Text("Score this"), "Rate quality", levels)
		if err != nil {
			t.Fatalf("Rate error: %v", err)
		}
		if math.Abs(ans.Score-0.9) > 1e-6 {
			t.Errorf("expected score 0.9, got %f", ans.Score)
		}
	})
}

func TestLLM_StructuredStateAndMarkdownCleanup(t *testing.T) {
	p := &mockLLMProvider{
		responseHandler: func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error) {
			// Wrap output in markdown fences
			return "```json\n" + `{
				"answers": {
					"multi": {
						"refused": false,
						"probability": 0.77
					}
				}
			}` + "\n```", nil, nil
		},
	}

	model, _ := llm.NewModel(p, "markdown-model", nil)
	adapter, _ := decision.NewLLMAdapter(model)

	state := map[string]any{
		"user_id": 12345,
		"tags":    []string{"vip", "enterprise"},
	}

	resp, err := adapter.Evaluate(context.Background(), &decision.Request{
		Input:     decision.State(state),
		Questions: []decision.Question{decision.Predicate("multi", "Is VIP?")},
	})
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}

	ans, _ := resp.Answer("multi")
	if math.Abs(ans.Probability-0.77) > 1e-6 {
		t.Errorf("expected probability 0.77, got %f", ans.Probability)
	}
}

func TestLLM_ProviderError(t *testing.T) {
	expectedErr := errors.New("network failure")
	p := &mockLLMProvider{
		responseHandler: func(ctx context.Context, req *llm.Request) (string, *message.TokenMetrics, error) {
			return "", nil, expectedErr
		},
	}

	model, _ := llm.NewModel(p, "error-model", nil)
	adapter, _ := decision.NewLLMAdapter(model)

	_, err := adapter.Evaluate(context.Background(), &decision.Request{
		Input:     decision.Text("Test"),
		Questions: []decision.Question{decision.Predicate("test", "Test")},
	})
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}
