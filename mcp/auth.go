package mcp

import (
	"context"
	"errors"
	"net/http"

	loomauth "github.com/masterkeysrd/loom/auth"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/auth/extauth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// TokenSourceProvider adapts any standard [oauth2.TokenSource] directly for MCP authentication.
type TokenSourceProvider struct {
	TokenSource oauth2.TokenSource
}

func (p *TokenSourceProvider) GetHandler(_ context.Context) (mcpauth.OAuthHandler, error) {
	if p.TokenSource == nil {
		return nil, errors.New("mcp: TokenSource is required")
	}
	return &tokenSourceHandler{ts: p.TokenSource}, nil
}

type tokenSourceHandler struct {
	ts oauth2.TokenSource
}

func (h *tokenSourceHandler) TokenSource(_ context.Context) (oauth2.TokenSource, error) {
	return h.ts, nil
}

func (h *tokenSourceHandler) Authorize(_ context.Context, _ *http.Request, resp *http.Response) error {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	_, err := h.ts.Token()
	return err
}

// CodeFetcher is an alias for the MCP SDK's AuthorizationCodeFetcher function type.
type CodeFetcher = mcpauth.AuthorizationCodeFetcher

// LoopbackCodeFetcher returns a [CodeFetcher] that starts a local loopback listener
// at redirectURL on-demand when an authorization challenge occurs.
func LoopbackCodeFetcher(redirectURL string, opts loomauth.LoopbackOptions) CodeFetcher {
	return func(ctx context.Context, args *mcpauth.AuthorizationArgs) (*mcpauth.AuthorizationResult, error) {
		res, err := loomauth.ReceiveCallback(ctx, redirectURL, args.URL, opts)
		if err != nil {
			return nil, err
		}
		return &mcpauth.AuthorizationResult{
			Code:  res.Code,
			State: res.State,
		}, nil
	}
}

func clientCredentials(clientID, clientSecret string) *oauthex.ClientCredentials {
	if clientID == "" {
		return nil
	}
	creds := &oauthex.ClientCredentials{ClientID: clientID}
	if clientSecret != "" {
		creds.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: clientSecret}
	}
	return creds
}

// OAuth2Provider implements AuthProvider for standard OAuth2 flows.
type OAuth2Provider struct {
	ClientID            string
	ClientSecret        string
	RedirectURL         string
	CodeFetcher         CodeFetcher
	InitialTokenSource  oauth2.TokenSource
	NewTokenSource      func(ctx context.Context, cfg *oauth2.Config, tok *oauth2.Token) (oauth2.TokenSource, error)
	RequestRefreshToken bool
	ScopeFilter         func(discovered []string) []string
	HTTPClient          *http.Client
}

func (p *OAuth2Provider) GetHandler(_ context.Context) (mcpauth.OAuthHandler, error) {
	if p.RedirectURL == "" {
		return nil, errors.New("mcp: RedirectURL is required for OAuth2Provider")
	}
	if p.CodeFetcher == nil {
		return nil, errors.New("mcp: CodeFetcher is required for OAuth2Provider")
	}

	return mcpauth.NewAuthorizationCodeHandler(&mcpauth.AuthorizationCodeHandlerConfig{
		PreregisteredClient:      clientCredentials(p.ClientID, p.ClientSecret),
		RedirectURL:              p.RedirectURL,
		AuthorizationCodeFetcher: p.CodeFetcher,
		InitialTokenSource:       p.InitialTokenSource,
		NewTokenSource:           p.NewTokenSource,
		RequestRefreshToken:      p.RequestRefreshToken,
		ScopeFilter:              p.ScopeFilter,
		Client:                   p.HTTPClient,
	})
}

// EnterpriseProvider implements AuthProvider for Enterprise Managed Authorization.
type EnterpriseProvider struct {
	ClientID         string
	ClientSecret     string
	RedirectURL      string
	CodeFetcher      CodeFetcher
	IdPIssuerURL     string
	MCPAuthServerURL string
	MCPResourceURI   string
	MCPScopes        []string
	OIDCScopes       []string
}

func (p *EnterpriseProvider) GetHandler(_ context.Context) (mcpauth.OAuthHandler, error) {
	if p.RedirectURL == "" {
		return nil, errors.New("mcp: RedirectURL is required for EnterpriseProvider")
	}
	if p.CodeFetcher == nil {
		return nil, errors.New("mcp: CodeFetcher is required for EnterpriseProvider")
	}

	oidcScopes := p.OIDCScopes
	if len(oidcScopes) == 0 {
		oidcScopes = []string{"openid", "profile", "email"}
	}
	mcpScopes := p.MCPScopes
	if len(mcpScopes) == 0 {
		mcpScopes = []string{"read", "write"}
	}

	idpCreds := clientCredentials(p.ClientID, p.ClientSecret)
	idTokenFetcher := func(ctx context.Context) (*oauth2.Token, error) {
		return extauth.PerformOIDCLogin(ctx, &extauth.OIDCLoginConfig{
			IssuerURL:   p.IdPIssuerURL,
			Credentials: idpCreds,
			RedirectURL: p.RedirectURL,
			Scopes:      oidcScopes,
		}, p.CodeFetcher)
	}

	return extauth.NewEnterpriseHandler(&extauth.EnterpriseHandlerConfig{
		IdPIssuerURL:     p.IdPIssuerURL,
		IdPCredentials:   idpCreds,
		MCPAuthServerURL: p.MCPAuthServerURL,
		MCPResourceURI:   p.MCPResourceURI,
		MCPCredentials:   idpCreds,
		MCPScopes:        mcpScopes,
		IDTokenFetcher:   idTokenFetcher,
	})
}

// ClientCredentialsProvider implements AuthProvider for OAuth2 Client Credentials (M2M) flows.
type ClientCredentialsProvider struct {
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
}

func (p *ClientCredentialsProvider) GetHandler(_ context.Context) (mcpauth.OAuthHandler, error) {
	if p.ClientID == "" || p.ClientSecret == "" {
		return nil, errors.New("mcp: ClientID and ClientSecret are required for client credentials flow")
	}
	return extauth.NewClientCredentialsHandler(&extauth.ClientCredentialsHandlerConfig{
		Credentials: clientCredentials(p.ClientID, p.ClientSecret),
		HTTPClient:  p.HTTPClient,
	})
}
