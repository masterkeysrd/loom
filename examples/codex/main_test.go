package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	loomcodex "github.com/masterkeysrd/loom/providers/codex"
	"golang.org/x/oauth2"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPromptWithDefaultModel(t *testing.T) {
	config := t.TempDir()
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "AppData"} {
		t.Setenv(key, config)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	session := savedSession{HostID: "host", Credentials: &loomcodex.Credentials{
		ClientID: "client", Subject: "user", Scopes: []string{"chatgpt.tokens.use.direct"},
		Token: oauth2.Token{AccessToken: "test", Expiry: time.Now().Add(time.Hour)},
	}}
	if err := writeSession(filepath.Join(dir, "loom", "codex-example", "session.json"), session); err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultClient
	defer func() { http.DefaultClient = previous }()
	var calls int
	http.DefaultClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		contentType, body := "application/json", `{"models":[{"slug":"visible-model","visibility":"list"}]}`
		switch r.URL.Path {
		case "/v1/models":
		case "/v1/responses":
			data, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			for _, want := range []string{"visible-model", "shell_command", "List files"} {
				if !strings.Contains(string(data), want) {
					t.Errorf("request missing %q", want)
				}
			}
			contentType = "text/event-stream"
			body = "data: " + `{"type":"response.output_text.delta","delta":"Done"}` + "\n\ndata: " +
				`{"type":"response.completed","response":{"id":"resp_1"}}` + "\n\n"
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if err := run(t.Context(), false, false, "", "List files"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected catalog and inference requests, got %d requests", calls)
	}
}
