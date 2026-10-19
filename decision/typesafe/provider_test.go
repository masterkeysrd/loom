package loomtypesafe_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/masterkeysrd/loom/decision"
	loomtypesafe "github.com/masterkeysrd/loom/decision/typesafe"
	"github.com/masterkeysrd/loom/message"
)

func TestNewProvider(t *testing.T) {
	t.Run("missing api key error", func(t *testing.T) {
		p, err := loomtypesafe.NewProvider()
		if err == nil {
			t.Fatal("expected error for missing api key, got nil")
		}
		if p != nil {
			t.Fatalf("expected nil provider, got %v", p)
		}
	})

	t.Run("invalid base URL error", func(t *testing.T) {
		p, err := loomtypesafe.NewProvider(
			loomtypesafe.WithAPIKey("test-key"),
			loomtypesafe.WithBaseURL(""),
		)
		if err == nil {
			t.Fatal("expected error for invalid base URL, got nil")
		}
		if p != nil {
			t.Fatalf("expected nil provider, got %v", p)
		}
	})

	t.Run("valid initialization and defaults", func(t *testing.T) {
		p, err := loomtypesafe.NewProvider(loomtypesafe.WithAPIKey("test-key"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Name() != "typesafe" {
			t.Errorf("expected provider name %q, got %q", "typesafe", p.Name())
		}

		// Verify GetProfile for all predefined models
		expectedModels := []string{
			loomtypesafe.ModelJevLatest,
			loomtypesafe.ModelJevPreview,
			loomtypesafe.ModelJev1_14_0,
			loomtypesafe.ModelJev1_13_0,
		}
		for _, mID := range expectedModels {
			profile, ok := p.GetProfile(mID)
			if !ok {
				t.Errorf("expected profile for model %q, got not found", mID)
			}
			if profile.ID != mID {
				t.Errorf("expected profile ID %q, got %q", mID, profile.ID)
			}
			if profile.Family != "jev" {
				t.Errorf("expected family 'jev', got %q", profile.Family)
			}
			if profile.Limits.Context != 64000 {
				t.Errorf("expected context limit 64000, got %d", profile.Limits.Context)
			}
			if profile.Pricing.Input != 0.042 {
				t.Errorf("expected pricing input 0.042, got %f", profile.Pricing.Input)
			}
			if !profile.Capabilities.Decision {
				t.Errorf("expected Decision capability to be true for model %q", mID)
			}
		}

		// Unknown model lookup
		_, ok := p.GetProfile("unknown-model")
		if ok {
			t.Error("expected ok=false for unknown model")
		}
	})

	t.Run("options are applied", func(t *testing.T) {
		customClient := &http.Client{}
		p, err := loomtypesafe.NewProvider(
			loomtypesafe.WithAPIKey("custom-key"),
			loomtypesafe.WithBaseURL("https://custom.api.typesafe.ai"),
			loomtypesafe.WithHTTPClient(customClient),
			loomtypesafe.WithCompression(false),
			loomtypesafe.WithMaxRetries(5),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p == nil {
			t.Fatal("expected non-nil provider")
		}
	})
}

func TestNewDefaultProvider(t *testing.T) {
	t.Run("missing env var error", func(t *testing.T) {
		t.Setenv("TYPESAFE_API_KEY", "")
		p, err := loomtypesafe.NewDefaultProvider()
		if err == nil {
			t.Fatal("expected error when TYPESAFE_API_KEY is not set")
		}
		if p != nil {
			t.Fatalf("expected nil provider, got %v", p)
		}
	})

	t.Run("success with env vars", func(t *testing.T) {
		t.Setenv("TYPESAFE_API_KEY", "env-secret-key")
		t.Setenv("TYPESAFE_BASE_URL", "https://env.api.typesafe.ai")

		p, err := loomtypesafe.NewDefaultProvider()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p == nil {
			t.Fatal("expected non-nil provider")
		}
		if p.Name() != "typesafe" {
			t.Errorf("expected Name 'typesafe', got %q", p.Name())
		}
	})
}

func TestEvaluateAllQuestionTypes(t *testing.T) {
	var receivedBody []byte
	var authHeader string
	var contentTypeHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		authHeader = r.Header.Get("Authorization")
		contentTypeHeader = r.Header.Get("Content-Type")

		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		resp := map[string]any{
			"model": loomtypesafe.ModelJevLatest,
			"answers": map[string]any{
				"is_support": map[string]any{
					"type":       "noul",
					"noul":       0.92,
					"confidence": 0.88,
				},
				"urgency": map[string]any{
					"type":       "choice",
					"choice":     "high",
					"confidence": 0.85,
					"probabilities": map[string]float64{
						"high": 0.85,
						"low":  0.15,
					},
				},
				"quality": map[string]any{
					"type":       "score",
					"score":      2.0,
					"confidence": 0.90,
					"probabilities": map[string]float64{
						"0": 0.05,
						"1": 0.15,
						"2": 0.80,
					},
				},
			},
			"usage": map[string]any{
				"input_tokens":  150,
				"output_tokens": 40,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p, err := loomtypesafe.NewProvider(
		loomtypesafe.WithAPIKey("test-api-key"),
		loomtypesafe.WithBaseURL(server.URL),
		loomtypesafe.WithCompression(false),
	)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	req := &decision.Request{
		Model: loomtypesafe.ModelJevLatest,
		Input: decision.Text("User: Help, my account is locked!"),
		Questions: []decision.Question{
			decision.Predicate("is_support", "Is this a support request?").
				WithCriteria("User needs help", "User is chatting"),
			decision.Choice("urgency", "What is the ticket urgency?",
				decision.Opt("high", "Urgent request"),
				decision.Opt("low", ""), // Empty description should serialize to null
			),
			decision.Score("quality", "Rate request clarity",
				decision.Lvl("Poor", "Confusing"),
				decision.Lvl("Fair", "Acceptable"),
				decision.Lvl("Good", "Clear and complete"),
			),
		},
	}

	resp, err := p.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	// Verify headers
	if authHeader != "Bearer test-api-key" {
		t.Errorf("expected Authorization 'Bearer test-api-key', got %q", authHeader)
	}
	if contentTypeHeader != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", contentTypeHeader)
	}

	// Verify request JSON structure
	var parsedReq struct {
		State     any `json:"state"`
		Model     string
		Questions map[string]struct {
			Type         string          `json:"type"`
			Instructions string          `json:"instructions"`
			Criteria     json.RawMessage `json:"criteria"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(receivedBody, &parsedReq); err != nil {
		t.Fatalf("failed to parse request JSON: %v", err)
	}

	if parsedReq.Model != loomtypesafe.ModelJevLatest {
		t.Errorf("expected model %q, got %q", loomtypesafe.ModelJevLatest, parsedReq.Model)
	}
	if parsedReq.State != "User: Help, my account is locked!" {
		t.Errorf("expected state text, got %v", parsedReq.State)
	}

	// Verify Predicate question structure
	predQ, ok := parsedReq.Questions["is_support"]
	if !ok {
		t.Fatal("missing question is_support")
	}
	if predQ.Type != "noul" {
		t.Errorf("expected type 'noul', got %q", predQ.Type)
	}
	var predCrit map[string]string
	if err := json.Unmarshal(predQ.Criteria, &predCrit); err != nil {
		t.Fatalf("failed to parse predicate criteria: %v", err)
	}
	if predCrit["true"] != "User needs help" || predCrit["false"] != "User is chatting" {
		t.Errorf("unexpected predicate criteria: %v", predCrit)
	}

	// Verify Choice question structure and null description preservation
	choiceQ, ok := parsedReq.Questions["urgency"]
	if !ok {
		t.Fatal("missing question urgency")
	}
	if choiceQ.Type != "choice" {
		t.Errorf("expected type 'choice', got %q", choiceQ.Type)
	}
	var choiceCrit map[string]*string
	if err := json.Unmarshal(choiceQ.Criteria, &choiceCrit); err != nil {
		t.Fatalf("failed to parse choice criteria: %v", err)
	}
	if choiceCrit["high"] == nil || *choiceCrit["high"] != "Urgent request" {
		t.Errorf("expected high to have description 'Urgent request', got %v", choiceCrit["high"])
	}
	if choiceCrit["low"] != nil {
		t.Errorf("expected low to have null description, got %v", *choiceCrit["low"])
	}

	// Verify Score question structure
	scoreQ, ok := parsedReq.Questions["quality"]
	if !ok {
		t.Fatal("missing question quality")
	}
	if scoreQ.Type != "score" {
		t.Errorf("expected type 'score', got %q", scoreQ.Type)
	}
	var scoreCrit []string
	if err := json.Unmarshal(scoreQ.Criteria, &scoreCrit); err != nil {
		t.Fatalf("failed to parse score criteria: %v", err)
	}
	expectedScoreCrit := []string{"Poor: Confusing", "Fair: Acceptable", "Good: Clear and complete"}
	if len(scoreCrit) != len(expectedScoreCrit) {
		t.Fatalf("expected %d score criteria, got %d", len(expectedScoreCrit), len(scoreCrit))
	}
	for i, c := range expectedScoreCrit {
		if scoreCrit[i] != c {
			t.Errorf("expected criteria[%d]=%q, got %q", i, c, scoreCrit[i])
		}
	}

	// Verify Response mappings
	if resp.Model != loomtypesafe.ModelJevLatest {
		t.Errorf("expected response model %q, got %q", loomtypesafe.ModelJevLatest, resp.Model)
	}

	// Verify is_support answer
	ansSupport, ok := resp.Answer("is_support")
	if !ok {
		t.Fatal("missing answer for is_support")
	}
	if ansSupport.Type != decision.QuestionPredicate {
		t.Errorf("expected answer type predicate, got %v", ansSupport.Type)
	}
	if ansSupport.Probability != 0.92 {
		t.Errorf("expected probability 0.92, got %f", ansSupport.Probability)
	}
	if ansSupport.Confidence != 0.88 {
		t.Errorf("expected confidence 0.88, got %f", ansSupport.Confidence)
	}

	// Verify urgency answer
	ansUrgency, ok := resp.Answer("urgency")
	if !ok {
		t.Fatal("missing answer for urgency")
	}
	if ansUrgency.Type != decision.QuestionChoice {
		t.Errorf("expected answer type choice, got %v", ansUrgency.Type)
	}
	if ansUrgency.Choice != "high" {
		t.Errorf("expected choice 'high', got %q", ansUrgency.Choice)
	}
	if ansUrgency.Probability != 0.85 {
		t.Errorf("expected choice probability 0.85, got %f", ansUrgency.Probability)
	}
	if len(ansUrgency.ChoiceProbabilities) != 2 {
		t.Fatalf("expected 2 choice probabilities, got %d", len(ansUrgency.ChoiceProbabilities))
	}
	if ansUrgency.ChoiceProbabilities[0].Value != "high" || ansUrgency.ChoiceProbabilities[0].Probability != 0.85 {
		t.Errorf("unexpected choice probability for high: %v", ansUrgency.ChoiceProbabilities[0])
	}
	if ansUrgency.ChoiceProbabilities[1].Value != "low" || ansUrgency.ChoiceProbabilities[1].Probability != 0.15 {
		t.Errorf("unexpected choice probability for low: %v", ansUrgency.ChoiceProbabilities[1])
	}

	// Verify quality answer
	ansQuality, ok := resp.Answer("quality")
	if !ok {
		t.Fatal("missing answer for quality")
	}
	if ansQuality.Type != decision.QuestionScore {
		t.Errorf("expected answer type score, got %v", ansQuality.Type)
	}
	if ansQuality.Score != 2.0 {
		t.Errorf("expected score 2.0, got %f", ansQuality.Score)
	}
	if len(ansQuality.ScoreProbabilities) != 3 {
		t.Fatalf("expected 3 score probabilities, got %d", len(ansQuality.ScoreProbabilities))
	}
	if ansQuality.ScoreProbabilities[2].Index != 2 || ansQuality.ScoreProbabilities[2].Label != "Good" || ansQuality.ScoreProbabilities[2].Probability != 0.80 {
		t.Errorf("unexpected score level 2: %v", ansQuality.ScoreProbabilities[2])
	}

	// Verify metrics
	if resp.Metrics == nil {
		t.Fatal("expected non-nil Metrics")
	}
	if resp.Metrics.TotalTokens != 190 {
		t.Errorf("expected TotalTokens 190, got %d", resp.Metrics.TotalTokens)
	}
	if resp.Metrics.Tokens.Input != 150 {
		t.Errorf("expected input tokens 150, got %d", resp.Metrics.Tokens.Input)
	}
	if resp.Metrics.Tokens.Output != 40 {
		t.Errorf("expected output tokens 40, got %d", resp.Metrics.Tokens.Output)
	}
}

func TestEvaluateLongContextHeader(t *testing.T) {
	var receivedHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("TypeSafe-Danger-Zone-Long-Context")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   loomtypesafe.ModelJevLatest,
			"answers": map[string]any{},
		})
	}))
	defer server.Close()

	p, err := loomtypesafe.NewProvider(
		loomtypesafe.WithAPIKey("test-key"),
		loomtypesafe.WithBaseURL(server.URL),
	)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	t.Run("with LongContext extension enabled", func(t *testing.T) {
		receivedHeader = ""
		req := &decision.Request{
			Model: loomtypesafe.ModelJevLatest,
			Input: decision.Text("long prompt"),
		}
		decision.WithExtensionOption(loomtypesafe.LongContext{Enabled: true})(req)

		_, err := p.Evaluate(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receivedHeader != "true" {
			t.Errorf("expected header 'true', got %q", receivedHeader)
		}
	})

	t.Run("with long_context call option", func(t *testing.T) {
		receivedHeader = ""
		req := &decision.Request{
			Model: loomtypesafe.ModelJevLatest,
			Input: decision.Text("long prompt"),
		}
		decision.WithCallOption("long_context", true)(req)

		_, err := p.Evaluate(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receivedHeader != "true" {
			t.Errorf("expected header 'true', got %q", receivedHeader)
		}
	})

	t.Run("without LongContext option", func(t *testing.T) {
		receivedHeader = ""
		req := &decision.Request{
			Model: loomtypesafe.ModelJevLatest,
			Input: decision.Text("standard prompt"),
		}

		_, err := p.Evaluate(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receivedHeader != "" {
			t.Errorf("expected empty header, got %q", receivedHeader)
		}
	})
}

func TestEvaluateGzipCompression(t *testing.T) {
	var isGzipEncoding bool
	var decompressedPayload []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encoding := r.Header.Get("Content-Encoding")
		isGzipEncoding = encoding == "gzip"

		var bodyReader io.Reader = r.Body
		if isGzipEncoding {
			gzReader, err := gzip.NewReader(r.Body)
			if err != nil {
				http.Error(w, fmt.Sprintf("gzip error: %v", err), http.StatusBadRequest)
				return
			}
			defer gzReader.Close()
			bodyReader = gzReader
		}

		var err error
		decompressedPayload, err = io.ReadAll(bodyReader)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   loomtypesafe.ModelJevLatest,
			"answers": map[string]any{},
		})
	}))
	defer server.Close()

	largeText := strings.Repeat("Long repetitive text for compression test. ", 50) // > 1 KiB

	t.Run("compresses when >= 1KiB and compress enabled", func(t *testing.T) {
		isGzipEncoding = false
		decompressedPayload = nil

		p, err := loomtypesafe.NewProvider(
			loomtypesafe.WithAPIKey("test-key"),
			loomtypesafe.WithBaseURL(server.URL),
			loomtypesafe.WithCompression(true),
		)
		if err != nil {
			t.Fatalf("failed to create provider: %v", err)
		}

		req := &decision.Request{
			Model: loomtypesafe.ModelJevLatest,
			Input: decision.Text(largeText),
		}

		_, err = p.Evaluate(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !isGzipEncoding {
			t.Error("expected request to have Content-Encoding: gzip")
		}

		var payload map[string]any
		if err := json.Unmarshal(decompressedPayload, &payload); err != nil {
			t.Fatalf("failed to parse decompressed JSON: %v", err)
		}
		if payload["state"] != largeText {
			t.Errorf("decompressed state does not match")
		}
	})

	t.Run("does not compress when compress disabled", func(t *testing.T) {
		isGzipEncoding = false
		decompressedPayload = nil

		p, err := loomtypesafe.NewProvider(
			loomtypesafe.WithAPIKey("test-key"),
			loomtypesafe.WithBaseURL(server.URL),
			loomtypesafe.WithCompression(false),
		)
		if err != nil {
			t.Fatalf("failed to create provider: %v", err)
		}

		req := &decision.Request{
			Model: loomtypesafe.ModelJevLatest,
			Input: decision.Text(largeText),
		}

		_, err = p.Evaluate(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if isGzipEncoding {
			t.Error("expected request NOT to have Content-Encoding: gzip when compression disabled")
		}
	})
}

func TestEvaluateRetriesOn429And529(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := attempts.Add(1)
		switch att {
		case 1:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests) // 429
			_, _ = w.Write([]byte(`{"error": "rate limit exceeded"}`))
		case 2:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(529) // 529 Overloaded
			_, _ = w.Write([]byte(`{"error": "engine overloaded"}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model":   loomtypesafe.ModelJevLatest,
				"answers": map[string]any{},
			})
		}
	}))
	defer server.Close()

	p, err := loomtypesafe.NewProvider(
		loomtypesafe.WithAPIKey("test-key"),
		loomtypesafe.WithBaseURL(server.URL),
		loomtypesafe.WithMaxRetries(3),
	)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	req := &decision.Request{
		Model: loomtypesafe.ModelJevLatest,
		Input: decision.Text("test query"),
	}

	resp, err := p.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("expected evaluate to succeed after retries, got err: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if attempts.Load() != 3 {
		t.Errorf("expected 3 total attempts, got %d", attempts.Load())
	}

	// Test retries exhaustion
	t.Run("exhausts retries and returns error", func(t *testing.T) {
		failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error": "quota permanently exceeded"}`))
		}))
		defer failServer.Close()

		failP, err := loomtypesafe.NewProvider(
			loomtypesafe.WithAPIKey("test-key"),
			loomtypesafe.WithBaseURL(failServer.URL),
			loomtypesafe.WithMaxRetries(2),
		)
		if err != nil {
			t.Fatalf("failed to create provider: %v", err)
		}

		_, err = failP.Evaluate(context.Background(), req)
		if err == nil {
			t.Fatal("expected error when retries exhausted, got nil")
		}
		if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "quota permanently exceeded") {
			t.Errorf("expected status 429 and error message in %q", err.Error())
		}
	})
}

func TestEvaluateRejectsImages(t *testing.T) {
	p, err := loomtypesafe.NewProvider(
		loomtypesafe.WithAPIKey("test-key"),
		loomtypesafe.WithBaseURL("https://api.typesafe.ai"),
	)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	req := &decision.Request{
		Model: loomtypesafe.ModelJevLatest,
		Input: decision.Text("analyze this").WithImages(message.ImageContent{
			Data: []byte("fake-image-bytes"),
		}),
	}

	_, err = p.Evaluate(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when images provided, got nil")
	}
	if !strings.Contains(err.Error(), "typesafe provider does not support image inputs") {
		t.Errorf("expected error containing 'typesafe provider does not support image inputs', got: %v", err)
	}
}

func TestListModels(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "invalid method", http.StatusMethodNotAllowed)
				return
			}
			if r.URL.Path != "/v1/models" {
				http.Error(w, "invalid path", http.StatusNotFound)
				return
			}
			if r.Header.Get("Authorization") != "Bearer list-key" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{
					{
						"name":         "jev-latest",
						"description":  "Latest Jev System One model",
						"release_date": "2026-09-01",
					},
					{
						"name":         "jev-preview",
						"description":  "Preview Jev System One model",
						"release_date": "2026-10-01",
					},
				},
			})
		}))
		defer server.Close()

		p, err := loomtypesafe.NewProvider(
			loomtypesafe.WithAPIKey("list-key"),
			loomtypesafe.WithBaseURL(server.URL),
		)
		if err != nil {
			t.Fatalf("failed to create provider: %v", err)
		}

		models, err := p.ListModels(context.Background())
		if err != nil {
			t.Fatalf("unexpected ListModels error: %v", err)
		}
		if len(models) != 2 {
			t.Fatalf("expected 2 models, got %d", len(models))
		}
		if models[0].Name != "jev-latest" || models[0].Description != "Latest Jev System One model" {
			t.Errorf("unexpected model 0: %+v", models[0])
		}
		if models[1].Name != "jev-preview" || models[1].ReleaseDate != "2026-10-01" {
			t.Errorf("unexpected model 1: %+v", models[1])
		}
	})

	t.Run("non-2xx error handling", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error": "forbidden access"}`))
		}))
		defer server.Close()

		p, err := loomtypesafe.NewProvider(
			loomtypesafe.WithAPIKey("list-key"),
			loomtypesafe.WithBaseURL(server.URL),
		)
		if err != nil {
			t.Fatalf("failed to create provider: %v", err)
		}

		_, err = p.ListModels(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "forbidden access") {
			t.Errorf("unexpected error format: %v", err)
		}
	})
}

func TestEvaluateEdgeCases(t *testing.T) {
	t.Run("nil provider error", func(t *testing.T) {
		var p *loomtypesafe.Provider
		_, err := p.Evaluate(context.Background(), &decision.Request{})
		if err == nil {
			t.Fatal("expected error for nil provider")
		}
	})

	t.Run("nil request error", func(t *testing.T) {
		p, _ := loomtypesafe.NewProvider(loomtypesafe.WithAPIKey("key"))
		_, err := p.Evaluate(context.Background(), nil)
		if err == nil {
			t.Fatal("expected error for nil request")
		}
	})

	t.Run("unsupported question type error", func(t *testing.T) {
		p, _ := loomtypesafe.NewProvider(loomtypesafe.WithAPIKey("key"))
		req := &decision.Request{
			Model: loomtypesafe.ModelJevLatest,
			Questions: []decision.Question{
				{
					Name: "bad_q",
					Type: "invalid_type",
				},
			},
		}
		_, err := p.Evaluate(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for unsupported question type")
		}
	})

	t.Run("predicate question with empty criteria omits criteria field", func(t *testing.T) {
		var body []byte
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model":   loomtypesafe.ModelJevLatest,
				"answers": map[string]any{},
			})
		}))
		defer server.Close()

		p, err := loomtypesafe.NewProvider(
			loomtypesafe.WithAPIKey("key"),
			loomtypesafe.WithBaseURL(server.URL),
			loomtypesafe.WithCompression(false),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		req := &decision.Request{
			Model: loomtypesafe.ModelJevLatest,
			Questions: []decision.Question{
				decision.Predicate("flag", "Is it flagged?"), // No criteria specified
			},
		}

		_, err = p.Evaluate(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if strings.Contains(string(body), `"criteria"`) {
			t.Errorf("expected criteria to be omitted when empty, got body: %s", string(body))
		}
	})

	t.Run("integration with decision.NewModel", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model": loomtypesafe.ModelJevLatest,
				"answers": map[string]any{
					"check": map[string]any{
						"type":       "noul",
						"noul":       0.99,
						"confidence": 0.95,
					},
				},
				"usage": map[string]any{
					"input_tokens":  50,
					"output_tokens": 10,
				},
			})
		}))
		defer server.Close()

		p, err := loomtypesafe.NewProvider(
			loomtypesafe.WithAPIKey("key"),
			loomtypesafe.WithBaseURL(server.URL),
			loomtypesafe.WithCompression(false),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		m, err := decision.NewModel(p, loomtypesafe.ModelJevLatest, nil)
		if err != nil {
			t.Fatalf("unexpected error creating decision.Model: %v", err)
		}

		ans, err := m.Check(context.Background(), decision.Text("Hello"), "Is this greeting?")
		if err != nil {
			t.Fatalf("unexpected Check error: %v", err)
		}
		if !ans.Passed(0.9) {
			t.Errorf("expected Passed(0.9) to be true, probability is %f", ans.Probability)
		}
	})
}
