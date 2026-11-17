package loomantigravity

import (
	"github.com/masterkeysrd/loom/providers/antigravity/sdk"
	"golang.org/x/oauth2"
)

const (
	// DefaultAntigravityClientID is the OAuth2 Client ID for Antigravity developer tools.
	DefaultAntigravityClientID = sdk.DefaultAntigravityClientID

	// DefaultCloudCodeClientID is an alias for DefaultAntigravityClientID.
	DefaultCloudCodeClientID = sdk.DefaultCloudCodeClientID

	// DefaultOAuthRedirectPort is the default local port for the OAuth callback listener.
	DefaultOAuthRedirectPort = sdk.DefaultOAuthRedirectPort
)

// DefaultAntigravityClientSecret returns the default client secret decoded at runtime.
func DefaultAntigravityClientSecret() string {
	return sdk.DefaultAntigravityClientSecret()
}

// DefaultScopes returns the standard OAuth 2.0 scopes required by Cloud Code / Antigravity endpoints.
func DefaultScopes() []string {
	return sdk.DefaultScopes()
}

// OAuthOptions defines parameters used to build an [oauth2.Config] for Antigravity.
// Type alias to [sdk.OAuthOptions].
type OAuthOptions = sdk.OAuthOptions

// NewOAuthConfig creates an [oauth2.Config] configured for Antigravity Google OAuth.
// Any omitted fields in opts fall back to defaults or environment variables.
func NewOAuthConfig(opts OAuthOptions) *oauth2.Config {
	return sdk.NewOAuthConfig(opts)
}
