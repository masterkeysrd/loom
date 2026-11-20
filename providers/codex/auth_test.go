package loomcodex

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestLoginAndRefresh(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const issuedID = "oaiapp_Test"
	const subject = "account"
	hostID := NewHostID()
	var authURL url.Values
	var refreshes int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{
				"issuer": base, "jwks_uri": base + "/keys",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/keys":
			json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
				"kty": "RSA", "alg": "RS256", "kid": "test",
				"n": base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.E)).Bytes()),
			}}})
		case "/api/accounts/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse form: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if r.Form.Get("client_id") != issuedID || r.Form.Get("resource") != resource || r.Form.Has("client_secret") {
				t.Errorf("invalid token exchange client or resource: %v", r.Form)
			}
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if r.Form.Get("code") != "code" || r.Form.Get("redirect_uri") != authURL.Get("redirect_uri") ||
					base64.RawURLEncoding.EncodeToString(sum[:]) != authURL.Get("code_challenge") {
					t.Errorf("invalid code exchange: %v", r.Form)
				}
				json.NewEncoder(w).Encode(tokenResponse{
					AccessToken: "first", RefreshToken: "initial-refresh", TokenType: "Bearer",
					ExpiresIn: 3600, Scope: "openid " + directScope,
					IDToken: signIDToken(t, privateKey, map[string]any{
						"iss": base, "aud": issuedID, "sub": subject, "nonce": authURL.Get("nonce"),
						"email": "dev@example.com", "exp": time.Now().Add(time.Hour).Unix(),
					}),
				})
			case "refresh_token":
				refreshes++
				if r.Form.Get("refresh_token") != "initial-refresh" || r.Form.Has("scope") {
					t.Errorf("invalid refresh form: %v", r.Form)
				}
				json.NewEncoder(w).Encode(tokenResponse{
					AccessToken: "second", RefreshToken: "rotated", TokenType: "Bearer", ExpiresIn: 3600, Scope: directScope,
				})
			default:
				t.Errorf("unexpected grant: %s", r.Form.Get("grant_type"))
				w.WriteHeader(http.StatusBadRequest)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	openBrowser := func(target string) error {
		u, err := url.Parse(target)
		if err != nil {
			return err
		}
		q := u.Query()
		if u.Path != "/api/accounts/authorize" || q.Get("client_id") != dynamicClientID ||
			q.Get("agent_name_hint") != "Loom Example" || q.Get("resource") != resource ||
			q.Get("ext_agent_host_id") != hostID || q.Get("nonce") == "" || q.Get("state") == "" ||
			q.Get("code_challenge_method") != "S256" || !strings.Contains(q.Get("scope"), directScope) {
			t.Errorf("unexpected authorization URL: %s", target)
		}
		mu.Lock()
		authURL = q
		mu.Unlock()
		callback := q.Get("redirect_uri") + "?" + url.Values{"code": {"code"}, "state": {q.Get("state")}, "client_id": {issuedID}}.Encode()
		resp, err := http.Get(callback)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("callback returned HTTP %d", resp.StatusCode)
		}
		return nil
	}
	creds, err := login(t.Context(), LoginOptions{AgentName: "Loom Example", HostID: hostID, OpenURL: openBrowser}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if creds.ClientID != issuedID || creds.Subject != subject || creds.HostID != hostID || creds.Email != "dev@example.com" {
		t.Fatalf("unexpected account metadata: %s %s %s %s", creds.ClientID, creds.Subject, creds.HostID, creds.Email)
	}
	creds.Token.Expiry = time.Now().Add(-time.Minute)
	saveErr := errors.New("disk unavailable")
	var saves int
	source, err := newTokenSource(t.Context(), creds, func(replacement Credentials) error {
		saves++
		if replacement.Token.AccessToken != "second" || replacement.Token.RefreshToken != "rotated" || replacement.ClientID != issuedID {
			t.Error("persisted record did not contain the rotated credentials")
		}
		if saves == 1 {
			return saveErr
		}
		return nil
	}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Token(); !errors.Is(err, saveErr) {
		t.Fatalf("expected persistence error, got %v", err)
	}
	token, err := source.Token()
	if err != nil || token == nil || token.AccessToken != "second" {
		t.Fatalf("could not retrieve rotated token: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if refreshes != 1 || saves != 2 {
		t.Fatalf("got %d refreshes and %d save calls, want one refresh and two saves", refreshes, saves)
	}
}

func TestTokenSourceRequiresDirectConsent(t *testing.T) {
	_, err := NewTokenSource(t.Context(), &Credentials{
		ClientID: "oaiapp_Test", Subject: "account", Scopes: []string{"openid"},
		Token: oauth2.Token{AccessToken: "example", Expiry: time.Now().Add(time.Hour)},
	}, nil)
	if err == nil {
		t.Fatal("accepted an account without direct plan usage permission")
	}
}

func signIDToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Error(err)
		return ""
	}
	encode := base64.RawURLEncoding.EncodeToString
	unsigned := encode([]byte(`{"alg":"RS256","kid":"test"}`)) + "." + encode(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Error(err)
		return ""
	}
	return unsigned + "." + encode(signature)
}
