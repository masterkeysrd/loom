package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/auth/extauth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// DefaultRedirectPort is the default port used for the local OAuth callback server.
const DefaultRedirectPort = 3142

// cliCodeReceiver handles the OAuth callback from the IdP's authorization endpoint.
type cliCodeReceiver struct {
	port     int
	authChan chan *auth.AuthorizationResult
	errChan  chan error
	server   *http.Server
}

func newCliCodeReceiver(port int) *cliCodeReceiver {
	if port == 0 {
		port = DefaultRedirectPort
	}
	return &cliCodeReceiver{
		port:     port,
		authChan: make(chan *auth.AuthorizationResult),
		errChan:  make(chan error),
	}
}

func (r *cliCodeReceiver) serve(ctx context.Context) error {
	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", r.port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", r.port, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		r.authChan <- &auth.AuthorizationResult{
			Code:  req.URL.Query().Get("code"),
			State: req.URL.Query().Get("state"),
		}
		fmt.Fprint(w, "Authentication successful. You can close this window.")
	})

	r.server = &http.Server{
		Handler: mux,
	}

	go func() {
		if err := r.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.errChan <- err
		}
	}()

	return nil
}

func (r *cliCodeReceiver) getAuthorizationCode(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
	fmt.Printf("\nPlease open the following URL in your browser to authenticate:\n%s\n\n", args.URL)
	select {
	case authRes := <-r.authChan:
		return authRes, nil
	case err := <-r.errChan:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *cliCodeReceiver) close() {
	if r.server != nil {
		r.server.Close()
	}
}

// OAuth2Provider implements AuthProvider for standard OAuth2 flows.
type OAuth2Provider struct {
	ClientID     string
	ClientSecret string
	RedirectPort int
}

func (p *OAuth2Provider) GetHandler(ctx context.Context) (auth.OAuthHandler, error) {
	receiver := newCliCodeReceiver(p.RedirectPort)
	if err := receiver.serve(ctx); err != nil {
		return nil, err
	}

	config := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL:              fmt.Sprintf("http://localhost:%d", receiver.port),
		AuthorizationCodeFetcher: receiver.getAuthorizationCode,
	}

	if p.ClientID != "" {
		config.PreregisteredClient = &oauthex.ClientCredentials{
			ClientID: p.ClientID,
		}
		if p.ClientSecret != "" {
			config.PreregisteredClient.ClientSecretAuth = &oauthex.ClientSecretAuth{
				ClientSecret: p.ClientSecret,
			}
		}
	}

	handler, err := auth.NewAuthorizationCodeHandler(config)
	if err != nil {
		receiver.close()
		return nil, err
	}

	return handler, nil
}

// EnterpriseProvider implements AuthProvider for Enterprise Managed Authorization.
type EnterpriseProvider struct {
	ClientID         string
	ClientSecret     string
	RedirectPort     int
	IdPIssuerURL     string
	MCPAuthServerURL string
	MCPResourceURI   string
}

func (p *EnterpriseProvider) GetHandler(ctx context.Context) (auth.OAuthHandler, error) {
	receiver := newCliCodeReceiver(p.RedirectPort)
	if err := receiver.serve(ctx); err != nil {
		return nil, err
	}

	idpCreds := &oauthex.ClientCredentials{
		ClientID: p.ClientID,
	}
	if p.ClientSecret != "" {
		idpCreds.ClientSecretAuth = &oauthex.ClientSecretAuth{
			ClientSecret: p.ClientSecret,
		}
	}

	idTokenFetcher := func(ctx context.Context) (*oauth2.Token, error) {
		oidcConfig := &extauth.OIDCLoginConfig{
			IssuerURL:   p.IdPIssuerURL,
			Credentials: idpCreds,
			RedirectURL: fmt.Sprintf("http://localhost:%d", receiver.port),
			Scopes:      []string{"openid", "profile", "email"},
		}
		return extauth.PerformOIDCLogin(ctx, oidcConfig, receiver.getAuthorizationCode)
	}

	handler, err := extauth.NewEnterpriseHandler(&extauth.EnterpriseHandlerConfig{
		IdPIssuerURL:   p.IdPIssuerURL,
		IdPCredentials: idpCreds,

		MCPAuthServerURL: p.MCPAuthServerURL,
		MCPResourceURI:   p.MCPResourceURI,
		MCPScopes:        []string{"read", "write"},

		IDTokenFetcher: idTokenFetcher,
	})
	if err != nil {
		receiver.close()
		return nil, err
	}

	return handler, nil
}
