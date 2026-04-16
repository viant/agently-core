package auth

import (
	"context"
	"strings"

	token "github.com/viant/agently-core/internal/auth/token"
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
	broker := &oauthRefreshBroker{
		configURL: configURL,
		store:     store,
	}
	return token.NewManager(
		token.WithBroker(broker),
		token.WithTokenStore(NewTokenStoreAdapter(store)),
	)
}

type oauthRefreshBroker struct {
	configURL string
	store     TokenStore
}

func (b *oauthRefreshBroker) Refresh(ctx context.Context, key token.Key, refreshToken string) (*scyauth.Token, error) {
	oauthCfg, err := loadOAuthClientConfig(ctx, b.configURL)
	if err != nil || oauthCfg == nil {
		return nil, err
	}
	base := &oauth2.Token{RefreshToken: strings.TrimSpace(refreshToken)}
	ts := oauthCfg.TokenSource(ctx, base)
	refreshed, err := ts.Token()
	if err != nil {
		return nil, err
	}
	if refreshed == nil {
		return nil, nil
	}
	if strings.TrimSpace(refreshed.RefreshToken) == "" {
		refreshed.RefreshToken = strings.TrimSpace(refreshToken)
	}
	idToken := ""
	if raw := refreshed.Extra("id_token"); raw != nil {
		if s, ok := raw.(string); ok {
			idToken = strings.TrimSpace(s)
		}
	}
	if idToken == "" && b.store != nil {
		if stored, err := b.store.Get(ctx, strings.TrimSpace(key.Subject), strings.TrimSpace(key.Provider)); err == nil && stored != nil {
			idToken = strings.TrimSpace(stored.IDToken)
		}
	}
	return &scyauth.Token{
		Token:   *refreshed,
		IDToken: idToken,
	}, nil
}

func (b *oauthRefreshBroker) Exchange(ctx context.Context, key token.Key, code string) (*scyauth.Token, error) {
	return nil, nil
}
