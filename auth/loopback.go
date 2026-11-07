package auth

import (
	"cmp"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// AuthSession captures the transient state of an in-flight OAuth 2.0 PKCE flow.
type AuthSession struct {
	AuthURL      string `json:"auth_url"`
	State        string `json:"state"`
	CodeVerifier string `json:"code_verifier"`
	RedirectURI  string `json:"redirect_uri"`
}

// BeginAuth generates a cryptographic state and PKCE code verifier, builds an
// authorization URL using cfg and opts, and returns an [AuthSession] to be completed later.
//
// If no opts are provided, BeginAuth defaults to requesting offline access with
// explicit consent prompt (oauth2.AccessTypeOffline and prompt=consent).
func BeginAuth(cfg *oauth2.Config, opts ...oauth2.AuthCodeOption) (*AuthSession, error) {
	if cfg == nil {
		return nil, errors.New("auth: oauth2 config cannot be nil")
	}

	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, fmt.Errorf("auth: generate random state: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	verifier := oauth2.GenerateVerifier()
	challengeOpt := oauth2.S256ChallengeOption(verifier)

	if len(opts) == 0 {
		opts = []oauth2.AuthCodeOption{
			oauth2.AccessTypeOffline,
			oauth2.SetAuthURLParam("prompt", "consent"),
		}
	}
	authOpts := append([]oauth2.AuthCodeOption{challengeOpt}, opts...)

	return &AuthSession{
		AuthURL:      cfg.AuthCodeURL(state, authOpts...),
		State:        state,
		CodeVerifier: verifier,
		RedirectURI:  cfg.RedirectURL,
	}, nil
}

// CompleteAuth validates the callback parameters and exchanges the authorization code
// for an [oauth2.Token] using the PKCE code verifier stored in session.
func CompleteAuth(ctx context.Context, cfg *oauth2.Config, session *AuthSession, code, state string, opts ...oauth2.AuthCodeOption) (*oauth2.Token, error) {
	if cfg == nil {
		return nil, errors.New("auth: oauth2 config cannot be nil")
	}
	if session == nil {
		return nil, errors.New("auth: auth session cannot be nil")
	}
	if session.State == "" || state != session.State {
		return nil, errors.New("auth: state mismatch in authorization response")
	}
	if code == "" {
		return nil, errors.New("auth: missing authorization code")
	}

	cfgCopy := *cfg
	cfgCopy.RedirectURL = cmp.Or(session.RedirectURI, cfg.RedirectURL)

	exchangeOpts := append([]oauth2.AuthCodeOption{oauth2.VerifierOption(session.CodeVerifier)}, opts...)
	tok, err := cfgCopy.Exchange(ctx, code, exchangeOpts...)
	if err != nil {
		return nil, fmt.Errorf("auth: exchange authorization code: %w", err)
	}
	return tok, nil
}

// CallbackData contains the status and error details passed to callback templates
// or custom response handlers.
type CallbackData struct {
	Success          bool
	Error            string
	ErrorDescription string
}

// LoopbackOptions configures the local loopback HTTP listener for [LoginLoopback] and [ReceiveCallback].
type LoopbackOptions struct {
	// Template is an optional custom HTML template rendered with CallbackData on completion.
	Template *template.Template
	// ResponseHandler is an optional custom HTTP handler to write the callback response (overrides Template if set).
	ResponseHandler func(w http.ResponseWriter, r *http.Request, data CallbackData)
	// OnAuthURL is called once the local listener is ready with the authorization URL for the user to visit.
	OnAuthURL func(url string)
	// AuthCodeOptions are optional oauth2.AuthCodeOption values passed to AuthCodeURL.
	AuthCodeOptions []oauth2.AuthCodeOption
}

//go:embed callback.html
var defaultCallbackHTML string

// DefaultCallbackTemplate is the standard HTML response rendered on OAuth callback completion.
var DefaultCallbackTemplate = template.Must(template.New("callback").Parse(defaultCallbackHTML))

// RenderCallback writes a standardized or custom callback response to the client.
func RenderCallback(w http.ResponseWriter, r *http.Request, tmpl *template.Template, handler func(w http.ResponseWriter, r *http.Request, data CallbackData), data CallbackData) {
	if handler != nil {
		handler(w, r, data)
		return
	}
	status := http.StatusOK
	if !data.Success {
		status = http.StatusBadRequest
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = cmp.Or(tmpl, DefaultCallbackTemplate).Execute(w, data)
}

// AuthorizationResult captures the authorization code and state returned in an OAuth callback.
type AuthorizationResult struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

type loopbackServer struct {
	redirectURL   *url.URL
	expectedState string
	opts          LoopbackOptions
	listener      net.Listener
	server        *http.Server
	resChan       chan *AuthorizationResult
	errChan       chan error
	closeOnce     sync.Once
}

func parseRedirectURL(raw string) (*url.URL, error) {
	u, err := url.Parse(cmp.Or(raw, "http://localhost:0/callback"))
	if err != nil {
		return nil, fmt.Errorf("auth: invalid redirect URL %q: %w", raw, err)
	}
	u.Scheme = cmp.Or(u.Scheme, "http")
	u.Host = cmp.Or(u.Host, "localhost:0")
	u.Path = cmp.Or(u.Path, "/callback")
	return u, nil
}

func startLoopbackServer(rawRedirectURL string, opts LoopbackOptions) (*loopbackServer, error) {
	u, err := parseRedirectURL(rawRedirectURL)
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", u.Host)
	if err != nil {
		return nil, fmt.Errorf("auth: listen on %s: %w", u.Host, err)
	}
	u.Host = listener.Addr().String()

	s := &loopbackServer{
		redirectURL: u,
		opts:        opts,
		listener:    listener,
		resChan:     make(chan *AuthorizationResult, 1),
		errChan:     make(chan error, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc(u.Path, s.handleCallback)
	s.server = &http.Server{Handler: mux}

	return s, nil
}

func (s *loopbackServer) serve() {
	err := s.server.Serve(s.listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.sendErr(err)
	}
}

func (s *loopbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if errParam := q.Get("error"); errParam != "" {
		errDesc := q.Get("error_description")
		s.render(w, r, CallbackData{Error: errParam, ErrorDescription: errDesc})
		s.sendErr(fmt.Errorf("auth: oauth callback error %s: %s", errParam, errDesc))
		return
	}

	state := q.Get("state")
	if s.expectedState != "" && state != s.expectedState {
		s.render(w, r, CallbackData{
			Error:            "state_mismatch",
			ErrorDescription: "The authorization state returned did not match the expected session state.",
		})
		s.sendErr(errors.New("auth: state mismatch in OAuth callback"))
		return
	}

	code := q.Get("code")
	if code == "" {
		s.render(w, r, CallbackData{
			Error:            "missing_code",
			ErrorDescription: "No authorization code was present in the OAuth callback parameters.",
		})
		s.sendErr(errors.New("auth: missing authorization code in OAuth callback"))
		return
	}

	s.render(w, r, CallbackData{Success: true})
	select {
	case s.resChan <- &AuthorizationResult{Code: code, State: state}:
	default:
	}
}

func (s *loopbackServer) render(w http.ResponseWriter, r *http.Request, data CallbackData) {
	RenderCallback(w, r, s.opts.Template, s.opts.ResponseHandler, data)
}

func (s *loopbackServer) sendErr(err error) {
	select {
	case s.errChan <- err:
	default:
	}
}

func (s *loopbackServer) wait(ctx context.Context) (*AuthorizationResult, error) {
	defer s.close()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-s.errChan:
		return nil, err
	case res := <-s.resChan:
		return res, nil
	}
}

func (s *loopbackServer) close() {
	s.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.server.Shutdown(ctx)
		_ = s.listener.Close()
	})
}

// ReceiveCallback starts a local loopback listener at redirectURL on-demand, invokes
// opts.OnAuthURL(authURL) if provided, waits for the OAuth callback, and shuts down
// the listener before returning.
func ReceiveCallback(ctx context.Context, redirectURL, authURL string, opts LoopbackOptions) (*AuthorizationResult, error) {
	srv, err := startLoopbackServer(redirectURL, opts)
	if err != nil {
		return nil, err
	}
	go srv.serve()
	if opts.OnAuthURL != nil {
		opts.OnAuthURL(authURL)
	}
	return srv.wait(ctx)
}

// LoginLoopback spins up a local HTTP listener at cfg.RedirectURL, directs the user
// through the OAuth 2.0 PKCE flow, captures the authorization code, and completes
// the token exchange.
func LoginLoopback(ctx context.Context, cfg *oauth2.Config, opts LoopbackOptions) (*oauth2.Token, error) {
	if cfg == nil {
		return nil, errors.New("auth: oauth2 config cannot be nil")
	}

	srv, err := startLoopbackServer(cfg.RedirectURL, opts)
	if err != nil {
		return nil, err
	}

	cfgCopy := *cfg
	cfgCopy.RedirectURL = srv.redirectURL.String()

	session, err := BeginAuth(&cfgCopy, opts.AuthCodeOptions...)
	if err != nil {
		srv.close()
		return nil, err
	}
	srv.expectedState = session.State
	go srv.serve()

	if opts.OnAuthURL != nil {
		opts.OnAuthURL(session.AuthURL)
	}

	res, err := srv.wait(ctx)
	if err != nil {
		return nil, err
	}

	return CompleteAuth(ctx, &cfgCopy, session, res.Code, res.State)
}

// OpenBrowser launches the user's default system web browser directed at targetURL.
func OpenBrowser(targetURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", targetURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	return cmd.Start()
}
