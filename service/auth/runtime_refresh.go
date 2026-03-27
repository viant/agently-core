package auth

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

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

func (r *Runtime) tryLoadFreshTokenFromStore(ctx context.Context, sess *Session) *scyauth.Token {
	if r.ext == nil || r.ext.tokenStore == nil || sess == nil {
		return nil
	}
	username := strings.TrimSpace(sess.Subject)
	provider := r.ext.oauthProviderName()
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
	r.sessions.Put(ctx, sess)
	log.Printf("[token-refresh] loaded fresh token from DB user=%q expiry=%v", username, dbTok.ExpiresAt.Format(time.RFC3339))
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

	username := strings.TrimSpace(sess.Subject)
	provider := r.ext.oauthProviderName()
	tokenStore := r.ext.tokenStore
	if tokenStore != nil {
		_, acquired, err := tokenStore.TryAcquireRefreshLease(ctx, username, provider, runtimeWorkerID, 30*time.Second)
		if err != nil {
			log.Printf("[token-refresh] lease acquire error user=%q err=%v", username, err)
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
		log.Printf("[token-refresh] failed user=%q err=%v", username, err)
		return nil
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = sess.Tokens.RefreshToken
	}
	result := &scyauth.Token{Token: *refreshed, IDToken: sess.Tokens.IDToken}
	sess.Tokens = result
	r.sessions.Put(ctx, sess)
	if tokenStore != nil {
		_ = tokenStore.Put(ctx, &OAuthToken{
			Username:     username,
			Provider:     provider,
			AccessToken:  refreshed.AccessToken,
			IDToken:      sess.Tokens.IDToken,
			RefreshToken: refreshed.RefreshToken,
			ExpiresAt:    refreshed.Expiry,
		})
	}
	log.Printf("[token-refresh] ok user=%q newExpiry=%v", username, refreshed.Expiry.Format(time.RFC3339))
	return result
}

func (r *Runtime) startTokenRefreshWatcher(ctx context.Context) func() {
	if r == nil || r.sessions == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Second)
		r.refreshExpiringSessions(ctx)
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
		log.Printf("[token-watcher] sessions=%d checked=%d refreshed=%d", len(sessions), checked, refreshed)
	}
}
