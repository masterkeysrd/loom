package loomcodex

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/masterkeysrd/loom/auth"
	"github.com/masterkeysrd/loom/internal/authstate"
	"golang.org/x/oauth2"
)

const (
	issuer          = "https://auth.openai.com"
	dynamicClientID = "dynamic_agent_client"
	directScope     = "chatgpt.tokens.use.direct"
)

// Credentials stores one verified ChatGPT account registration. Protect all
// tokens and write replacements atomically when persisting it.
type Credentials struct {
	ClientID string       `json:"client_id"`
	HostID   string       `json:"ext_agent_host_id"`
	Subject  string       `json:"subject"`
	Email    string       `json:"email,omitempty"`
	IDToken  string       `json:"id_token"`
	Scopes   []string     `json:"scopes"`
	Token    oauth2.Token `json:"token"`
}

// LoginOptions configures the browser authorization for a local agent host.
type LoginOptions struct {
	// AgentName is the app's actual name and is required on first registration.
	AgentName string
	// HostID is a stable identifier generated and persisted before first sign-in.
	HostID string
	// Previous selects an existing account registration for reauthorization.
	Previous *Credentials
	// OpenURL opens the authorization URL in the system browser. The default
	// uses [auth.OpenBrowser]. Never log the URL, which may contain an ID token.
	OpenURL func(string) error
}

// NewHostID generates an opaque OAuth host identifier. Persist it once per host.
func NewHostID() string { return "urn:uuid:" + uuid.NewString() }

// Login starts a local PKCE callback and validates the OpenID Connect ID token
// before returning credentials authorized for direct ChatGPT plan usage.
func Login(ctx context.Context, opts LoginOptions) (*Credentials, error) {
	return login(ctx, opts, issuer)
}

func login(ctx context.Context, opts LoginOptions, authIssuer string) (*Credentials, error) {
	if opts.HostID == "" {
		return nil, errors.New("codex: a persistent host ID is required")
	}
	clientID := dynamicClientID
	if opts.Previous != nil {
		clientID = opts.Previous.ClientID
		if clientID == "" || clientID == dynamicClientID || opts.Previous.Subject == "" {
			return nil, errors.New("codex: previous credentials must identify a registered account")
		}
	} else if opts.AgentName == "" {
		return nil, errors.New("codex: an agent name is required for registration")
	}
	result, err := authorize(ctx, opts, authIssuer, clientID)
	var endpointErr *tokenEndpointError
	if errors.As(err, &endpointErr) && endpointErr.code == "invalid_grant" && result.clientID != "" {
		// Each retry uses a fresh PKCE code, state, nonce, and callback port.
		result, err = authorize(ctx, opts, authIssuer, result.clientID)
	}
	if err != nil {
		return nil, err
	}
	scopes := strings.Fields(result.token.Scope)
	if !slices.Contains(scopes, directScope) {
		return nil, fmt.Errorf("codex: %w: ChatGPT plan usage permission was not granted", auth.ErrAuthRequired)
	}
	if result.token.IDToken == "" {
		return nil, errors.New("codex: token response did not include an ID token")
	}
	provider, err := oidc.NewProvider(ctx, authIssuer)
	if err != nil {
		return nil, fmt.Errorf("codex: discover OpenID Connect configuration: %w", err)
	}
	verified, err := provider.VerifierContext(ctx, &oidc.Config{ClientID: result.clientID}).Verify(ctx, result.token.IDToken)
	if err != nil {
		return nil, fmt.Errorf("codex: validate ID token: %w", err)
	}
	if verified.Nonce != result.nonce || verified.Subject == "" {
		return nil, errors.New("codex: invalid ID token nonce or subject")
	}
	if opts.Previous != nil && opts.Previous.Subject != verified.Subject {
		return nil, errors.New("codex: signed-in identity does not match the selected account")
	}
	var identity struct {
		Email string `json:"email"`
	}
	if err := verified.Claims(&identity); err != nil {
		return nil, fmt.Errorf("codex: decode account identity: %w", err)
	}
	return &Credentials{
		ClientID: result.clientID, HostID: opts.HostID, Subject: verified.Subject,
		Email: identity.Email, IDToken: result.token.IDToken, Scopes: scopes,
		Token: result.token.oauthToken(),
	}, nil
}

type authorization struct {
	clientID string
	nonce    string
	token    tokenResponse
}

func authorize(ctx context.Context, opts LoginOptions, authIssuer, clientID string) (authorization, error) {
	nonce := make([]byte, 32)
	rand.Read(nonce)
	result := authorization{nonce: base64.RawURLEncoding.EncodeToString(nonce)}
	cfg := &oauth2.Config{
		ClientID:    clientID,
		RedirectURL: "http://127.0.0.1:0/auth/callback",
		Scopes:      []string{"openid", "profile", "email", "offline_access", "resource.invoke", directScope},
		Endpoint:    oauth2.Endpoint{AuthURL: authIssuer + "/api/accounts/authorize"},
	}
	params := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("resource", resource),
		oauth2.SetAuthURLParam("ext_agent_host_id", opts.HostID),
		oauth2.SetAuthURLParam("nonce", result.nonce),
	}
	if clientID == dynamicClientID {
		params = append(params, oauth2.SetAuthURLParam("agent_name_hint", opts.AgentName))
	} else if opts.Previous != nil && opts.Previous.IDToken != "" {
		params = append(params, oauth2.SetAuthURLParam("id_token_hint", opts.Previous.IDToken))
	}
	openURL := opts.OpenURL
	if openURL == nil {
		openURL = auth.OpenBrowser
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var openErr error
	session, callback, err := auth.AuthorizeLoopback(ctx, cfg, auth.LoopbackOptions{
		AuthCodeOptions: params,
		OnAuthURL: func(target string) {
			if openErr = openURL(target); openErr != nil {
				cancel()
			}
		},
	})
	if openErr != nil {
		return result, errors.New("codex: could not open the ChatGPT authorization URL")
	}
	if err != nil {
		return result, fmt.Errorf("codex: authorize ChatGPT account: %w", err)
	}
	if clientID == dynamicClientID {
		if callback.ClientID == "" || callback.ClientID == dynamicClientID {
			return result, errors.New("codex: authorization did not return an issued client ID")
		}
		clientID = callback.ClientID
	} else if callback.ClientID != "" && callback.ClientID != clientID {
		return result, errors.New("codex: authorization returned a different client ID")
	}
	result.clientID = clientID
	result.token, err = exchangeToken(ctx, authIssuer, clientID, url.Values{
		"grant_type": {"authorization_code"}, "code": {callback.Code},
		"redirect_uri": {session.RedirectURI}, "code_verifier": {session.CodeVerifier},
	})
	return result, err
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

func (t tokenResponse) oauthToken() oauth2.Token {
	return oauth2.Token{
		AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, TokenType: "Bearer",
		Expiry: time.Now().Add(time.Duration(t.ExpiresIn) * time.Second),
	}
}

type tokenEndpointError struct {
	status int
	code   string
}

func (e *tokenEndpointError) Error() string {
	if e.code == "invalid_grant" {
		return fmt.Sprintf("codex: token exchange rejected the grant (HTTP %d, invalid_grant)", e.status)
	}
	return fmt.Sprintf("codex: token exchange failed (HTTP %d)", e.status)
}

func exchangeToken(ctx context.Context, authIssuer, clientID string, form url.Values) (tokenResponse, error) {
	form.Set("client_id", clientID)
	form.Set("resource", resource)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authIssuer+"/api/accounts/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := http.DefaultClient
	if customClient, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok && customClient != nil {
		client = customClient
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := clientCopy.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("codex: request token exchange: %w", err)
	}
	defer resp.Body.Close()
	var token tokenResponse
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&token)
	if resp.StatusCode != http.StatusOK {
		return tokenResponse{}, &tokenEndpointError{status: resp.StatusCode, code: token.Error}
	}
	if err != nil {
		return tokenResponse{}, fmt.Errorf("codex: decode token response: %w", err)
	}
	if token.AccessToken == "" || token.RefreshToken == "" || token.ExpiresIn <= 0 || !strings.EqualFold(token.TokenType, "Bearer") {
		return tokenResponse{}, errors.New("codex: token response did not include a valid renewable bearer session")
	}
	return token, nil
}

// NewTokenSource creates a reusable token source from a previously verified account
// registration. Use a context with the same lifetime as the source. Refreshes are
// serialized within the source. If provided, save persists each rotated token set
// before it is used. Applications should use one source per selected registration
// and coordinate refreshes across processes that share a registration.
func NewTokenSource(ctx context.Context, credentials *Credentials, save func(Credentials) error) (oauth2.TokenSource, error) {
	return newTokenSource(ctx, credentials, save, issuer)
}

func newTokenSource(ctx context.Context, credentials *Credentials, save func(Credentials) error, authIssuer string) (oauth2.TokenSource, error) {
	if credentials == nil || credentials.ClientID == "" || credentials.ClientID == dynamicClientID ||
		credentials.Subject == "" || !slices.Contains(credentials.Scopes, directScope) ||
		credentials.Token.Expiry.IsZero() || credentials.Token.AccessToken == "" {
		return nil, auth.ErrAuthRequired
	}
	copy := *credentials
	copy.Scopes = slices.Clone(credentials.Scopes)
	source := &refreshSource{ctx: ctx, credentials: copy, issuer: authIssuer}
	if save != nil {
		source.pendingSave.Write = func(record Credentials) error {
			record.Scopes = slices.Clone(record.Scopes)
			if err := save(record); err != nil {
				return fmt.Errorf("codex: persist refreshed session: %w", err)
			}
			return nil
		}
	}
	return source, nil
}

type refreshSource struct {
	ctx         context.Context
	issuer      string
	mu          sync.Mutex
	credentials Credentials
	pendingSave authstate.PendingSave[Credentials]
}

func (s *refreshSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.pendingSave.Retry(); err != nil {
		return nil, err
	}
	if time.Until(s.credentials.Token.Expiry) <= 30*time.Second {
		if s.credentials.Token.RefreshToken == "" {
			return nil, auth.ErrAuthRequired
		}
		token, err := exchangeToken(s.ctx, s.issuer, s.credentials.ClientID, url.Values{
			"grant_type": {"refresh_token"}, "refresh_token": {s.credentials.Token.RefreshToken},
		})
		var endpointErr *tokenEndpointError
		if errors.As(err, &endpointErr) && endpointErr.code == "invalid_grant" {
			return nil, fmt.Errorf("codex: %w: %w", auth.ErrAuthRequired, err)
		}
		if err != nil {
			return nil, err
		}
		s.credentials.Token = token.oauthToken()
		if token.Scope != "" {
			s.credentials.Scopes = strings.Fields(token.Scope)
		}
		if err := s.pendingSave.Save(s.credentials); err != nil {
			return nil, err
		}
	}
	if !slices.Contains(s.credentials.Scopes, directScope) {
		return nil, auth.ErrAuthRequired
	}
	token := s.credentials.Token
	return &token, nil
}
