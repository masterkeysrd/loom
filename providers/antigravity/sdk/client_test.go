package sdk_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/masterkeysrd/loom/providers/antigravity/sdk"
	"golang.org/x/oauth2"
)

func TestSDKRawProtocolSeparation(t *testing.T) {
	var seenModel, seenPromptID string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			_ = json.NewEncoder(w).Encode(map[string]any{"cloudaicompanionProject": "proj-sdk"})

		case "/v1internal:fetchAvailableModels":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": map[string]any{
					"gemini-3.8-flash-tiered": map[string]any{
						"displayName":   "Gemini 3.8 Flash",
						"modelProvider": "MODEL_PROVIDER_GOOGLE",
						"quotaInfo": map[string]any{
							"remainingFraction": 0.9,
							"resetTime":         "2026-11-17T19:00:00Z",
						},
					},
				},
			})

		case "/v1internal:retrieveUserQuotaSummary":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"groups": []map[string]any{
					{
						"displayName": "Gemini Models",
						"buckets": []map[string]any{
							{"bucketId": "gemini-5h", "window": "5h", "remainingFraction": 0.9},
							{"bucketId": "gemini-weekly", "window": "weekly", "remainingFraction": 0.4},
						},
					},
				},
			})

		case "/v1internal:streamGenerateContent":
			var env sdk.RequestEnvelope
			if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
				t.Errorf("decode RequestEnvelope: %v", err)
			}
			seenModel = env.Model
			seenPromptID = env.UserPromptID
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}}\n\n")

		case "/v1internal:countTokens":
			_ = json.NewEncoder(w).Encode(sdk.CountTokensResponse{TotalTokens: 17})

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := sdk.NewClient(
		oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "sdk-tok"}),
		sdk.WithBaseURL(server.URL),
	)
	ctx := context.Background()

	// FetchAvailableModels returns only the raw backend model without -tiered expansion.
	models, err := client.FetchAvailableModels(ctx)
	if err != nil {
		t.Fatalf("FetchAvailableModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gemini-3.8-flash-tiered" {
		t.Fatalf("expected 1 raw model, got %+v", models)
	}

	// RetrieveUserQuotaSummary returns grouped wire buckets untouched.
	summary, err := client.RetrieveUserQuotaSummary(ctx)
	if err != nil || len(summary.Groups) != 1 || len(summary.Groups[0].Buckets) != 2 {
		t.Fatalf("RetrieveUserQuotaSummary = %+v, err=%v", summary, err)
	}

	// StreamGenerateContent sends the exact model string passed in without alias rewriting.
	stream, err := client.StreamGenerateContent(ctx, "gemini-3.8-flash-high", sdk.GenerateContentRequest{}, sdk.WithUserPromptID("prompt-1"))
	if err != nil {
		t.Fatalf("StreamGenerateContent: %v", err)
	}
	var text string
	for env, err := range stream {
		if err != nil {
			t.Fatalf("stream chunk error: %v", err)
		}
		if res := env.Result(); res != nil && len(res.Candidates) > 0 && res.Candidates[0].Content != nil {
			text += res.Candidates[0].Content.Parts[0].Text
		}
	}
	if seenModel != "gemini-3.8-flash-high" || seenPromptID != "prompt-1" || text != "ok" {
		t.Fatalf("unexpected stream result: model=%q promptID=%q text=%q", seenModel, seenPromptID, text)
	}

	// CountTokens
	ct, err := client.CountTokens(ctx, sdk.CountTokensRequest{
		Contents: []sdk.Content{{Role: "user", Parts: []sdk.Part{{Text: "hi"}}}},
	})
	if err != nil || ct.TotalTokens != 17 {
		t.Fatalf("CountTokens = %+v, err=%v", ct, err)
	}
}
