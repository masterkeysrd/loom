package loomollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/masterkeysrd/loom/decision"
	"github.com/masterkeysrd/loom/message"
	"github.com/ollama/ollama/api"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestToSystemOneRequestConversion(t *testing.T) {
	req := &decision.Request{
		Model: "decision-model-v1",
		Input: decision.Input{
			State: map[string]any{"balance": 150.50, "status": "active"},
			Images: []message.ImageContent{
				{Data: []byte("img-data-1"), MIMEType: "image/png"},
				{Data: nil, MIMEType: "image/jpeg"}, // should be skipped
				{Data: []byte("img-data-2"), MIMEType: "image/webp"},
			},
		},
		Questions: []decision.Question{
			decision.Predicate("can_afford", "Is the balance >= 100?").
				WithCriteria("Balance meets threshold", "Balance too low"),
			decision.Choice("risk_level", "Assess transaction risk level",
				decision.Opt("low", "Minimal risk"),
				decision.Opt("medium", ""), // empty description -> null in JSON
				decision.Opt("high", "Severe risk"),
			),
			decision.Score("urgency", "Rate priority",
				decision.Lvl("1", "Low priority"),
				decision.Lvl("2", ""), // empty description -> label only
				decision.Lvl("3", "Critical priority"),
			),
		},
		Options: map[string]any{
			"runner": "safetensors",
		},
	}

	sysReq, err := toSystemOneRequest(req)
	if err != nil {
		t.Fatalf("unexpected error from toSystemOneRequest: %v", err)
	}

	if sysReq.Model != "decision-model-v1" {
		t.Errorf("expected model %q, got %q", "decision-model-v1", sysReq.Model)
	}
	if sysReq.Runner != "safetensors" {
		t.Errorf("expected runner %q, got %q", "safetensors", sysReq.Runner)
	}

	// Verify state
	var parsedState map[string]any
	if err := json.Unmarshal(sysReq.State, &parsedState); err != nil {
		t.Fatalf("failed to unmarshal state: %v", err)
	}
	if parsedState["balance"] != 150.50 || parsedState["status"] != "active" {
		t.Errorf("state mismatch: got %v", parsedState)
	}

	// Verify images
	if len(sysReq.Images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(sysReq.Images))
	}
	if string(sysReq.Images[0]) != "img-data-1" || string(sysReq.Images[1]) != "img-data-2" {
		t.Errorf("unexpected image bytes: %v", sysReq.Images)
	}

	// Verify questions count and ordering
	if sysReq.Questions == nil || sysReq.Questions.Len() != 3 {
		t.Fatalf("expected 3 questions, got %v", sysReq.Questions.Len())
	}

	// 1. Predicate Question
	predQ, ok := sysReq.Questions.Get("can_afford")
	if !ok {
		t.Fatalf("question 'can_afford' not found")
	}
	if predQ.Type != "noul" {
		t.Errorf("expected question type %q, got %q", "noul", predQ.Type)
	}
	var predInst string
	if err := json.Unmarshal(predQ.Instructions, &predInst); err != nil || predInst != "Is the balance >= 100?" {
		t.Errorf("predicate instructions mismatch: got %q, err %v", predInst, err)
	}
	var predCrit map[string]string
	if err := json.Unmarshal(predQ.Criteria, &predCrit); err != nil {
		t.Fatalf("failed to unmarshal predicate criteria: %v", err)
	}
	if predCrit["true"] != "Balance meets threshold" || predCrit["false"] != "Balance too low" {
		t.Errorf("predicate criteria mismatch: %v", predCrit)
	}

	// 2. Choice Question
	choiceQ, ok := sysReq.Questions.Get("risk_level")
	if !ok {
		t.Fatalf("question 'risk_level' not found")
	}
	if choiceQ.Type != "choice" {
		t.Errorf("expected question type %q, got %q", "choice", choiceQ.Type)
	}
	var choiceInst string
	if err := json.Unmarshal(choiceQ.Instructions, &choiceInst); err != nil || choiceInst != "Assess transaction risk level" {
		t.Errorf("choice instructions mismatch: got %q, err %v", choiceInst, err)
	}
	// Verify raw Criteria JSON preserves order and nulls
	expectedChoiceCriteriaJSON := `{"low":"Minimal risk","medium":null,"high":"Severe risk"}`
	if string(choiceQ.Criteria) != expectedChoiceCriteriaJSON {
		t.Errorf("choice criteria JSON mismatch:\ngot:  %s\nwant: %s", string(choiceQ.Criteria), expectedChoiceCriteriaJSON)
	}

	// 3. Score Question
	scoreQ, ok := sysReq.Questions.Get("urgency")
	if !ok {
		t.Fatalf("question 'urgency' not found")
	}
	if scoreQ.Type != "score" {
		t.Errorf("expected question type %q, got %q", "score", scoreQ.Type)
	}
	var scoreInst string
	if err := json.Unmarshal(scoreQ.Instructions, &scoreInst); err != nil || scoreInst != "Rate priority" {
		t.Errorf("score instructions mismatch: got %q, err %v", scoreInst, err)
	}
	var scoreCrit []string
	if err := json.Unmarshal(scoreQ.Criteria, &scoreCrit); err != nil {
		t.Fatalf("failed to unmarshal score criteria: %v", err)
	}
	expectedScoreCrit := []string{"1: Low priority", "2", "3: Critical priority"}
	if !reflect.DeepEqual(scoreCrit, expectedScoreCrit) {
		t.Errorf("score criteria mismatch: got %v, want %v", scoreCrit, expectedScoreCrit)
	}
}

func TestToSystemOneRequestNilStateAndEmptyCriteria(t *testing.T) {
	req := &decision.Request{
		Model: "model-x",
		Input: decision.Input{
			State: nil,
		},
		Questions: []decision.Question{
			decision.Predicate("is_valid", "instructions only"),
		},
	}

	sysReq, err := toSystemOneRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// State should be `""`
	if string(sysReq.State) != `""` {
		t.Errorf("expected state `\"\"`, got %q", string(sysReq.State))
	}

	q, ok := sysReq.Questions.Get("is_valid")
	if !ok {
		t.Fatalf("question not found")
	}
	if len(q.Criteria) != 0 {
		t.Errorf("expected empty criteria, got %s", string(q.Criteria))
	}
}

func TestToSystemOneRequestSinglePredicateCriteria(t *testing.T) {
	// Only TrueDescription provided
	req1 := &decision.Request{
		Model: "model-x",
		Questions: []decision.Question{
			decision.Question{
				Name:            "has_access",
				Type:            decision.QuestionPredicate,
				Instructions:    "Check access",
				TrueDescription: "User is an admin",
			},
		},
	}
	sysReq1, err := toSystemOneRequest(req1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	q1, _ := sysReq1.Questions.Get("has_access")
	var crit1 map[string]string
	if err := json.Unmarshal(q1.Criteria, &crit1); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if crit1["true"] != "User is an admin" || len(crit1) != 1 {
		t.Errorf("expected only 'true' key in criteria, got: %v", crit1)
	}

	// Only FalseDescription provided
	req2 := &decision.Request{
		Model: "model-x",
		Questions: []decision.Question{
			decision.Question{
				Name:             "has_access",
				Type:             decision.QuestionPredicate,
				Instructions:     "Check access",
				FalseDescription: "User is a guest",
			},
		},
	}
	sysReq2, err := toSystemOneRequest(req2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	q2, _ := sysReq2.Questions.Get("has_access")
	var crit2 map[string]string
	if err := json.Unmarshal(q2.Criteria, &crit2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if crit2["false"] != "User is a guest" || len(crit2) != 1 {
		t.Errorf("expected only 'false' key in criteria, got: %v", crit2)
	}
}

func TestToDecisionResponseConversion(t *testing.T) {
	req := &decision.Request{
		Model: "test-model",
		Questions: []decision.Question{
			decision.Predicate("can_afford", "check"),
			decision.Choice("risk_level", "check",
				decision.Opt("low", "Low"),
				decision.Opt("medium", "Med"),
				decision.Opt("high", "High"),
			),
			decision.Score("urgency", "check",
				decision.Lvl("1", "Low"),
				decision.Lvl("2", "Med"),
				decision.Lvl("3", "High"),
			),
		},
	}

	answersRaw := new(api.SystemOneAnswers)
	_ = answersRaw.UnmarshalJSON([]byte("{}"))
	answersRaw.Set("can_afford", json.RawMessage(`{"type":"noul","noul":0.85}`))
	answersRaw.Set("risk_level", json.RawMessage(`{"type":"choice","choice":"low","probabilities":{"low":0.7,"medium":0.2,"high":0.1},"confidence":0.78}`))
	answersRaw.Set("urgency", json.RawMessage(`{"type":"score","score":1.5,"legend":{"0":"1: Low","1":"2: Med","2":"3: High"},"probabilities":{"0":0.2,"1":0.5,"2":0.3},"confidence":0.65}`))

	cachedCount := 42
	sysResp := &api.SystemOneResponse{
		Model:   "test-model",
		Answers: answersRaw,
		Usage: api.SystemOneUsage{
			InputTokens:  100,
			OutputTokens: 25,
		},
		PromptEvalCachedCount: &cachedCount,
	}

	resp, err := toDecisionResponse(sysResp, req)
	if err != nil {
		t.Fatalf("unexpected error from toDecisionResponse: %v", err)
	}

	if resp.Model != "test-model" {
		t.Errorf("expected model %q, got %q", "test-model", resp.Model)
	}

	// 1. Verify Predicate Answer
	predAns, ok := resp.Answer("can_afford")
	if !ok {
		t.Fatalf("answer 'can_afford' not found")
	}
	if predAns.Type != decision.QuestionPredicate {
		t.Errorf("expected type %v, got %v", decision.QuestionPredicate, predAns.Type)
	}
	if predAns.Probability != 0.85 {
		t.Errorf("expected probability 0.85, got %v", predAns.Probability)
	}

	// 2. Verify Choice Answer
	choiceAns, ok := resp.Answer("risk_level")
	if !ok {
		t.Fatalf("answer 'risk_level' not found")
	}
	if choiceAns.Type != decision.QuestionChoice {
		t.Errorf("expected type %v, got %v", decision.QuestionChoice, choiceAns.Type)
	}
	if choiceAns.Choice != "low" {
		t.Errorf("expected choice 'low', got %q", choiceAns.Choice)
	}
	if choiceAns.Confidence != 0.78 {
		t.Errorf("expected confidence 0.78, got %v", choiceAns.Confidence)
	}
	if len(choiceAns.ChoiceProbabilities) != 3 {
		t.Fatalf("expected 3 choice probabilities, got %d", len(choiceAns.ChoiceProbabilities))
	}
	expectedChoiceOrder := []string{"low", "medium", "high"}
	for i, val := range expectedChoiceOrder {
		if choiceAns.ChoiceProbabilities[i].Value != val {
			t.Errorf("at index %d, expected choice %q, got %q", i, val, choiceAns.ChoiceProbabilities[i].Value)
		}
	}
	if choiceAns.Prob("low") != 0.7 || choiceAns.Prob("medium") != 0.2 || choiceAns.Prob("high") != 0.1 {
		t.Errorf("choice probabilities values mismatch: %v", choiceAns.ChoiceProbabilities)
	}

	// 3. Verify Score Answer
	scoreAns, ok := resp.Answer("urgency")
	if !ok {
		t.Fatalf("answer 'urgency' not found")
	}
	if scoreAns.Type != decision.QuestionScore {
		t.Errorf("expected type %v, got %v", decision.QuestionScore, scoreAns.Type)
	}
	if scoreAns.Score != 1.5 {
		t.Errorf("expected score 1.5, got %v", scoreAns.Score)
	}
	if scoreAns.Confidence != 0.65 {
		t.Errorf("expected confidence 0.65, got %v", scoreAns.Confidence)
	}
	if len(scoreAns.ScoreProbabilities) != 3 {
		t.Fatalf("expected 3 score probabilities, got %d", len(scoreAns.ScoreProbabilities))
	}
	for i := 0; i < 3; i++ {
		sp := scoreAns.ScoreProbabilities[i]
		if sp.Index != i {
			t.Errorf("at index %d, expected Index %d, got %d", i, i, sp.Index)
		}
		if sp.Label != req.Questions[2].Levels[i].Label {
			t.Errorf("at index %d, expected Label %q, got %q", i, req.Questions[2].Levels[i].Label, sp.Label)
		}
	}
	if scoreAns.ScoreProbabilities[0].Probability != 0.2 ||
		scoreAns.ScoreProbabilities[1].Probability != 0.5 ||
		scoreAns.ScoreProbabilities[2].Probability != 0.3 {
		t.Errorf("score probabilities mismatch: %v", scoreAns.ScoreProbabilities)
	}

	// Verify Metrics
	if resp.Metrics == nil {
		t.Fatalf("expected metrics to be populated")
	}
	if resp.Metrics.TotalTokens != 125 {
		t.Errorf("expected total tokens 125, got %d", resp.Metrics.TotalTokens)
	}
	if resp.Metrics.Tokens.Input != 100 || resp.Metrics.Tokens.Output != 25 {
		t.Errorf("token breakdown mismatch: %+v", resp.Metrics.Tokens)
	}
	if resp.Metrics.Tokens.CacheRead != 42 {
		t.Errorf("expected cache read 42, got %d", resp.Metrics.Tokens.CacheRead)
	}
}

func TestProviderEvaluateRoundTrip(t *testing.T) {
	// Create mock HTTP server simulating Ollama /v1/systemone endpoint
	receivedReq := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		receivedReq = true

		var sysReq api.SystemOneRequest
		if err := json.NewDecoder(r.Body).Decode(&sysReq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if sysReq.Model != "llama3-decision" {
			http.Error(w, "unexpected model", http.StatusBadRequest)
			return
		}

		// Prepare mock response
		respJSON := `{
			"model": "llama3-decision",
			"answers": {
				"pass_test": {
					"type": "noul",
					"noul": 0.92
				},
				"sentiment": {
					"type": "choice",
					"choice": "positive",
					"probabilities": {
						"positive": 0.88,
						"negative": 0.12
					},
					"confidence": 0.82
				},
				"quality": {
					"type": "score",
					"score": 2.8,
					"legend": {
						"0": "1: Poor",
						"1": "2: Fair",
						"2": "3: Excellent"
					},
					"probabilities": {
						"0": 0.05,
						"1": 0.15,
						"2": 0.80
					},
					"confidence": 0.85
				}
			},
			"usage": {
				"input_tokens": 50,
				"output_tokens": 10
			},
			"prompt_eval_cached_count": 20
		}`

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respJSON))
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("failed to parse test server URL: %v", err)
	}

	client := api.NewClient(u, server.Client())
	provider := (&Provider{}).NewProvider(client, u)

	// Set up tracing to check span attributes
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	ctx, span := tp.Tracer("test").Start(context.Background(), "evaluate-test")

	req := &decision.Request{
		Model: "llama3-decision",
		Input: decision.Text("The code runs quickly and passes all automated test suites."),
		Questions: []decision.Question{
			decision.Predicate("pass_test", "Does it pass tests?"),
			decision.Choice("sentiment", "Assess sentiment",
				decision.Opt("positive", "Positive sentiment"),
				decision.Opt("negative", "Negative sentiment"),
			),
			decision.Score("quality", "Assess overall quality",
				decision.Lvl("1", "Poor"),
				decision.Lvl("2", "Fair"),
				decision.Lvl("3", "Excellent"),
			),
		},
	}

	resp, err := provider.Evaluate(ctx, req)
	span.End()

	if err != nil {
		t.Fatalf("unexpected error from Evaluate: %v", err)
	}
	if !receivedReq {
		t.Fatal("expected mock server to receive request")
	}

	if resp.Model != "llama3-decision" {
		t.Errorf("expected model %q, got %q", "llama3-decision", resp.Model)
	}

	// Verify answers
	ans1, ok := resp.Answer("pass_test")
	if !ok || !ans1.Passed(0.9) || ans1.Probability != 0.92 {
		t.Errorf("pass_test answer mismatch: %+v", ans1)
	}

	ans2, ok := resp.Answer("sentiment")
	if !ok || ans2.Choice != "positive" || ans2.Prob("positive") != 0.88 {
		t.Errorf("sentiment answer mismatch: %+v", ans2)
	}

	ans3, ok := resp.Answer("quality")
	if !ok || ans3.Score != 2.8 || len(ans3.ScoreProbabilities) != 3 {
		t.Errorf("quality answer mismatch: %+v", ans3)
	}
	topLvl, ok := ans3.TopLevel()
	if !ok || topLvl.Label != "3" || topLvl.Probability != 0.80 {
		t.Errorf("quality top level mismatch: %+v", topLvl)
	}

	// Verify metrics
	if resp.Metrics.TotalTokens != 60 || resp.Metrics.Tokens.CacheRead != 20 {
		t.Errorf("metrics mismatch: %+v", resp.Metrics)
	}

	// Verify span decoration
	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatalf("expected at least 1 span recorded")
	}
	var foundOllamaType bool
	for _, attr := range spans[0].Attributes {
		if attr.Key == "ollama.api.type" && attr.Value.AsString() == "systemone" {
			foundOllamaType = true
		}
	}
	if !foundOllamaType {
		t.Errorf("expected 'ollama.api.type' attribute to be 'systemone', got attributes: %v", spans[0].Attributes)
	}
}

func TestProviderEvaluateErrors(t *testing.T) {
	t.Run("nil provider or client", func(t *testing.T) {
		var p *Provider
		_, err := p.Evaluate(context.Background(), &decision.Request{})
		if err == nil {
			t.Fatal("expected error on nil provider")
		}

		p2 := &Provider{}
		_, err = p2.Evaluate(context.Background(), &decision.Request{})
		if err == nil {
			t.Fatal("expected error on nil client")
		}
	})

	t.Run("nil request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer server.Close()
		u, _ := url.Parse(server.URL)
		p := (&Provider{}).NewProvider(api.NewClient(u, server.Client()), u)

		_, err := p.Evaluate(context.Background(), nil)
		if err == nil {
			t.Fatal("expected error on nil request")
		}
	})

	t.Run("unsupported question type", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer server.Close()
		u, _ := url.Parse(server.URL)
		p := (&Provider{}).NewProvider(api.NewClient(u, server.Client()), u)

		req := &decision.Request{
			Model: "test",
			Questions: []decision.Question{
				{Name: "bad", Type: "custom_type"},
			},
		}
		_, err := p.Evaluate(context.Background(), req)
		if err == nil {
			t.Fatal("expected error on unsupported question type")
		}
	})

	t.Run("server 500 error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "server crashed", http.StatusInternalServerError)
		}))
		defer server.Close()
		u, _ := url.Parse(server.URL)
		p := (&Provider{}).NewProvider(api.NewClient(u, server.Client()), u)

		req := &decision.Request{
			Model: "test",
			Questions: []decision.Question{
				decision.Predicate("test", "test"),
			},
		}
		_, err := p.Evaluate(context.Background(), req)
		if err == nil {
			t.Fatal("expected error on server 500")
		}
	})

	t.Run("server returns invalid answer json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			respJSON := `{
				"model": "test",
				"answers": {
					"q1": "not-a-valid-answer-object"
				},
				"usage": {"input_tokens": 1, "output_tokens": 1}
			}`
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(respJSON))
		}))
		defer server.Close()
		u, _ := url.Parse(server.URL)
		p := (&Provider{}).NewProvider(api.NewClient(u, server.Client()), u)

		req := &decision.Request{
			Model: "test",
			Questions: []decision.Question{
				decision.Predicate("q1", "test"),
			},
		}
		_, err := p.Evaluate(context.Background(), req)
		if err == nil {
			t.Fatal("expected error on invalid answer json")
		}
	})
}
