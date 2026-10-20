package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	token "github.com/viant/agently-core/internal/auth/token"
	"github.com/viant/agently-core/internal/authlog"
	dexec "github.com/viant/datly/exec"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

// NewCreatedByUserTokenProvider returns a store-backed token provider suitable
// for scheduler created_by_user_id auth restoration. It only restores tokens
// already persisted in user_oauth_token; it does not enable any broader auth flow.
func NewCreatedByUserTokenProvider(cfg *Config, invoker dexec.ComponentInvoker) token.Provider {
	if invoker == nil || cfg == nil || cfg.OAuth == nil || cfg.OAuth.Client == nil {
		return nil
	}
	configURL := strings.TrimSpace(cfg.OAuth.Client.ConfigURL)
	if configURL == "" {
		return nil
	}
	store := NewTokenStoreDAO(invoker, configURL,
		WithDelegatedSalt(cfg.DelegatedTokenEncryptionSalt()),
		WithPreviousSalts(cfg.OAuth.Client.ConfigURLPrevious...))
	users := NewDatlyUserService(invoker)
	canonicalStore := &canonicalTokenStore{inner: store, users: users}
	workspaceBroker := &oauthRefreshBroker{
		configURL: configURL,
		store:     canonicalStore,
		client:    cfg.OAuth.Client,
	}
	// One token.Manager serves every provider. Refreshes route by provider:
	// workspace aliases go to the workspace broker; anything else (including
	// delegated storage keys, which refresh through the delegated resolver)
	// fails with ErrNoBrokerForProvider — a routing condition the manager
	// treats as transient, never as invalid_grant, so rows are never deleted.
	broker := &token.RoutingBroker{
		Workspace: workspaceBroker,
		IsWorkspaceAlias: func(provider string) bool {
			return IsWorkspaceProviderAlias(cfg, provider)
		},
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
		authlog.Log(ctx, authlog.Event{
			Op: "scheduler_broker_config", UserID: key.Subject, Provider: key.Provider,
			Source: "scheduler_broker", Classification: "config", Action: "preserve_no_inject",
		})
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
	authlog.Log(ctx, authlog.Event{
		Op: "scheduler_broker_refresh", UserID: key.Subject, Provider: key.Provider,
		Source: "scheduler_broker", Endpoint: oauthCfg.Endpoint.TokenURL,
		Classification: "refresh", Action: "start",
	})
	refreshed, err := refreshOAuthToken(ctx, cloneOAuthConfigWithScopes(oauthCfg, scopes), base, scopes, resource)
	if err != nil {
		status, code := oauthRefreshErrorDetails(err)
		action := "preserve"
		if isPermanentRefreshError(err) {
			action = "clear_by_manager"
		}
		authlog.Log(ctx, authlog.Event{
			Op: "scheduler_broker_refresh", UserID: key.Subject, Provider: key.Provider,
			Source: "scheduler_broker", Endpoint: oauthCfg.Endpoint.TokenURL,
			HTTPStatus: status, OAuthErrorCode: code, Classification: oauthRefreshErrorClassification(err),
			Action: action, Elapsed: time.Since(started),
		})
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
	idTokenSource := "prior"
	if candidate, ok := refreshed.Extra("id_token").(string); ok && strings.TrimSpace(candidate) != "" {
		idTokenSource = "response"
	}
	authlog.Log(ctx, authlog.Event{
		Op: "scheduler_broker_response", UserID: key.Subject, Provider: key.Provider,
		Source: "scheduler_broker", RefreshRotated: fmt.Sprint(refreshed.RefreshToken != refreshToken),
		IDTokenSource: idTokenSource, Classification: "refresh", Action: "return_to_manager",
		Elapsed: time.Since(started),
	})
	if err := validateRefreshedOAuthScopes(b.client, scopes, refreshed, idToken); err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "scheduler_broker_scope_validate",
			UserID:         strings.TrimSpace(key.Subject),
			Provider:       strings.TrimSpace(key.Provider),
			Source:         "scheduler_broker",
			RefreshRotated: fmt.Sprint(refreshed.RefreshToken != refreshToken),
			IDTokenSource:  idTokenSource,
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
