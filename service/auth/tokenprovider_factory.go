package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	token "github.com/viant/agently-core/internal/auth/token"
	"github.com/viant/agently-core/internal/authlog"
	"github.com/viant/datly"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
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
	users := NewDatlyUserService(dao)
	canonicalStore := &canonicalTokenStore{inner: store, users: users}
	broker := &oauthRefreshBroker{
		configURL: configURL,
		store:     canonicalStore,
		client:    cfg.OAuth.Client,
	}
	return token.NewManager(
		token.WithBroker(broker),
		token.WithTokenStore(newTokenStoreAdapter(store, users, cfg.OAuth.Client)),
	)
}

type oauthRefreshBroker struct {
	configURL string
	store     TokenStore
	client    *OAuthClient
}

func (b *oauthRefreshBroker) Refresh(ctx context.Context, key token.Key, refreshToken string) (*scyauth.Token, error) {
	started := time.Now()
	oauthCfg, err := loadOAuthClientConfig(ctx, b.configURL)
	if err != nil || oauthCfg == nil {
		if err == nil {
			err = fmt.Errorf("oauth config unavailable")
		}
		return nil, err
	}
	base := &oauth2.Token{RefreshToken: strings.TrimSpace(refreshToken)}
	var stored *OAuthToken
	if b.store != nil {
		stored, err = b.store.Get(ctx, strings.TrimSpace(key.Subject), strings.TrimSpace(key.Provider))
		if err != nil {
			return nil, err
		}
	}
	scopes := oauthRefreshScopesForClient(nil, stored, b.client)
	resource := oauthRefreshResource(nil, stored, oauthCfg.ClientID)
	refreshed, err := refreshOAuthToken(ctx, cloneOAuthConfigWithScopes(oauthCfg, scopes), base, scopes, resource)
	if err != nil {
		if isPermanentRefreshError(err) {
			return nil, token.NewRefreshInvalidGrantError(err)
		}
		return nil, err
	}
	if refreshed == nil {
		return nil, nil
	}
	if strings.TrimSpace(refreshed.RefreshToken) == "" {
		refreshed.RefreshToken = strings.TrimSpace(refreshToken)
	}
	previousIDToken := ""
	if stored != nil {
		previousIDToken = strings.TrimSpace(stored.IDToken)
	}
	idToken := refreshedOAuthIDToken(refreshed, previousIDToken)
	if err := validateRefreshedOAuthScopes(b.client, scopes, refreshed, idToken); err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "scheduler_broker_scope_validate",
			UserID:         strings.TrimSpace(key.Subject),
			Provider:       strings.TrimSpace(key.Provider),
			Endpoint:       oauthCfg.Endpoint.TokenURL,
			Classification: "scope_validation",
			Action:         "preserve_cooldown_no_inject",
			Elapsed:        time.Since(started),
			Err:            err,
		})
		return nil, err
	}
	return &scyauth.Token{
		Token:   *refreshed,
		IDToken: idToken,
	}, nil
}

func (b *oauthRefreshBroker) Exchange(ctx context.Context, key token.Key, code string) (*scyauth.Token, error) {
	return nil, nil
}
