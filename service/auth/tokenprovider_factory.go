package auth

import (
	"strings"

	token "github.com/viant/agently-core/internal/auth/token"
	"github.com/viant/datly"
)

// NewCreatedByUserTokenProvider returns a store-backed token provider suitable
// for scheduler created_by_user_id auth restoration. It only restores tokens
// already persisted in user_oauth_token; it does not enable any broader auth flow.
func NewCreatedByUserTokenProvider(cfg *Config, dao *datly.Service) token.Provider {
	if dao == nil || cfg == nil || cfg.OAuth == nil || cfg.OAuth.Client == nil {
		return nil
	}
	configURL := strings.TrimSpace(cfg.OAuth.Client.ConfigURL)
	if configURL == "" {
		return nil
	}
	store := NewTokenStoreDAO(dao, configURL)
	return token.NewManager(
		token.WithTokenStore(NewTokenStoreAdapter(store)),
	)
}
