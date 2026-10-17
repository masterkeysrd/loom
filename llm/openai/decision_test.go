package loomopenai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/masterkeysrd/loom/decision"
	"github.com/masterkeysrd/loom/message"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestInterfaceCompliance(t *testing.T) {
	p := &Provider{}
	var _ decision.Provider = p
	var _ decision.ProfileProvider = p
	if p.Name() != "openai" {
		t.Fatalf("expected provider name 'openai', got %q", p.Name())
	}
}

func TestToDecisionNewParams_StringInput(t *testing.T) {
	req := &decision.Request{
		Model: "gpt-6-luna",
		Input: decision.Text("analyze this text"),
		Questions: []decision.Question{
			decision.Predicate("is_clear", "is text clear"),
		},
	}
	params, err := toDecisionNewParams(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Model != "gpt-6-luna" {
		t.Errorf("expected model 'gpt-6-luna', got %q", params.Model)
	}
	if param.IsOmitted(params.Input.OfString) || params.Input.OfString.Value != "analyze this text" {
		t.Errorf("expected input string 'analyze this text', got %v", params.Input.OfString)
	}
	if len(params.Input.OfDecisionInputMessageArray) != 0 {
		t.Errorf("expected 0 messages in OfDecisionInputMessageArray, got %d", len(params.Input.OfDecisionInputMessageArray))
	}
}

func TestToDecisionNewParams_StructStateInput(t *testing.T) {
	type userState struct {
		Role   string `json:"role"`
		Action string `json:"action"`
	}
	req := &decision.Request{
		Model: "gpt-6-luna",
		Input: decision.State(userState{Role: "admin", Action: "delete"}),
		Questions: []decision.Question{
			decision.Predicate("can_perform", "can user perform action"),
		},
	}
	params, err := toDecisionNewParams(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedJSON := `{"role":"admin","action":"delete"}`
	if params.Input.OfString.Value != expectedJSON {
		t.Errorf("expected JSON string %q, got %q", expectedJSON, params.Input.OfString.Value)
	}
}

func TestToDecisionNewParams_MultimodalImageInput(t *testing.T) {
	rawJPEG := []byte("jpeg-binary-data")
	rawPNG := []byte("png-binary-data")
	req := &decision.Request{
		Model: "gpt-6-luna",
		Input: decision.Text("describe the image").WithImages(
			message.ImageContent{URL: "https://example.com/cat.jpg"},
			message.ImageContent{Data: rawJPEG, MIMEType: "image/jpeg"},
			message.ImageContent{Data: rawPNG}, // default to image/png
		),
		Questions: []decision.Question{
			decision.Predicate("has_cat", "is there a cat in the image"),
		},
	}

	params, err := toDecisionNewParams(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !param.IsOmitted(params.Input.OfString) {
		t.Errorf("expected OfString to be omitted for multimodal input, got %v", params.Input.OfString)
	}

	if len(params.Input.OfDecisionInputMessageArray) != 1 {
		t.Fatalf("expected 1 message in OfDecisionInputMessageArray, got %d", len(params.Input.OfDecisionInputMessageArray))
	}

	msg := params.Input.OfDecisionInputMessageArray[0]
	parts := msg.Content.OfParts
	if len(parts) != 4 {
		t.Fatalf("expected 4 parts (1 text + 3 images), got %d", len(parts))
	}

	// Part 0: text
	if parts[0].OfInputText == nil || parts[0].OfInputText.Text != "describe the image" {
		t.Errorf("expected part 0 text 'describe the image', got %+v", parts[0].OfInputText)
	}

	// Part 1: URL image
	if parts[1].OfInputImage == nil || parts[1].OfInputImage.ImageURL != "https://example.com/cat.jpg" {
		t.Errorf("expected part 1 image URL 'https://example.com/cat.jpg', got %+v", parts[1].OfInputImage)
	}

	// Part 2: base64 JPEG
	expectedJPEGURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(rawJPEG)
	if parts[2].OfInputImage == nil || parts[2].OfInputImage.ImageURL != expectedJPEGURL {
		t.Errorf("expected part 2 image URL %q, got %+v", expectedJPEGURL, parts[2].OfInputImage)
	}

	// Part 3: base64 PNG (defaulted)
	expectedPNGURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(rawPNG)
	if parts[3].OfInputImage == nil || parts[3].OfInputImage.ImageURL != expectedPNGURL {
		t.Errorf("expected part 3 image URL %q, got %+v", expectedPNGURL, parts[3].OfInputImage)
	}
}

func TestToDecisionNewParams_Questions(t *testing.T) {
	req := &decision.Request{
		Model: "gpt-6-luna",
		Input: decision.Text("input context"),
		Questions: []decision.Question{
			decision.Predicate("is_safe", "assess content safety").
				WithCriteria("safe and benign", "hateful or dangerous"),
			decision.Choice("intent", "classify intent",
				decision.Opt("buy", "customer wants to buy"),
				decision.Opt("support", ""),
			),
			decision.Score("quality", "rate response quality",
				decision.Lvl("poor", "very low quality"),
				decision.Lvl("good", "acceptable quality"),
				decision.Lvl("excellent", "outstanding quality"),
			),
		},
	}

	params, err := toDecisionNewParams(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(params.Questions) != 3 {
		t.Fatalf("expected 3 questions, got %d", len(params.Questions))
	}

	// Predicate question
	predQ := params.Questions[0].OfPredicate
	if predQ == nil {
		t.Fatalf("expected question 0 to be predicate")
	}
	if predQ.Name.Value != "is_safe" {
		t.Errorf("expected name 'is_safe', got %q", predQ.Name.Value)
	}
	if !strings.Contains(predQ.Instructions, "assess content safety") {
		t.Errorf("expected instructions to contain base prompt, got %q", predQ.Instructions)
	}
	if !strings.Contains(predQ.Instructions, "safe and benign") || !strings.Contains(predQ.Instructions, "hateful or dangerous") {
		t.Errorf("expected instructions to contain criteria, got %q", predQ.Instructions)
	}

	// Choice question
	choiceQ := params.Questions[1].OfChoice
	if choiceQ == nil {
		t.Fatalf("expected question 1 to be choice")
	}
	if choiceQ.Name.Value != "intent" {
		t.Errorf("expected name 'intent', got %q", choiceQ.Name.Value)
	}
	if choiceQ.Instructions != "classify intent" {
		t.Errorf("expected instructions 'classify intent', got %q", choiceQ.Instructions)
	}
	if len(choiceQ.Choices) != 2 {
		t.Fatalf("expected 2 choices, got %d", len(choiceQ.Choices))
	}
	if choiceQ.Choices[0].Value.OfString.Value != "buy" || choiceQ.Choices[0].Description.Value != "customer wants to buy" {
		t.Errorf("unexpected choice 0: %+v", choiceQ.Choices[0])
	}
	if choiceQ.Choices[1].Value.OfString.Value != "support" || !param.IsOmitted(choiceQ.Choices[1].Description) {
		t.Errorf("unexpected choice 1: %+v", choiceQ.Choices[1])
	}

	// Score question
	scoreQ := params.Questions[2].OfScore
	if scoreQ == nil {
		t.Fatalf("expected question 2 to be score")
	}
	if scoreQ.Name.Value != "quality" {
		t.Errorf("expected name 'quality', got %q", scoreQ.Name.Value)
	}
	if scoreQ.Instructions != "rate response quality" {
		t.Errorf("expected instructions 'rate response quality', got %q", scoreQ.Instructions)
	}
	if len(scoreQ.Levels) != 3 {
		t.Fatalf("expected 3 levels, got %d", len(scoreQ.Levels))
	}
	if scoreQ.Levels[0].Label != "poor" || scoreQ.Levels[0].Description.Value != "very low quality" {
		t.Errorf("unexpected level 0: %+v", scoreQ.Levels[0])
	}
	if scoreQ.Levels[2].Label != "excellent" || scoreQ.Levels[2].Description.Value != "outstanding quality" {
		t.Errorf("unexpected level 2: %+v", scoreQ.Levels[2])
	}
}

func TestToDecisionNewParams_UnsupportedQuestionType(t *testing.T) {
	req := &decision.Request{
		Model: "gpt-6-luna",
		Input: decision.Text("test"),
		Questions: []decision.Question{
			{Name: "bad", Type: decision.QuestionType("unknown")},
		},
	}
	_, err := toDecisionNewParams(req)
	if err == nil {
		t.Fatalf("expected error for unsupported question type, got nil")
	}
}

func TestProvider_Evaluate_RoundTrip(t *testing.T) {
	// Setup trace exporter
	spanRecorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	otel.SetTracerProvider(tp)
	tracer := tp.Tracer("test")

	mockResp := map[string]any{
		"model": "gpt-6-luna",
		"answers": []map[string]any{
			{
				"name":        "is_safe",
				"type":        "predicate",
				"probability": 0.95,
			},
			{
				"name":       "intent",
				"type":       "choice",
				"choice":     "buy",
				"confidence": 0.88,
				"probabilities": []map[string]any{
					{"value": "buy", "probability": 0.88},
					{"value": "support", "probability": 0.12},
				},
			},
			{
				"name":       "quality",
				"type":       "score",
				"score":      2.0,
				"confidence": 0.92,
				"probabilities": []map[string]any{
					{"value": 0, "label": "poor", "probability": 0.05},
					{"value": 1, "label": "good", "probability": 0.15},
					{"value": 2, "label": "excellent", "probability": 0.80},
				},
			},
			{
				"name": "refused_q",
				"type": "refusal",
			},
		},
		"usage": map[string]any{
			"input_tokens":          40,
			"input_tokens_details":  map[string]any{},
			"output_tokens":         12,
			"output_tokens_details": map[string]any{},
			"total_tokens":          52,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/decisions") {
			t.Errorf("expected path to end with /decisions, got %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test" {
			t.Errorf("expected Authorization: Bearer test, got %q", auth)
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read body: %v", err)
		}
		var parsedBody map[string]any
		if err := json.Unmarshal(bodyBytes, &parsedBody); err != nil {
			t.Errorf("failed to parse request json: %v", err)
		}
		if parsedBody["model"] != "gpt-6-luna" {
			t.Errorf("expected model 'gpt-6-luna', got %v", parsedBody["model"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mockResp)
	}))
	defer server.Close()

	client := openai.NewClient(
		option.WithBaseURL(server.URL),
		option.WithAPIKey("test"),
	)
	p := NewProvider(&client)

	ctx, span := tracer.Start(context.Background(), "evaluate_test")

	req := &decision.Request{
		Model: "gpt-6-luna",
		Input: decision.Text("Evaluate this request context"),
		Questions: []decision.Question{
			decision.Predicate("is_safe", "is it safe"),
			decision.Choice("intent", "intent classification", decision.Opt("buy", "buy"), decision.Opt("support", "support")),
			decision.Score("quality", "quality rating", decision.Lvl("poor", ""), decision.Lvl("good", ""), decision.Lvl("excellent", "")),
			decision.Predicate("refused_q", "some sensitive question"),
		},
	}

	resp, err := p.Evaluate(ctx, req)
	span.End()

	if err != nil {
		t.Fatalf("unexpected error from Evaluate: %v", err)
	}

	if resp.Model != "gpt-6-luna" {
		t.Errorf("expected model 'gpt-6-luna', got %q", resp.Model)
	}

	// 1. Predicate answer
	ansSafe, ok := resp.Answer("is_safe")
	if !ok {
		t.Fatalf("missing answer for 'is_safe'")
	}
	if ansSafe.Type != decision.QuestionPredicate {
		t.Errorf("expected type predicate, got %s", ansSafe.Type)
	}
	if ansSafe.Probability != 0.95 {
		t.Errorf("expected probability 0.95, got %f", ansSafe.Probability)
	}
	if !ansSafe.Passed(0.9) {
		t.Errorf("expected Passed(0.9) to be true")
	}

	// 2. Choice answer
	ansIntent, ok := resp.Answer("intent")
	if !ok {
		t.Fatalf("missing answer for 'intent'")
	}
	if ansIntent.Type != decision.QuestionChoice {
		t.Errorf("expected type choice, got %s", ansIntent.Type)
	}
	if ansIntent.Choice != "buy" {
		t.Errorf("expected choice 'buy', got %q", ansIntent.Choice)
	}
	if ansIntent.Confidence != 0.88 {
		t.Errorf("expected confidence 0.88, got %f", ansIntent.Confidence)
	}
	if len(ansIntent.ChoiceProbabilities) != 2 {
		t.Fatalf("expected 2 choice probabilities, got %d", len(ansIntent.ChoiceProbabilities))
	}
	if ansIntent.Prob("buy") != 0.88 || ansIntent.Prob("support") != 0.12 {
		t.Errorf("unexpected choice probabilities: %+v", ansIntent.ChoiceProbabilities)
	}

	// 3. Score answer
	ansQuality, ok := resp.Answer("quality")
	if !ok {
		t.Fatalf("missing answer for 'quality'")
	}
	if ansQuality.Type != decision.QuestionScore {
		t.Errorf("expected type score, got %s", ansQuality.Type)
	}
	if ansQuality.Score != 2.0 {
		t.Errorf("expected score 2.0, got %f", ansQuality.Score)
	}
	if ansQuality.Confidence != 0.92 {
		t.Errorf("expected confidence 0.92, got %f", ansQuality.Confidence)
	}
	if len(ansQuality.ScoreProbabilities) != 3 {
		t.Fatalf("expected 3 score probabilities, got %d", len(ansQuality.ScoreProbabilities))
	}
	topLvl, hasTop := ansQuality.TopLevel()
	if !hasTop || topLvl.Label != "excellent" || topLvl.Index != 2 {
		t.Errorf("expected top level excellent (index 2), got %+v", topLvl)
	}

	// 4. Refused answer
	ansRefused, ok := resp.Answer("refused_q")
	if !ok {
		t.Fatalf("missing answer for 'refused_q'")
	}
	if !ansRefused.Refused {
		t.Errorf("expected Refused to be true")
	}
	if ansRefused.Type != decision.QuestionPredicate {
		t.Errorf("expected type predicate from request question type matching, got %s", ansRefused.Type)
	}

	// Metrics
	if resp.Metrics == nil {
		t.Fatalf("expected non-nil metrics")
	}
	if resp.Metrics.TotalTokens != 52 {
		t.Errorf("expected total tokens 52, got %d", resp.Metrics.TotalTokens)
	}
	if resp.Metrics.Tokens.Input != 40 || resp.Metrics.Tokens.Output != 12 {
		t.Errorf("expected input: 40, output: 12, got %+v", resp.Metrics.Tokens)
	}

	// Verify span attribute
	spans := spanRecorder.Ended()
	foundAttr := false
	for _, s := range spans {
		for _, attr := range s.Attributes() {
			if string(attr.Key) == "openai.api.type" && attr.Value.AsString() == "decisions" {
				foundAttr = true
				break
			}
		}
	}
	if !foundAttr {
		t.Errorf("expected span attribute openai.api.type=decisions to be recorded")
	}
}

func TestProvider_Evaluate_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": {"message": "Internal error"}}`))
	}))
	defer server.Close()

	client := openai.NewClient(
		option.WithBaseURL(server.URL),
		option.WithAPIKey("test"),
	)
	p := NewProvider(&client)

	req := &decision.Request{
		Model: "gpt-6-luna",
		Input: decision.Text("test"),
		Questions: []decision.Question{
			decision.Predicate("test_q", "test instructions"),
		},
	}

	_, err := p.Evaluate(context.Background(), req)
	if err == nil {
		t.Fatalf("expected error from Evaluate, got nil")
	}
}

func TestProvider_Evaluate_NilProviderOrClient(t *testing.T) {
	var p *Provider
	_, err := p.Evaluate(context.Background(), &decision.Request{})
	if err == nil {
		t.Errorf("expected error for nil provider")
	}

	p2 := &Provider{}
	_, err = p2.Evaluate(context.Background(), &decision.Request{})
	if err == nil {
		t.Errorf("expected error for nil client")
	}

	client := openai.NewClient(option.WithAPIKey("test"))
	p3 := NewProvider(&client)
	_, err = p3.Evaluate(context.Background(), nil)
	if err == nil {
		t.Errorf("expected error for nil request")
	}
}
