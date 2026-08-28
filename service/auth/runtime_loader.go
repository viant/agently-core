package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	token "github.com/viant/agently-core/internal/auth/token"
	"github.com/viant/agently-core/internal/authlog"
	"github.com/viant/agently-core/internal/logx"
	sessionread "github.com/viant/agently-core/pkg/agently/user/session"
	sessiondelete "github.com/viant/agently-core/pkg/agently/user/session/delete"
	sessionwrite "github.com/viant/agently-core/pkg/agently/user/session/write"
	wscfg "github.com/viant/agently-core/workspace/config"
	"github.com/viant/datly"
	"github.com/viant/scy"
	vcfg "github.com/viant/scy/auth/jwt/verifier"
	authmeta "github.com/viant/scy/auth/metadata"
	"golang.org/x/oauth2"
)

const oauthMetadataTimeout = 5 * time.Second

var defaultOAuthMetadataHTTPClient = &http.Client{Timeout: oauthMetadataTimeout}

func NewRuntime(ctx context.Context, workspaceRoot string, dao *datly.Service) (*Runtime, error) {
	cfg, err := LoadConfig(workspaceRoot)
	if err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "runtime_config_load",
			Classification: "config",
			Action:         "failed",
			Err:            err,
		})
		return nil, err
	}
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}

	if strings.TrimSpace(cfg.CookieName) == "" {
		cfg.CookieName = "agently_session"
	}
	if cfg.SessionTTLHours <= 0 {
		cfg.SessionTTLHours = 24 * 7
	}
	if strings.TrimSpace(cfg.RedirectPath) == "" {
		cfg.RedirectPath = "/v1/api/auth/oauth/callback"
	}
	if strings.TrimSpace(cfg.IpHashKey) == "" {
		cfg.IpHashKey = "agently-app-dev-ip-hash-key"
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid auth configuration: %w", err)
	}

	var sessionStore SessionStore
	if dao != nil {
		if err := sessionread.DefineSessionComponent(ctx, dao); err != nil {
			return nil, fmt.Errorf("failed to register session read component: %w", err)
		}
		if _, err := sessiondelete.DefineComponent(ctx, dao); err != nil {
			return nil, fmt.Errorf("failed to register session delete component: %w", err)
		}
		if _, err := sessionwrite.DefineComponent(ctx, dao); err != nil {
			return nil, fmt.Errorf("failed to register session write component: %w", err)
		}
		sessionStore = NewSessionStoreDAO(dao)
	}
	sessions := NewManager(time.Duration(cfg.SessionTTLHours)*time.Hour, sessionStore)
	opts := make([]HandlerOption, 0, 2)

	var tokenStore TokenStore
	if dao != nil {
		configURL := ""
		if cfg.OAuth != nil && cfg.OAuth.Client != nil {
			configURL = strings.TrimSpace(cfg.OAuth.Client.ConfigURL)
		}
		// The delegated salt lets the store decrypt delegated (mcp:v1) rows
		// even when they are keyed differently from workspace rows, and it
		// enables the token store for JWT/local-auth workspaces that have no
		// OAuth client but configure auth.tokenEncryptionKey.
		delegatedSalt := cfg.DelegatedTokenEncryptionSalt()
		if configURL != "" || delegatedSalt != "" {
			tokenStore = NewTokenStoreDAO(dao, firstNonEmpty(configURL, delegatedSalt), WithDelegatedSalt(delegatedSalt))
			opts = append(opts, WithTokenStore(tokenStore))
			logx.Debugf("auth-token", "runtime token store enabled provider=%q", firstNonEmpty(strings.TrimSpace(configuredOAuthProvider(cfg)), "oauth"))
		}
	}
	var users UserService
	if dao != nil {
		users = NewDatlyUserService(dao)
	}

	var jwtVerifier *vcfg.Service
	var jwtService *JWTService
	if cfg.JWT != nil && cfg.JWT.Enabled {
		verifyCfg := &vcfg.Config{CertURL: strings.TrimSpace(cfg.JWT.CertURL)}
		for _, rsaPath := range cfg.JWT.RSA {
			trimmed := strings.TrimSpace(rsaPath)
			if trimmed == "" {
				continue
			}
			verifyCfg.RSA = append(verifyCfg.RSA, scy.NewResource("", trimmed, ""))
		}
		if hmac := strings.TrimSpace(cfg.JWT.HMAC); hmac != "" {
			verifyCfg.HMAC = scy.NewResource("", hmac, "")
		}
		v := vcfg.New(verifyCfg)
		if err := v.Init(ctx); err != nil {
			return nil, fmt.Errorf("unable to initialize jwt verifier: %w", err)
		}
		jwtVerifier = v
		jwtService = NewJWTService(cfg.JWT)
		if err := jwtService.Init(ctx); err != nil {
			return nil, fmt.Errorf("unable to initialize jwt service: %w", err)
		}
	} else if verifyCfg, err := oauthVerifierConfig(ctx, cfg); err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "oauth_verifier_config",
			Provider:       configuredOAuthProvider(cfg),
			Classification: "config",
			Action:         "preserve",
			Err:            err,
		})
	} else if verifyCfg != nil {
		v := vcfg.New(verifyCfg)
		if err := v.Init(ctx); err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "oauth_verifier_init",
				Provider:       configuredOAuthProvider(cfg),
				Classification: "config",
				Action:         "preserve",
				Err:            err,
			})
		} else {
			jwtVerifier = v
			logx.Debugf("auth-token", "oauth jwt verifier enabled")
		}
	}

	runtime := &Runtime{
		cfg:         cfg,
		sessions:    sessions,
		jwtMintKey:  strings.TrimSpace(jwtPrivateKeyPath(cfg)),
		jwtVerifier: jwtVerifier,
		jwtService:  jwtService,
		handlerOpts: opts,
		ext:         newAuthExtension(cfg, sessions, strings.TrimSpace(jwtPrivateKeyPath(cfg)), tokenStore, users),
	}
	// Enable delegated-row routing for the background watcher: without this,
	// rows stored under delegated provider keys are skipped without mutation.
	if delegated := NewDelegatedMCPAuth(cfg, dao); delegated != nil {
		if users != nil {
			delegated.SetUserLookup(users)
		}
		runtime.delegatedRefresher = delegated.TokenRefresher()
		// Delegated MCP OAuth link endpoints: register the oauth_link_state
		// Datly components and expose the state store through the narrow
		// OAuthStateStore adapter. HTTP handlers never touch database/sql.
		if dao != nil && runtime.ext != nil {
			if err := DefineOAuthLinkStateComponents(ctx, dao); err != nil {
				return nil, err
			}
			if states := NewOAuthStateStoreDatly(dao); states != nil {
				runtime.ext.mcpLink = newMCPLinkService(cfg, delegated, states, nil, users)
			}
		}
	}
	runtime.stopRefresh = runtime.startTokenRefreshWatcher(ctx)
	return runtime, nil
}

func configuredOAuthProvider(cfg *Config) string {
	if cfg == nil || cfg.OAuth == nil {
		return "oauth"
	}
	return firstNonEmpty(strings.TrimSpace(cfg.OAuth.Name), "oauth")
}

// LoadConfig reads `<workspaceRoot>/config.yaml` and decodes the
// `auth:` section into a *Config. Returns (nil, nil) when no auth
// section is present or all fields are zero.
//
// Callers that have already loaded the workspace `Root` (e.g. the
// executor bootstrap that also reads `default:` / `mcpServer`) should
// prefer DecodeConfigFromRoot to avoid re-reading and re-parsing
// config.yaml.
func LoadConfig(workspaceRoot string) (*Config, error) {
	cfg, err := wscfg.Load(workspaceRoot)
	if err != nil {
		return nil, err
	}
	return DecodeConfigFromRoot(cfg)
}

// DecodeConfigFromRoot decodes the `auth:` section from an already-
// loaded workspace Root. Returns (nil, nil) when the root is nil or
// the auth section is empty — callers treat that as "auth disabled".
// Env-template expansion and the "effectively empty" check live here
// so the two entry points (LoadConfig / DecodeConfigFromRoot) behave
// identically.
func DecodeConfigFromRoot(root *wscfg.Root) (*Config, error) {
	if root == nil {
		return nil, nil
	}
	ret := &Config{}
	if err := root.DecodeAuth(ret); err != nil {
		return nil, fmt.Errorf("decode auth config: %w", err)
	}
	expandAuthEnvTemplates(ret)
	if ret.Enabled || ret.CookieName != "" || ret.Local != nil || ret.OAuth != nil || ret.JWT != nil {
		return ret, nil
	}
	return nil, nil
}

// LoadWorkspaceConfig is a thin compatibility shim over LoadConfig.
//
// Deprecated: use LoadConfig for new code, or DecodeConfigFromRoot when
// the workspace Root is already in hand. Kept so external callers that
// still reference the old name keep compiling; will be removed once
// the tree is fully migrated.
func LoadWorkspaceConfig(workspaceRoot string) (*Config, error) {
	return LoadConfig(workspaceRoot)
}

var authEnvTemplate = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

func expandAuthEnvTemplates(cfg *Config) {
	if cfg == nil {
		return
	}
	cfg.CookieName = expandAuthEnvString(cfg.CookieName)
	cfg.DefaultUsername = expandAuthEnvString(cfg.DefaultUsername)
	cfg.IpHashKey = expandAuthEnvString(cfg.IpHashKey)
	cfg.RedirectPath = expandAuthEnvString(cfg.RedirectPath)
	cfg.TokenEncryptionKey = expandAuthEnvString(cfg.TokenEncryptionKey)
	cfg.StateEncryptionKey = expandAuthEnvString(cfg.StateEncryptionKey)
	cfg.StateEncryptionKeyPrevious = expandAuthEnvString(cfg.StateEncryptionKeyPrevious)
	for i := range cfg.TrustedProxies {
		cfg.TrustedProxies[i] = expandAuthEnvString(cfg.TrustedProxies[i])
	}
	if cfg.OAuth != nil {
		cfg.OAuth.Mode = expandAuthEnvString(cfg.OAuth.Mode)
		cfg.OAuth.Name = expandAuthEnvString(cfg.OAuth.Name)
		cfg.OAuth.Label = expandAuthEnvString(cfg.OAuth.Label)
		if cfg.OAuth.Client != nil {
			cfg.OAuth.Client.ConfigURL = expandAuthEnvString(cfg.OAuth.Client.ConfigURL)
			cfg.OAuth.Client.DiscoveryURL = expandAuthEnvString(cfg.OAuth.Client.DiscoveryURL)
			cfg.OAuth.Client.JWKSURL = expandAuthEnvString(cfg.OAuth.Client.JWKSURL)
			cfg.OAuth.Client.RedirectURI = expandAuthEnvString(cfg.OAuth.Client.RedirectURI)
			for i := range cfg.OAuth.Client.RedirectURIs {
				cfg.OAuth.Client.RedirectURIs[i] = expandAuthEnvString(cfg.OAuth.Client.RedirectURIs[i])
			}
			cfg.OAuth.Client.ClientID = expandAuthEnvString(cfg.OAuth.Client.ClientID)
			cfg.OAuth.Client.Issuer = expandAuthEnvString(cfg.OAuth.Client.Issuer)
			for i := range cfg.OAuth.Client.Scopes {
				cfg.OAuth.Client.Scopes[i] = expandAuthEnvString(cfg.OAuth.Client.Scopes[i])
			}
			for i := range cfg.OAuth.Client.WebUIScopes {
				cfg.OAuth.Client.WebUIScopes[i] = expandAuthEnvString(cfg.OAuth.Client.WebUIScopes[i])
			}
			for i := range cfg.OAuth.Client.MobileUIScopes {
				cfg.OAuth.Client.MobileUIScopes[i] = expandAuthEnvString(cfg.OAuth.Client.MobileUIScopes[i])
			}
			for i := range cfg.OAuth.Client.CLIScopes {
				cfg.OAuth.Client.CLIScopes[i] = expandAuthEnvString(cfg.OAuth.Client.CLIScopes[i])
			}
			for i := range cfg.OAuth.Client.Audiences {
				cfg.OAuth.Client.Audiences[i] = expandAuthEnvString(cfg.OAuth.Client.Audiences[i])
			}
		}
	}
	if cfg.JWT != nil {
		cfg.JWT.HMAC = expandAuthEnvString(cfg.JWT.HMAC)
		cfg.JWT.CertURL = expandAuthEnvString(cfg.JWT.CertURL)
		cfg.JWT.RSAPrivateKey = expandAuthEnvString(cfg.JWT.RSAPrivateKey)
		for i := range cfg.JWT.RSA {
			cfg.JWT.RSA[i] = expandAuthEnvString(cfg.JWT.RSA[i])
		}
	}
}

func expandAuthEnvString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.Contains(value, "${") {
		return value
	}
	return authEnvTemplate.ReplaceAllStringFunc(value, func(match string) string {
		parts := authEnvTemplate.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		if current, ok := os.LookupEnv(parts[1]); ok && current != "" {
			return current
		}
		if len(parts) >= 4 {
			return parts[3]
		}
		return ""
	})
}

func (r *Runtime) JWTService() *JWTService {
	if r == nil {
		return nil
	}
	return r.jwtService
}

func jwtPrivateKeyPath(cfg *Config) string {
	if cfg == nil || cfg.JWT == nil || !cfg.JWT.Enabled {
		return ""
	}
	return strings.TrimSpace(cfg.JWT.RSAPrivateKey)
}

// tokenRefreshLead returns the workspace refresh lead. The default is the
// shared 15-minute lead applied to every provider; provider-aware code
// computes the effective (lifetime-clamped) lead before invoking a broker.
func (c *Config) tokenRefreshLead() time.Duration {
	if c == nil || c.TokenRefreshLeadMinutes <= 0 {
		return token.DefaultRefreshLead
	}
	return time.Duration(c.TokenRefreshLeadMinutes) * time.Minute
}

func oauthVerifierConfig(ctx context.Context, cfg *Config) (*vcfg.Config, error) {
	if cfg == nil || cfg.OAuth == nil || cfg.OAuth.Client == nil {
		return nil, nil
	}
	client := cfg.OAuth.Client
	if certURL := strings.TrimSpace(client.JWKSURL); certURL != "" {
		return &vcfg.Config{CertURL: certURL}, nil
	}
	if discoveryURL := strings.TrimSpace(client.DiscoveryURL); discoveryURL != "" {
		jwksURL, err := fetchOpenIDJWKSURL(ctx, discoveryURL)
		if err != nil {
			return nil, err
		}
		return &vcfg.Config{CertURL: jwksURL}, nil
	}
	if issuer := strings.TrimSpace(client.Issuer); issuer != "" {
		jwksURL, err := fetchIssuerJWKSURL(ctx, issuer)
		if err != nil {
			return nil, err
		}
		return &vcfg.Config{CertURL: jwksURL}, nil
	}
	if configURL := strings.TrimSpace(client.ConfigURL); configURL != "" {
		oauthCfg, err := loadOAuthClientConfig(ctx, configURL)
		if err != nil {
			return nil, err
		}
		for _, issuer := range issuerCandidatesFromAuthURL(oauthCfg.Endpoint.AuthURL) {
			jwksURL, err := fetchIssuerJWKSURL(ctx, issuer)
			if err != nil {
				continue
			}
			return &vcfg.Config{CertURL: jwksURL}, nil
		}
	}
	return nil, nil
}

func fetchIssuerJWKSURL(ctx context.Context, issuer string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if meta, err := authmeta.FetchAuthorizationServerMetadata(ctx, issuer, oauthMetadataHTTPClient(ctx)); err == nil && meta != nil && strings.TrimSpace(meta.JSONWebKeySetURI) != "" {
		return strings.TrimSpace(meta.JSONWebKeySetURI), nil
	}
	discoveryURL, err := openIDDiscoveryURL(issuer)
	if err != nil {
		return "", err
	}
	return fetchOpenIDJWKSURL(ctx, discoveryURL)
}

func fetchOpenIDJWKSURL(ctx context.Context, discoveryURL string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(discoveryURL), nil)
	if err != nil {
		return "", err
	}
	resp, err := oauthMetadataHTTPClient(ctx).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openid discovery returned status %d", resp.StatusCode)
	}
	var payload authmeta.OpenIDConfiguration
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	jwksURL := strings.TrimSpace(payload.JwksURI)
	if jwksURL == "" {
		return "", fmt.Errorf("openid discovery missing jwks_uri")
	}
	return jwksURL, nil
}

// oauthMetadataHTTPClient keeps verifier discovery bounded during startup.
// A caller-supplied OAuth client remains authoritative for platform policy and
// deterministic tests.
func oauthMetadataHTTPClient(ctx context.Context) *http.Client {
	if ctx != nil {
		if ctxClient, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok && ctxClient != nil {
			return ctxClient
		}
	}
	return defaultOAuthMetadataHTTPClient
}

func issuerCandidatesFromAuthURL(authURL string) []string {
	authURL = strings.TrimSpace(authURL)
	if authURL == "" {
		return nil
	}
	parsed, err := url.Parse(authURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil
	}
	base := *parsed
	base.Path = ""
	base.RawQuery = ""
	base.Fragment = ""
	root := strings.TrimRight(base.String(), "/")
	dir := path.Dir(parsed.Path)
	var candidates []string
	appendUnique := func(value string) {
		value = strings.TrimRight(strings.TrimSpace(value), "/")
		if value == "" {
			return
		}
		for _, candidate := range candidates {
			if candidate == value {
				return
			}
		}
		candidates = append(candidates, value)
	}
	appendUnique(root)
	switch dir {
	case ".", "/":
		appendUnique(root)
	default:
		parsed.RawQuery = ""
		parsed.Fragment = ""
		parsed.Path = dir
		appendUnique(parsed.String())
	}
	return candidates
}

func openIDDiscoveryURL(issuer string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(issuer))
	if err != nil {
		return "", err
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/.well-known/openid-configuration"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
