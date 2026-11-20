package loomcodex

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/masterkeysrd/loom/auth"
	"github.com/masterkeysrd/loom/profile"
)

type catalogTransport func(*http.Request) (*http.Response, error)

func (f catalogTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProfileCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var requests atomic.Int32
		client := &http.Client{Transport: catalogTransport(func(r *http.Request) (*http.Response, error) {
			n := requests.Add(1)
			time.Sleep(time.Millisecond)
			body := `{"models":[{"slug":"new-model","visibility":"list"},{"slug":"gpt-5.4","visibility":"list","display_name":"Account GPT","context_window":12345},{"slug":"hidden","visibility":"hide"}]}`
			if n == 2 {
				return nil, fmt.Errorf("catalog unavailable")
			}
			if n >= 3 {
				body = `{"models":[]}`
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		p, err := NewProvider(auth.StaticTokenSource("test"), client, WithProfileCacheTTL(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for range 5 {
			wg.Go(func() {
				models, err := p.ListModels(t.Context())
				if err != nil || len(models) != 2 || models[0].ID != "new-model" {
					t.Errorf("unexpected discovery: %v, %v", models, err)
				}
			})
		}
		wg.Wait()
		known, ok := p.GetProfile("gpt-5.4")
		if !ok || known.Name != "Account GPT" || known.Limits.Context != 12345 || !known.Capabilities.ToolCall || known.Capabilities.Temperature {
			t.Fatalf("unexpected profile: %+v", known)
		}
		unknown, ok := p.GetProfile("new-model")
		if !ok || unknown.ValidateToolCallCapability("codex", unknown.ID, true) != nil {
			t.Fatal("discovery rejected tools on an unknown model")
		}
		if len(p.SearchProfiles("Account GPT")) != 1 || requests.Load() != 1 {
			t.Fatal("expected one cached discovery")
		}
		p.OverrideProfile("gpt-5.4", profile.Profile{ID: "gpt-5.4", Name: "Override"})
		models, err := p.ListModels(t.Context())
		if err != nil || models[1].Name != "Override" {
			t.Fatal("override missing from cached model list")
		}
		time.Sleep(time.Minute)
		if _, ok := p.GetProfile("new-model"); ok || len(p.ListProfiles()) != 1 || requests.Load() != 1 {
			t.Fatal("expired discovery remained visible or made a network request")
		}
		if _, err := p.ListModels(t.Context()); err == nil {
			t.Fatal("expected refresh failure")
		}
		for range 2 {
			models, err := p.ListModels(t.Context())
			if err != nil || len(models) != 0 {
				t.Fatalf("expected empty refreshed catalog: %v, %v", models, err)
			}
		}
		if requests.Load() != 3 {
			t.Fatalf("unexpected request count: %d", requests.Load())
		}
		uncached, err := NewProvider(auth.StaticTokenSource("test"), client, WithProfileCacheTTL(0))
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err := uncached.ListModels(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		if requests.Load() != 5 {
			t.Fatal("zero TTL reused a discovery result")
		}
	})
}
