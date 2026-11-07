package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestStaticTokenSource(t *testing.T) {
	ts := StaticTokenSource("my-token")
	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if tok.AccessToken != "my-token" {
		t.Fatalf("expected 'my-token', got %q", tok.AccessToken)
	}

	emptyTS := StaticTokenSource("")
	_, err = emptyTS.Token()
	if !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("expected ErrAuthRequired, got %v", err)
	}
}

func TestEnvTokenSource(t *testing.T) {
	const envKey1 = "TEST_AUTH_TOKEN_PRIMARY"
	const envKey2 = "TEST_AUTH_TOKEN_SECONDARY"

	t.Setenv(envKey1, "")
	t.Setenv(envKey2, "")

	ts := EnvTokenSource(envKey1, envKey2)
	_, err := ts.Token()
	if !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("expected ErrAuthRequired when env vars are unset, got %v", err)
	}

	t.Setenv(envKey2, "secondary-val")
	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if tok.AccessToken != "secondary-val" {
		t.Fatalf("expected 'secondary-val', got %q", tok.AccessToken)
	}

	t.Setenv(envKey1, "primary-val")
	tok, err = ts.Token()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if tok.AccessToken != "primary-val" {
		t.Fatalf("expected 'primary-val' (priority), got %q", tok.AccessToken)
	}
}

func TestLoadAndSaveTokenFile(t *testing.T) {
	// Empty path validation
	if err := SaveTokenFile("", &oauth2.Token{}); err == nil {
		t.Fatal("expected error for empty path in SaveTokenFile")
	}
	if err := SaveTokenFile("path", nil); err == nil {
		t.Fatal("expected error for nil token in SaveTokenFile")
	}
	if _, err := LoadTokenFile(""); err == nil {
		t.Fatal("expected error for empty path in LoadTokenFile")
	}

	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "nested", "dir", "token.json")

	// File doesn't exist yet
	if _, err := LoadTokenFile(tokenPath); err == nil {
		t.Fatal("expected error loading non-existent token file")
	}

	origTok := &oauth2.Token{
		AccessToken:  "access-123",
		TokenType:    "Bearer",
		RefreshToken: "refresh-456",
		Expiry:       time.Now().Add(1 * time.Hour).Truncate(time.Second),
	}

	if err := SaveTokenFile(tokenPath, origTok); err != nil {
		t.Fatalf("SaveTokenFile failed: %v", err)
	}

	// Verify permissions on file
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("os.Stat failed: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("expected 0600 file permission, got %o", perm)
	}

	loadedTok, err := LoadTokenFile(tokenPath)
	if err != nil {
		t.Fatalf("LoadTokenFile failed: %v", err)
	}
	if loadedTok.AccessToken != origTok.AccessToken {
		t.Errorf("expected access token %q, got %q", origTok.AccessToken, loadedTok.AccessToken)
	}
	if loadedTok.RefreshToken != origTok.RefreshToken {
		t.Errorf("expected refresh token %q, got %q", origTok.RefreshToken, loadedTok.RefreshToken)
	}
	if !loadedTok.Expiry.Equal(origTok.Expiry) {
		t.Errorf("expected expiry %v, got %v", origTok.Expiry, loadedTok.Expiry)
	}

	// Corrupted file test
	if err := os.WriteFile(tokenPath, []byte("invalid-json"), 0600); err != nil {
		t.Fatalf("failed to write corrupt file: %v", err)
	}
	if _, err := LoadTokenFile(tokenPath); err == nil {
		t.Fatal("expected error for corrupt JSON token file")
	}
}

func TestPersistentTokenSource_LoadInitial(t *testing.T) {
	ctx := context.Background()
	loaded := false
	saved := false

	ts := NewPersistentTokenSource(ctx, nil, PersistentConfig{
		Load: func() (*oauth2.Token, error) {
			loaded = true
			return &oauth2.Token{
				AccessToken: "loaded-access-token",
				Expiry:      time.Now().Add(10 * time.Minute),
			}, nil
		},
		Save: func(t *oauth2.Token) error {
			saved = true
			return nil
		},
	})

	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !loaded {
		t.Error("expected Load() to have been called")
	}
	if saved {
		t.Error("Save() should not be called when token is unchanged from Load()")
	}
	if tok.AccessToken != "loaded-access-token" {
		t.Errorf("expected 'loaded-access-token', got %q", tok.AccessToken)
	}
}

func TestPersistentTokenSource_LoginWhenMissing(t *testing.T) {
	ctx := context.Background()
	loginCalled := false
	var savedToken *oauth2.Token

	ts := NewPersistentTokenSource(ctx, nil, PersistentConfig{
		Load: func() (*oauth2.Token, error) {
			return nil, os.ErrNotExist
		},
		Login: func(ctx context.Context) (*oauth2.Token, error) {
			loginCalled = true
			return &oauth2.Token{
				AccessToken:  "newly-logged-in-token",
				RefreshToken: "refresh-token-1",
				Expiry:       time.Now().Add(1 * time.Hour),
			}, nil
		},
		Save: func(tok *oauth2.Token) error {
			savedToken = tok
			return nil
		},
	})

	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !loginCalled {
		t.Error("expected Login() to be called")
	}
	if savedToken == nil || savedToken.AccessToken != "newly-logged-in-token" {
		t.Errorf("expected Save() with newly logged in token, got %+v", savedToken)
	}
	if tok.AccessToken != "newly-logged-in-token" {
		t.Errorf("expected token 'newly-logged-in-token', got %q", tok.AccessToken)
	}
}

func TestPersistentTokenSource_MissingNoLogin(t *testing.T) {
	ctx := context.Background()
	ts := NewPersistentTokenSource(ctx, nil, PersistentConfig{
		Load: func() (*oauth2.Token, error) {
			return nil, os.ErrNotExist
		},
	})

	_, err := ts.Token()
	if !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("expected ErrAuthRequired when missing and no Login configured, got %v", err)
	}
}

func TestPersistentTokenSource_RefreshAndSave(t *testing.T) {
	// Setup a mock OAuth2 token refresh server
	var refreshRequests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshRequests, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "refreshed-access-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "new-refresh-token",
		})
	}))
	defer server.Close()

	cfg := &oauth2.Config{
		ClientID: "test-client-id",
		Endpoint: oauth2.Endpoint{
			TokenURL: server.URL,
		},
	}

	var savedToken *oauth2.Token
	ts := NewPersistentTokenSource(context.Background(), cfg, PersistentConfig{
		Load: func() (*oauth2.Token, error) {
			// Return an already expired token with a refresh token to trigger auto-refresh
			return &oauth2.Token{
				AccessToken:  "expired-token",
				RefreshToken: "initial-refresh-token",
				Expiry:       time.Now().Add(-1 * time.Minute),
			}, nil
		},
		Save: func(tok *oauth2.Token) error {
			savedToken = tok
			return nil
		},
	})

	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if tok.AccessToken != "refreshed-access-token" {
		t.Errorf("expected refreshed token 'refreshed-access-token', got %q", tok.AccessToken)
	}
	if savedToken == nil || savedToken.AccessToken != "refreshed-access-token" {
		t.Errorf("expected Save() to be called with refreshed token, got %+v", savedToken)
	}
	if atomic.LoadInt32(&refreshRequests) != 1 {
		t.Errorf("expected 1 refresh request, got %d", refreshRequests)
	}
}

func TestPersistentTokenSource_ReLoginWhenRefreshFails(t *testing.T) {
	// Setup a mock OAuth2 server that rejects refresh (e.g. invalid_grant)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"token revoked"}`))
	}))
	defer server.Close()

	cfg := &oauth2.Config{
		ClientID: "test-client-id",
		Endpoint: oauth2.Endpoint{
			TokenURL: server.URL,
		},
	}

	loginCalled := false
	var savedToken *oauth2.Token

	ts := NewPersistentTokenSource(context.Background(), cfg, PersistentConfig{
		Load: func() (*oauth2.Token, error) {
			// Expired token that fails to refresh
			return &oauth2.Token{
				AccessToken:  "expired-revoked-token",
				RefreshToken: "revoked-refresh-token",
				Expiry:       time.Now().Add(-10 * time.Minute),
			}, nil
		},
		Login: func(ctx context.Context) (*oauth2.Token, error) {
			loginCalled = true
			return &oauth2.Token{
				AccessToken:  "recovered-access-token",
				RefreshToken: "recovered-refresh-token",
				Expiry:       time.Now().Add(1 * time.Hour),
			}, nil
		},
		Save: func(tok *oauth2.Token) error {
			savedToken = tok
			return nil
		},
	})

	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("expected recovery through Login(), got error: %v", err)
	}
	if !loginCalled {
		t.Error("expected Login() to have been called upon refresh failure")
	}
	if tok.AccessToken != "recovered-access-token" {
		t.Errorf("expected 'recovered-access-token', got %q", tok.AccessToken)
	}
	if savedToken == nil || savedToken.AccessToken != "recovered-access-token" {
		t.Errorf("expected Save() with recovered token, got %+v", savedToken)
	}
}

func TestPersistentTokenSource_Concurrency(t *testing.T) {
	var counter int64
	ts := NewPersistentTokenSource(context.Background(), nil, PersistentConfig{
		Load: func() (*oauth2.Token, error) {
			atomic.AddInt64(&counter, 1)
			return &oauth2.Token{
				AccessToken: "concurrent-token",
				Expiry:      time.Now().Add(1 * time.Hour),
			}, nil
		},
	})

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			tok, err := ts.Token()
			if err != nil || tok.AccessToken != "concurrent-token" {
				t.Errorf("unexpected error or token: tok=%v, err=%v", tok, err)
			}
		})
	}
	wg.Wait()

	if atomic.LoadInt64(&counter) != 1 {
		t.Errorf("expected Load() to be executed only once, executed %d times", counter)
	}
}

func TestBeginAuthAndCompleteAuth(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" {
			t.Errorf("expected grant_type authorization_code, got %q", r.Form.Get("grant_type"))
		}
		if r.Form.Get("code") != "valid-auth-code" {
			t.Errorf("expected code 'valid-auth-code', got %q", r.Form.Get("code"))
		}
		if r.Form.Get("code_verifier") == "" {
			t.Error("expected code_verifier in token request")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "exchanged-access-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "exchanged-refresh-token",
		})
	}))
	defer tokenServer.Close()

	cfg := &oauth2.Config{
		ClientID: "test-client",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.example.com/o/oauth2/auth",
			TokenURL: tokenServer.URL,
		},
		RedirectURL: "http://localhost:51121/callback",
		Scopes:      []string{"scope1", "scope2"},
	}

	session, err := BeginAuth(cfg)
	if err != nil {
		t.Fatalf("BeginAuth failed: %v", err)
	}

	if session.State == "" {
		t.Fatal("expected non-empty state")
	}
	if session.CodeVerifier == "" {
		t.Fatal("expected non-empty code verifier")
	}
	if session.RedirectURI != cfg.RedirectURL {
		t.Fatalf("expected redirect URI %q, got %q", cfg.RedirectURL, session.RedirectURI)
	}

	parsedURL, err := url.Parse(session.AuthURL)
	if err != nil {
		t.Fatalf("failed to parse auth URL: %v", err)
	}
	q := parsedURL.Query()
	if q.Get("state") != session.State {
		t.Errorf("expected state %q in AuthURL query, got %q", session.State, q.Get("state"))
	}
	if q.Get("code_challenge") == "" {
		t.Error("expected code_challenge in AuthURL query")
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("expected S256 challenge method, got %q", q.Get("code_challenge_method"))
	}

	// CompleteAuth: state mismatch test
	ctx := context.Background()
	_, err = CompleteAuth(ctx, cfg, session, "valid-auth-code", "wrong-state")
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("expected state mismatch error, got %v", err)
	}

	// CompleteAuth: empty code test
	_, err = CompleteAuth(ctx, cfg, session, "", session.State)
	if err == nil || !strings.Contains(err.Error(), "missing authorization code") {
		t.Fatalf("expected missing code error, got %v", err)
	}

	// CompleteAuth: successful exchange
	tok, err := CompleteAuth(ctx, cfg, session, "valid-auth-code", session.State)
	if err != nil {
		t.Fatalf("CompleteAuth failed: %v", err)
	}
	if tok.AccessToken != "exchanged-access-token" {
		t.Errorf("expected 'exchanged-access-token', got %q", tok.AccessToken)
	}
}

func TestLoginLoopback_Success(t *testing.T) {
	// 1. Mock token exchange server
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "loopback-access-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "loopback-refresh-token",
		})
	}))
	defer tokenServer.Close()

	cfg := &oauth2.Config{
		ClientID: "test-client",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.example.com/auth",
			TokenURL: tokenServer.URL,
		},
		RedirectURL: "http://localhost:0/callback",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	onAuthURLCalled := false
	tok, err := LoginLoopback(ctx, cfg, LoopbackOptions{
		OnAuthURL: func(authURL string) {
			onAuthURLCalled = true
			u, err := url.Parse(authURL)
			if err != nil {
				t.Errorf("failed to parse authURL: %v", err)
				return
			}
			state := u.Query().Get("state")
			redirectURI := u.Query().Get("redirect_uri")

			// Simulate browser completing OAuth login by sending GET to redirect_uri
			go func() {
				callbackURL := fmt.Sprintf("%s?code=test-code-123&state=%s", redirectURI, state)
				resp, err := http.Get(callbackURL)
				if err != nil {
					t.Errorf("simulated callback GET failed: %v", err)
					return
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				if !strings.Contains(string(body), "Authentication Successful") {
					t.Errorf("expected success HTML, got %s", string(body))
				}
			}()
		},
	})

	if err != nil {
		t.Fatalf("LoginLoopback failed: %v", err)
	}
	if !onAuthURLCalled {
		t.Error("expected OnAuthURL to be invoked")
	}
	if tok.AccessToken != "loopback-access-token" {
		t.Errorf("expected 'loopback-access-token', got %q", tok.AccessToken)
	}
}

func TestLoginLoopback_CustomTemplateAndHandler(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok",
			"token_type":   "Bearer",
		})
	}))
	defer tokenServer.Close()

	// 1. Custom template test
	customTmpl := template.Must(template.New("custom").Parse("CUSTOM_OK:{{.Success}}"))
	cfg := &oauth2.Config{
		ClientID: "test",
		Endpoint: oauth2.Endpoint{TokenURL: tokenServer.URL},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := LoginLoopback(ctx, cfg, LoopbackOptions{
		Template: customTmpl,
		OnAuthURL: func(authURL string) {
			u, _ := url.Parse(authURL)
			state := u.Query().Get("state")
			redirectURI := u.Query().Get("redirect_uri")
			go func() {
				resp, err := http.Get(fmt.Sprintf("%s?code=abc&state=%s", redirectURI, state))
				if err != nil {
					return
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				if string(body) != "CUSTOM_OK:true" {
					t.Errorf("expected 'CUSTOM_OK:true', got %q", string(body))
				}
			}()
		},
	})
	if err != nil {
		t.Fatalf("LoginLoopback with custom template failed: %v", err)
	}

	// 2. Custom ResponseHandler test
	responseHandlerInvoked := false
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()

	_, err = LoginLoopback(ctx2, cfg, LoopbackOptions{
		ResponseHandler: func(w http.ResponseWriter, r *http.Request, data CallbackData) {
			responseHandlerInvoked = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("HANDLED_BY_CUSTOM"))
		},
		OnAuthURL: func(authURL string) {
			u, _ := url.Parse(authURL)
			state := u.Query().Get("state")
			redirectURI := u.Query().Get("redirect_uri")
			go func() {
				resp, err := http.Get(fmt.Sprintf("%s?code=abc&state=%s", redirectURI, state))
				if err != nil {
					return
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				if string(body) != "HANDLED_BY_CUSTOM" {
					t.Errorf("expected 'HANDLED_BY_CUSTOM', got %q", string(body))
				}
			}()
		},
	})
	if err != nil {
		t.Fatalf("LoginLoopback with custom handler failed: %v", err)
	}
	if !responseHandlerInvoked {
		t.Error("expected custom ResponseHandler to be called")
	}
}

func TestLoginLoopback_CallbackErrors(t *testing.T) {
	cfg := &oauth2.Config{
		ClientID: "test",
		Endpoint: oauth2.Endpoint{TokenURL: "http://unused"},
	}

	// Error param from IdP
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := LoginLoopback(ctx, cfg, LoopbackOptions{
		OnAuthURL: func(authURL string) {
			u, _ := url.Parse(authURL)
			redirectURI := u.Query().Get("redirect_uri")
			go func() {
				_, _ = http.Get(fmt.Sprintf("%s?error=access_denied&error_description=User+declined", redirectURI))
			}()
		},
	})
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("expected access_denied error, got %v", err)
	}

	// State mismatch error
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()

	_, err = LoginLoopback(ctx2, cfg, LoopbackOptions{
		OnAuthURL: func(authURL string) {
			u, _ := url.Parse(authURL)
			redirectURI := u.Query().Get("redirect_uri")
			go func() {
				_, _ = http.Get(fmt.Sprintf("%s?code=123&state=wrong-state", redirectURI))
			}()
		},
	})
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("expected state mismatch error, got %v", err)
	}

	// Context cancellation
	ctx3, cancel3 := context.WithCancel(context.Background())
	cancel3()

	_, err = LoginLoopback(ctx3, cfg, LoopbackOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestReceiveCallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	redirectURL := fmt.Sprintf("http://localhost:%d/my_callback", port)
	authURL := "https://auth.example.com/oauth?state=rc-state"

	res, err := ReceiveCallback(ctx, redirectURL, authURL, LoopbackOptions{
		OnAuthURL: func(url string) {
			if url != authURL {
				t.Errorf("expected %q, got %q", authURL, url)
			}
			go func() {
				resp, err := http.Get(fmt.Sprintf("%s?code=rc-code&state=rc-state", redirectURL))
				if err != nil {
					t.Errorf("GET to callback failed: %v", err)
					return
				}
				defer resp.Body.Close()
			}()
		},
	})
	if err != nil {
		t.Fatalf("ReceiveCallback failed: %v", err)
	}
	if res.Code != "rc-code" || res.State != "rc-state" {
		t.Errorf("unexpected result: %+v", res)
	}

	lAfter, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		t.Errorf("expected port %d to be released, but listen failed: %v", port, err)
	} else {
		lAfter.Close()
	}
}
