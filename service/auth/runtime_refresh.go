package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/logx"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

var runtimeWorkerID = func() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s:%d", host, os.Getpid())
}()

func (r *Runtime) ensureSessionOAuthTokens(ctx context.Context, sess *Session) bool {
	if sess == nil {
		return false
	}
	if sess.Tokens != nil && (strings.TrimSpace(sess.Tokens.AccessToken) != "" || strings.TrimSpace(sess.Tokens.IDToken) != "") {
		return true
	}
	return r.tryLoadFreshTokenFromStore(ctx, sess) != nil
}

func (r *Runtime) resolveRuntimeOAuthTokenOwner(_ context.Context, sess *Session) (string, string) {
	if r == nil || r.ext == nil || sess == nil {
		return "", ""
	}
	provider := strings.TrimSpace(firstNonEmpty(sess.Provider, r.ext.oauthProviderName()))
	if provider == "" {
		return "", ""
	}
	lookupID := resolveOAuthTokenOwnerID(context.Background(), r.ext.users, provider, sess)
	if lookupID == "" {
		return "", ""
	}
	return lookupID, provider
}

func (r *Runtime) tryLoadFreshTokenFromStore(ctx context.Context, sess *Session) *scyauth.Token {
	if r.ext == nil || r.ext.tokenStore == nil || sess == nil {
		return nil
	}
	username, provider := r.resolveRuntimeOAuthTokenOwner(ctx, sess)
	if username == "" || provider == "" {
		return nil
	}
	dbTok, err := r.ext.tokenStore.Get(ctx, username, provider)
	if err != nil || dbTok == nil {
		return nil
	}
	if !dbTok.ExpiresAt.IsZero() && !dbTok.ExpiresAt.After(time.Now()) {
		return nil
	}
	if sess.Tokens != nil && !sess.Tokens.Expiry.IsZero() && !dbTok.ExpiresAt.After(sess.Tokens.Expiry) {
		return nil
	}
	result := &scyauth.Token{
		Token: oauth2.Token{
			AccessToken:  dbTok.AccessToken,
			RefreshToken: dbTok.RefreshToken,
			Expiry:       dbTok.ExpiresAt,
		},
		IDToken: dbTok.IDToken,
	}
	sess.Tokens = result
	sess.Provider = provider
	r.sessions.Put(ctx, sess)
	logx.Debugf("token-refresh", "loaded fresh token from DB user=%q provider=%q expiry=%v", username, provider, dbTok.ExpiresAt.Format(time.RFC3339))
	return result
}

func (r *Runtime) tryRefreshToken(ctx context.Context, sess *Session) *scyauth.Token {
	if sess == nil || sess.Tokens == nil || sess.Tokens.RefreshToken == "" {
		return nil
	}
	if r.ext == nil || r.ext.cfg == nil || r.ext.cfg.OAuth == nil || r.ext.cfg.OAuth.Client == nil {
		return nil
	}
	if fresh := r.tryLoadFreshTokenFromStore(ctx, sess); fresh != nil {
		return fresh
	}

	username, provider := r.resolveRuntimeOAuthTokenOwner(ctx, sess)
	if username == "" || provider == "" {
		return nil
	}
	tokenStore := r.ext.tokenStore
	if tokenStore != nil {
		_, acquired, err := tokenStore.TryAcquireRefreshLease(ctx, username, provider, runtimeWorkerID, 30*time.Second)
		if err != nil {
			logx.Warnf("token-refresh", "lease acquire error user=%q err=%v", username, err)
			return nil
		}
		if !acquired {
			time.Sleep(2 * time.Second)
			return r.tryLoadFreshTokenFromStore(ctx, sess)
		}
		defer func() {
			_ = tokenStore.ReleaseRefreshLease(ctx, username, provider, runtimeWorkerID)
		}()
	}

	oauthCfg, err := loadOAuthClientConfig(ctx, r.ext.cfg.OAuth.Client.ConfigURL)
	if err != nil || oauthCfg == nil {
		return nil
	}
	ts := oauthCfg.TokenSource(ctx, &sess.Tokens.Token)
	refreshed, err := ts.Token()
	if err != nil {
		logx.Warnf("token-refresh", "refresh failed user=%q err=%v", username, err)
		return nil
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = sess.Tokens.RefreshToken
	}
	previousIDToken := strings.TrimSpace(sess.Tokens.IDToken)
	refreshedIDToken := refreshedOAuthIDToken(refreshed, previousIDToken)
	result := &scyauth.Token{Token: *refreshed, IDToken: refreshedIDToken}
	sess.Tokens = result
	sess.Provider = provider
	r.sessions.Put(ctx, sess)
	if tokenStore != nil {
		_ = tokenStore.Put(ctx, &OAuthToken{
			Username:     username,
			Provider:     provider,
			AccessToken:  refreshed.AccessToken,
			IDToken:      refreshedIDToken,
			RefreshToken: refreshed.RefreshToken,
			ExpiresAt:    refreshed.Expiry,
		})
	}
	logx.Debugf("token-refresh", "refresh ok user=%q newExpiry=%v access_fp=%s id_fp=%s id_rotated=%v",
		username,
		refreshed.Expiry.Format(time.RFC3339),
		tokenFingerprint(refreshed.AccessToken),
		tokenFingerprint(refreshedIDToken),
		strings.TrimSpace(refreshedIDToken) != previousIDToken,
	)
	return result
}

func refreshedOAuthIDToken(refreshed *oauth2.Token, current string) string {
	if refreshed == nil {
		return strings.TrimSpace(current)
	}
	if raw := refreshed.Extra("id_token"); raw != nil {
		if token, ok := raw.(string); ok && strings.TrimSpace(token) != "" {
			return strings.TrimSpace(token)
		}
	}
	return strings.TrimSpace(current)
}

func tokenFingerprint(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "none"
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:6])
}

func (r *Runtime) startTokenRefreshWatcher(ctx context.Context) func() {
	if r == nil || r.sessions == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Second)
		r.refreshExpiringSessions(ctx)
		r.refreshTokenStore(ctx)
		lead := r.cfg.tokenRefreshLead()
		if lead <= 0 {
			lead = 30 * time.Minute
		}
		ticker := time.NewTicker(lead / 2)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.refreshExpiringSessions(ctx)
				r.refreshTokenStore(ctx)
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return func() { close(done) }
}

func (r *Runtime) refreshExpiringSessions(ctx context.Context) {
	if r == nil || r.sessions == nil {
		return
	}
	sessions := r.sessions.ActiveSessions()
	if len(sessions) == 0 {
		return
	}
	horizon := time.Now().Add(r.cfg.tokenRefreshLead())
	var checked, refreshed int
	for _, sess := range sessions {
		if sess == nil || sess.Tokens == nil || sess.Tokens.RefreshToken == "" {
			continue
		}
		checked++
		if !sess.Tokens.Expiry.IsZero() && sess.Tokens.Expiry.After(horizon) {
			continue
		}
		if r.tryRefreshToken(ctx, sess) != nil {
			refreshed++
		}
	}
	if checked > 0 {
		logx.Debugf("token-watcher", "sessions=%d checked=%d refreshed=%d", len(sessions), checked, refreshed)
	}
}

// refreshTokenStore proactively refreshes tokens stored in the persistent token
// store that are expiring soon but have no active in-memory session. This covers
// users who are idle (tab open, no requests) but whose tokens will soon expire.
func (r *Runtime) refreshTokenStore(ctx context.Context) {
	if r == nil || r.ext == nil {
		return
	}
	scanner, ok := r.ext.tokenStore.(ExpiringTokenScanner)
	if !ok || scanner == nil {
		return
	}
	horizon := time.Now().Add(r.cfg.tokenRefreshLead())
	tokens, err := scanner.ScanExpiring(ctx, horizon)
	if err != nil || len(tokens) == 0 {
		return
	}
	var refreshed int
	for _, tok := range tokens {
		if tok == nil || tok.RefreshToken == "" {
			continue
		}
		// Build a minimal session to drive the existing refresh path.
		sess := &Session{
			ID: "store-refresh-" + tok.Username,

			Username: tok.Username,
			Subject:  tok.Username,
			Provider: tok.Provider,
			Tokens: &scyauth.Token{
				Token: oauth2.Token{
					AccessToken:  tok.AccessToken,
					RefreshToken: tok.RefreshToken,
					Expiry:       tok.ExpiresAt,
				},
				IDToken: tok.IDToken,
			},
		}
		if r.tryRefreshToken(ctx, sess) != nil {
			refreshed++
		}
	}
	if len(tokens) > 0 {
		logx.Debugf("token-watcher", "store_scan=%d store_refreshed=%d", len(tokens), refreshed)
	}
}
