package loomgenai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/genai"
)

func newAPIVersionTestProvider(t *testing.T, config *genai.ClientConfig, capturedPath *string) *Provider {
	t.Helper()
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			*capturedPath = req.URL.Path
			rec := httptest.NewRecorder()
			rec.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rec).Encode(map[string]any{"models": []map[string]any{}})
			return rec.Result(), nil
		}),
	}
	config.APIKey = "test-api-key"
	config.HTTPClient = httpClient
	p, err := NewProvider(context.Background(), config)
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}
	return p
}

func TestNewProviderPinsV1BetaAPIVersion(t *testing.T) {
	tests := []struct {
		name     string
		config   *genai.ClientConfig
		wantPath string
	}{
		{
			name:     "gemini backend defaults to v1beta",
			config:   &genai.ClientConfig{Backend: genai.BackendGeminiAPI},
			wantPath: "/v1beta/models",
		},
		{
			name:     "explicit v1 is respected",
			config:   &genai.ClientConfig{Backend: genai.BackendGeminiAPI, HTTPOptions: genai.HTTPOptions{APIVersion: "v1"}},
			wantPath: "/v1/models",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			p := newAPIVersionTestProvider(t, tt.config, &gotPath)
			if _, err := p.ListModels(context.Background()); err != nil {
				t.Fatalf("ListModels failed: %v", err)
			}
			if !strings.HasPrefix(gotPath, tt.wantPath) {
				t.Fatalf("request path = %q, want prefix %q", gotPath, tt.wantPath)
			}
		})
	}
}
