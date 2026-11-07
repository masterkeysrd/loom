package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/oauth2"
)

// ErrAuthRequired is returned when authentication credentials are required but missing.
var ErrAuthRequired = errors.New("auth: authentication required")

type errTokenSource struct {
	err error
}

func (e *errTokenSource) Token() (*oauth2.Token, error) {
	return nil, e.err
}

// StaticTokenSource wraps a static token string in an [oauth2.TokenSource].
// If token is empty, its Token method returns [ErrAuthRequired].
func StaticTokenSource(token string) oauth2.TokenSource {
	if token == "" {
		return &errTokenSource{err: ErrAuthRequired}
	}
	return oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: token,
	})
}

type envTokenSource struct {
	envVars []string
}

func (e *envTokenSource) Token() (*oauth2.Token, error) {
	for _, envVar := range e.envVars {
		if val := os.Getenv(envVar); val != "" {
			return &oauth2.Token{AccessToken: val}, nil
		}
	}
	return nil, ErrAuthRequired
}

// EnvTokenSource returns an [oauth2.TokenSource] that checks each environment variable
// in envVars in order when Token() is called. If none of the variables are non-empty,
// [ErrAuthRequired] is returned.
func EnvTokenSource(envVars ...string) oauth2.TokenSource {
	vars := make([]string, len(envVars))
	copy(vars, envVars)
	return &envTokenSource{envVars: vars}
}

// PersistentConfig defines the callbacks used by [NewPersistentTokenSource] to manage
// token loading, saving, and re-authentication.
type PersistentConfig struct {
	// Load loads the initial token from the developer's storage (optional).
	Load func() (*oauth2.Token, error)
	// Save persists a newly acquired or refreshed token to the developer's storage (optional).
	Save func(*oauth2.Token) error
	// Login acquires a new token when no token is loaded or when refreshing an expired token fails (optional).
	Login func(ctx context.Context) (*oauth2.Token, error)
}

type persistentTokenSource struct {
	ctx     context.Context
	cfg     *oauth2.Config
	pcfg    PersistentConfig
	mu      sync.Mutex
	base    oauth2.TokenSource
	lastTok *oauth2.Token
}

// NewPersistentTokenSource creates a thread-safe [oauth2.TokenSource] that loads tokens
// from user-defined storage, automatically refreshes them, invokes a Save callback when
// tokens change, and falls back to an interactive or headless Login callback if tokens
// are absent or expired beyond recovery.
func NewPersistentTokenSource(ctx context.Context, cfg *oauth2.Config, pcfg PersistentConfig) oauth2.TokenSource {
	if ctx == nil {
		ctx = context.Background()
	}
	return &persistentTokenSource{
		ctx:  ctx,
		cfg:  cfg,
		pcfg: pcfg,
	}
}

func (s *persistentTokenSource) initBase(tok *oauth2.Token) {
	s.lastTok = tok
	if s.cfg != nil {
		s.base = s.cfg.TokenSource(s.ctx, tok)
		return
	}
	s.base = oauth2.StaticTokenSource(tok)
}

func (s *persistentTokenSource) save(tok *oauth2.Token) error {
	if s.pcfg.Save == nil {
		return nil
	}
	return s.pcfg.Save(tok)
}

func (s *persistentTokenSource) loginAndSave() (*oauth2.Token, error) {
	tok, err := s.pcfg.Login(s.ctx)
	if err != nil {
		return nil, err
	}
	if tok == nil {
		return nil, errors.New("auth: login returned nil token")
	}
	if err := s.save(tok); err != nil {
		return nil, err
	}
	s.initBase(tok)
	return tok, nil
}

func (s *persistentTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.base == nil && s.pcfg.Load != nil {
		if tok, err := s.pcfg.Load(); err == nil && tok != nil {
			s.initBase(tok)
		}
	}

	if s.base == nil {
		if s.pcfg.Login == nil {
			return nil, ErrAuthRequired
		}
		return s.loginAndSave()
	}

	tok, err := s.base.Token()
	if err != nil {
		if s.pcfg.Login == nil {
			return nil, err
		}
		return s.loginAndSave()
	}

	if s.lastTok == nil || s.lastTok.AccessToken != tok.AccessToken || s.lastTok.RefreshToken != tok.RefreshToken {
		s.lastTok = tok
		if err := s.save(tok); err != nil {
			return nil, err
		}
	}

	return tok, nil
}

// LoadTokenFile reads and unmarshals an [oauth2.Token] from the specified JSON file path.
func LoadTokenFile(path string) (*oauth2.Token, error) {
	if path == "" {
		return nil, errors.New("auth: token file path cannot be empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("auth: parse token file %q: %w", path, err)
	}
	return &tok, nil
}

// SaveTokenFile writes tok as formatted JSON to path with mode 0600, creating any
// parent directories with mode 0700 if necessary.
func SaveTokenFile(path string, tok *oauth2.Token) error {
	if path == "" {
		return errors.New("auth: token file path cannot be empty")
	}
	if tok == nil {
		return errors.New("auth: token cannot be nil")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("auth: create token directory %q: %w", dir, err)
	}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: marshal token: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("auth: write token file %q: %w", path, err)
	}
	return nil
}
