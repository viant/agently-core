package auth

import (
	"fmt"
	"strings"

	iauth "github.com/viant/agently-core/internal/auth"
)

// Config defines global authentication settings for public embedders.
type Config struct {
	Enabled                 bool     `yaml:"enabled" json:"enabled"`
	CookieName              string   `yaml:"cookieName" json:"cookieName"`
	SessionTTLHours         int      `yaml:"sessionTTLHours,omitempty" json:"sessionTTLHours,omitempty"`
	TokenRefreshLeadMinutes int      `yaml:"tokenRefreshLeadMinutes,omitempty" json:"tokenRefreshLeadMinutes,omitempty"`
	DefaultUsername         string   `yaml:"defaultUsername" json:"defaultUsername"`
	IpHashKey               string   `yaml:"ipHashKey" json:"ipHashKey"`
	TrustedProxies          []string `yaml:"trustedProxies" json:"trustedProxies"`
	RedirectPath            string   `yaml:"redirectPath" json:"redirectPath"`
	// WorkspaceNamespace is the immutable identifier mixed into delegated MCP
	// token storage keys. It is not a filesystem path or display name;
	// changing it requires an explicit token-key migration. Empty selects
	// "default".
	WorkspaceNamespace string `yaml:"workspaceNamespace,omitempty" json:"workspaceNamespace,omitempty"`
	// TokenEncryptionKey is the explicit (env-expandable) encryption key for
	// delegated MCP token storage. When empty, delegated storage falls back to
	// the legacy workspace OAuth client configURL-derived salt. It lets
	// JWT/local-auth workspaces (no OAuth client) use delegated MCP OAuth.
	TokenEncryptionKey string `yaml:"tokenEncryptionKey,omitempty" json:"tokenEncryptionKey,omitempty"`
	// StateEncryptionKey is the active AEAD key material for delegated MCP
	// OAuth callback state (env-expandable). When empty, the delegated token
	// encryption salt is used. StateEncryptionKeyPrevious keeps the retired
	// key decryptable for at least the state TTL plus clock skew after a
	// rotation; new state always seals with the active key.
	StateEncryptionKey         string `yaml:"stateEncryptionKey,omitempty" json:"stateEncryptionKey,omitempty"`
	StateEncryptionKeyPrevious string `yaml:"stateEncryptionKeyPrevious,omitempty" json:"stateEncryptionKeyPrevious,omitempty"`
	// MCPLinkStateTTLMinutes bounds the delegated MCP OAuth state lifetime;
	// values are clamped to the mandated 5–10 minute window (default 7).
	MCPLinkStateTTLMinutes int    `yaml:"mcpLinkStateTTLMinutes,omitempty" json:"mcpLinkStateTTLMinutes,omitempty"`
	OAuth                  *OAuth `yaml:"oauth" json:"oauth"`
	Local                  *Local `yaml:"local" json:"local"`
	JWT                    *JWT   `yaml:"jwt,omitempty" json:"jwt,omitempty"`
}

// DelegatedTokenEncryptionSalt returns the salt encrypting delegated MCP
// token rows: the explicit auth.tokenEncryptionKey when configured, otherwise
// the legacy workspace OAuth client configURL. Empty means delegated storage
// has no usable key; delegated configuration must then fail loudly rather
// than silently disable.
func (c *Config) DelegatedTokenEncryptionSalt() string {
	if c == nil {
		return ""
	}
	if key := strings.TrimSpace(c.TokenEncryptionKey); key != "" {
		return key
	}
	if c.OAuth != nil && c.OAuth.Client != nil {
		return strings.TrimSpace(c.OAuth.Client.ConfigURL)
	}
	return ""
}

type OAuth struct {
	Mode          string       `yaml:"mode" json:"mode"`
	Name          string       `yaml:"name" json:"name"`
	Label         string       `yaml:"label" json:"label"`
	UsePopupLogin bool         `yaml:"usePopupLogin,omitempty" json:"usePopupLogin,omitempty"`
	Client        *OAuthClient `yaml:"client" json:"client"`
}

type OAuthClient struct {
	ConfigURL      string   `yaml:"configURL" json:"configURL"`
	DiscoveryURL   string   `yaml:"discoveryURL" json:"discoveryURL"`
	JWKSURL        string   `yaml:"jwksURL" json:"jwksURL"`
	RedirectURI    string   `yaml:"redirectURI" json:"redirectURI"`
	RedirectURIs   []string `yaml:"redirectURIs" json:"redirectURIs"`
	ClientID       string   `yaml:"clientID" json:"clientID"`
	Scopes         []string `yaml:"scopes" json:"scopes"`
	WebUIScopes    []string `yaml:"webUIScopes,omitempty" json:"webUIScopes,omitempty"`
	MobileUIScopes []string `yaml:"mobileUIScopes,omitempty" json:"mobileUIScopes,omitempty"`
	CLIScopes      []string `yaml:"cliScopes,omitempty" json:"cliScopes,omitempty"`
	Issuer         string   `yaml:"issuer" json:"issuer"`
	Audiences      []string `yaml:"audiences" json:"audiences"`
}

type Local struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

type JWT struct {
	Enabled       bool     `yaml:"enabled" json:"enabled"`
	RSA           []string `yaml:"rsa,omitempty" json:"rsa,omitempty"`
	HMAC          string   `yaml:"hmac,omitempty" json:"hmac,omitempty"`
	CertURL       string   `yaml:"certURL,omitempty" json:"certURL,omitempty"`
	RSAPrivateKey string   `yaml:"rsaPrivateKey,omitempty" json:"rsaPrivateKey,omitempty"`
}

// UserInfo carries minimal identity extracted from auth context or JWT claims.
type UserInfo struct {
	Subject string
	Email   string
}

func toInternalUserInfo(info *UserInfo) *iauth.UserInfo {
	if info == nil {
		return nil
	}
	return &iauth.UserInfo{
		Subject: info.Subject,
		Email:   info.Email,
	}
}

func fromInternalUserInfo(info *iauth.UserInfo) *UserInfo {
	if info == nil {
		return nil
	}
	return &UserInfo{
		Subject: info.Subject,
		Email:   info.Email,
	}
}

func (c *Config) Validate() error {
	if c == nil || !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.IpHashKey) == "" {
		return fmt.Errorf("auth.ipHashKey is required when auth is enabled")
	}
	needsCookie := c.Local != nil && c.Local.Enabled
	if c.OAuth != nil {
		mode := strings.ToLower(strings.TrimSpace(c.OAuth.Mode))
		if mode == "bff" || mode == "mixed" || mode == "local" {
			needsCookie = true
		}
	}
	if needsCookie && strings.TrimSpace(c.CookieName) == "" {
		return fmt.Errorf("auth.cookieName is required for cookie-based auth")
	}
	return nil
}

func (c *Config) IsLocalAuth() bool {
	if c == nil || !c.Enabled {
		return false
	}
	if c.Local == nil || !c.Local.Enabled {
		return false
	}
	if c.OAuth == nil {
		return true
	}
	m := strings.ToLower(strings.TrimSpace(c.OAuth.Mode))
	return m == "" || m == "local"
}

func (c *Config) IsCookieAccepted() bool {
	if c == nil || !c.Enabled {
		return false
	}
	if c.Local != nil && c.Local.Enabled {
		return true
	}
	if c.OAuth != nil {
		m := strings.ToLower(strings.TrimSpace(c.OAuth.Mode))
		if m == "bff" || m == "mixed" || m == "local" {
			return true
		}
	}
	return false
}

func (c *Config) IsBearerAccepted() bool {
	if c == nil || !c.Enabled {
		return false
	}
	if c.JWT != nil && c.JWT.Enabled {
		return true
	}
	if c.OAuth == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(c.OAuth.Mode)) {
	case "spa", "bearer", "oidc", "mixed":
		return true
	}
	return false
}

func (c *Config) IsJWTAuth() bool {
	return c != nil && c.Enabled && c.JWT != nil && c.JWT.Enabled
}
