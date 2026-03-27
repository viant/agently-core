package scheduler

import (
	"strings"
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
		return cloneUserCredAuthConfig(s.userCredAuthCfg)
	}
	if s.authCfg == nil || s.authCfg.OAuth == nil || s.authCfg.OAuth.Client == nil {
		return nil
	}
	return &UserCredAuthConfig{
		Mode:            strings.TrimSpace(s.authCfg.OAuth.Mode),
		ClientConfigURL: strings.TrimSpace(s.authCfg.OAuth.Client.ConfigURL),
		Scopes:          append([]string(nil), s.authCfg.OAuth.Client.Scopes...),
	}
}
