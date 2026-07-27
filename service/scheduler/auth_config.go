package scheduler

import (
	"strings"

	svcauth "github.com/viant/agently-core/service/auth"
)

// UserCredAuthConfig contains the subset of auth settings required for legacy
// scheduler user_cred_url OOB authorization.
type UserCredAuthConfig struct {
	Mode            string
	ClientConfigURL string
	Scopes          []string
}

// WithUserCredAuthConfig sets public scheduler auth configuration used by
// legacy user_cred_url OOB authorization.
func WithUserCredAuthConfig(cfg *UserCredAuthConfig) Option {
	cloned := cloneUserCredAuthConfig(cfg)
	return func(s *Service) { s.userCredAuthCfg = cloned }
}

func cloneUserCredAuthConfig(cfg *UserCredAuthConfig) *UserCredAuthConfig {
	if cfg == nil {
		return nil
	}
	return &UserCredAuthConfig{
		Mode:            strings.TrimSpace(cfg.Mode),
		ClientConfigURL: strings.TrimSpace(cfg.ClientConfigURL),
		Scopes:          append([]string(nil), cfg.Scopes...),
	}
}

func (s *Service) resolveUserCredAuthConfig() *UserCredAuthConfig {
	if s == nil {
		return nil
	}
	if s.userCredAuthCfg != nil {
		result := cloneUserCredAuthConfig(s.userCredAuthCfg)
		if s.authCfg != nil && s.authCfg.OAuth != nil && s.authCfg.OAuth.Client != nil {
			result.Scopes = mergeSchedulerScopes(result.Scopes, svcauth.OAuthScopesForHeadless(s.authCfg.OAuth.Client))
		}
		return result
	}
	if s.authCfg == nil || s.authCfg.OAuth == nil || s.authCfg.OAuth.Client == nil {
		return nil
	}
	return &UserCredAuthConfig{
		Mode:            strings.TrimSpace(s.authCfg.OAuth.Mode),
		ClientConfigURL: strings.TrimSpace(s.authCfg.OAuth.Client.ConfigURL),
		Scopes:          svcauth.OAuthScopesForHeadless(s.authCfg.OAuth.Client),
	}
}

func mergeSchedulerScopes(groups ...[]string) []string {
	var result []string
	seen := map[string]bool{}
	for _, group := range groups {
		for _, scope := range group {
			scope = strings.TrimSpace(scope)
			if scope == "" || seen[scope] {
				continue
			}
			seen[scope] = true
			result = append(result, scope)
		}
	}
	return result
}
