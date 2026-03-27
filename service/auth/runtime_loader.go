package auth

import (
	"context"
	"fmt"
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
	cfg, err := LoadWorkspaceConfig(workspaceRoot)
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
		}
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
		ext:         newAuthExtension(cfg, sessions, strings.TrimSpace(jwtPrivateKeyPath(cfg)), tokenStore),
	}
	runtime.stopRefresh = runtime.startTokenRefreshWatcher(ctx)
	return runtime, nil
}

func LoadWorkspaceConfig(workspaceRoot string) (*Config, error) {
	cfg, err := wscfg.Load(workspaceRoot)
	if err != nil || cfg == nil {
		return nil, err
	}
	ret := &Config{}
	if err := cfg.DecodeAuth(ret); err != nil {
		return nil, fmt.Errorf("decode auth config: %w", err)
	}
	if ret.Enabled || ret.CookieName != "" || ret.Local != nil || ret.OAuth != nil || ret.JWT != nil {
		return ret, nil
	}
	return nil, nil
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
