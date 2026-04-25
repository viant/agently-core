package auth

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	sessionread "github.com/viant/agently-core/pkg/agently/user/session"
	sessiondelete "github.com/viant/agently-core/pkg/agently/user/session/delete"
	sessionwrite "github.com/viant/agently-core/pkg/agently/user/session/write"
	wscfg "github.com/viant/agently-core/workspace/config"
	"github.com/viant/datly"
	"github.com/viant/scy"
	vcfg "github.com/viant/scy/auth/jwt/verifier"
)

func NewRuntime(ctx context.Context, workspaceRoot string, dao *datly.Service) (*Runtime, error) {
	cfg, err := LoadConfig(workspaceRoot)
	if err != nil {
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
	if dao != nil && cfg.OAuth != nil && cfg.OAuth.Client != nil {
		if configURL := strings.TrimSpace(cfg.OAuth.Client.ConfigURL); configURL != "" {
			tokenStore = NewTokenStoreDAO(dao, configURL)
			opts = append(opts, WithTokenStore(tokenStore))
			log.Printf("[auth-oauth] runtime token store enabled provider=%q config_url_set=%t dao=%t", firstNonEmpty(strings.TrimSpace(cfg.OAuth.Name), "oauth"), true, dao != nil)
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
	runtime.stopRefresh = runtime.startTokenRefreshWatcher(ctx)
	return runtime, nil
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
			cfg.OAuth.Client.ClientID = expandAuthEnvString(cfg.OAuth.Client.ClientID)
			cfg.OAuth.Client.Issuer = expandAuthEnvString(cfg.OAuth.Client.Issuer)
			for i := range cfg.OAuth.Client.Scopes {
				cfg.OAuth.Client.Scopes[i] = expandAuthEnvString(cfg.OAuth.Client.Scopes[i])
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

func (c *Config) tokenRefreshLead() time.Duration {
	if c == nil || c.TokenRefreshLeadMinutes <= 0 {
		return 30 * time.Minute
	}
	return time.Duration(c.TokenRefreshLeadMinutes) * time.Minute
}
