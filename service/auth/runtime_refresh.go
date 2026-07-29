package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/authlog"
	"github.com/viant/agently-core/internal/logx"
	runtimediscovery "github.com/viant/agently-core/runtime/discovery"
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

const transientRefreshRetryWindow = 30 * time.Second
const runtimeAuthStoreTimeout = 5 * time.Second
const authRefreshWatchIntervalEnv = "AGENTLY_AUTH_REFRESH_WATCH_INTERVAL"

func authStoreContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), runtimeAuthStoreTimeout)
}

func (r *Runtime) putSessionDurable(ctx context.Context, sess *Session) {
	if r == nil || r.sessions == nil || sess == nil {
		return
	}
	storeCtx, cancel := authStoreContext(ctx)
	defer cancel()
	r.sessions.Put(storeCtx, sess)
}

func (r *Runtime) deleteSessionDurable(ctx context.Context, id string) {
	if r == nil || r.sessions == nil || strings.TrimSpace(id) == "" {
		return
	}
	storeCtx, cancel := authStoreContext(ctx)
	defer cancel()
	r.sessions.Delete(storeCtx, strings.TrimSpace(id))
}

func (r *Runtime) ensureSessionOAuthTokens(ctx context.Context, sess *Session) tokenAvailabilityResult {
	if sess == nil {
		return preserveOAuthSession()
	}
	if current := currentSessionOAuthToken(runtimeOAuthClient(r), sess); current.state == tokenAvailable {
		return current
	}
	return r.tryLoadFreshTokenFromStore(ctx, sess)
}

func (r *Runtime) resolveRuntimeOAuthTokenOwner(ctx context.Context, sess *Session) (string, string) {
	owner, provider := r.resolveRuntimeOAuthTokenOwnerResolution(ctx, sess)
	return owner.id, provider
}

func (r *Runtime) resolveRuntimeOAuthTokenOwnerResolution(ctx context.Context, sess *Session) (oauthTokenOwnerResolution, string) {
	if r == nil || r.ext == nil || sess == nil {
		return oauthTokenOwnerResolution{}, ""
	}
	provider := strings.TrimSpace(firstNonEmpty(sess.Provider, r.ext.oauthProviderName()))
	if provider == "" {
		return oauthTokenOwnerResolution{}, ""
	}
	owner := resolveOAuthTokenOwner(ctx, r.ext.users, provider, sess)
	if owner.id == "" {
		return oauthTokenOwnerResolution{}, ""
	}
	return owner, provider
}

func (r *Runtime) tryLoadFreshTokenFromStore(ctx context.Context, sess *Session) tokenAvailabilityResult {
	if r == nil || r.ext == nil || r.ext.tokenStore == nil || sess == nil {
		return preserveOAuthSession()
	}
	owner, provider := r.resolveRuntimeOAuthTokenOwnerResolution(ctx, sess)
	if owner.uncertain || owner.id == "" || provider == "" {
		return preserveOAuthSession()
	}
	storeCtx, cancel := authStoreContext(ctx)
	defer cancel()
	dbTok, err := r.ext.tokenStore.Get(storeCtx, owner.id, provider)
	if err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "store_get",
			UserID:         owner.id,
			Provider:       provider,
			Classification: "persistence",
			Action:         "preserve",
		})
		return preserveOAuthSession()
	}
	if dbTok == nil {
		if owner.canonical {
			return confirmedMissingOAuthToken()
		}
		return preserveOAuthSession()
	}
	if storedProvider := strings.TrimSpace(dbTok.Provider); storedProvider != "" && storedProvider != provider {
		return preserveOAuthSession()
	}
	if !dbTok.ExpiresAt.IsZero() && !dbTok.ExpiresAt.After(time.Now()) {
		return preserveOAuthSession()
	}
	if sess.Tokens != nil && !sess.Tokens.Expiry.IsZero() && !dbTok.ExpiresAt.After(sess.Tokens.Expiry) {
		return preserveOAuthSession()
	}
	if err := validateConfiguredOAuthScopes(runtimeOAuthClient(r), sess.Scopes, dbTok.IDToken, dbTok.AccessToken, dbTok.RefreshToken); err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "scope_validate_stored",
			UserID:         owner.id,
			Provider:       provider,
			Classification: "scope_validation",
			Action:         "preserve_no_inject",
			Err:            err,
		})
		return preserveOAuthSession()
	}
	result := &scyauth.Token{
		Token: oauth2.Token{
			AccessToken:  dbTok.AccessToken,
			RefreshToken: dbTok.RefreshToken,
			Expiry:       dbTok.ExpiresAt,
		},
		IDToken: dbTok.IDToken,
	}
	if !usableOAuthToken(result, time.Now()) {
		return preserveOAuthSession()
	}
	sess.Tokens = result
	sess.Provider = provider
	r.putSessionDurable(ctx, sess)
	logx.Debugf("token-refresh", "loaded fresh token from DB user=%q provider=%q expiry=%v", owner.id, provider, dbTok.ExpiresAt.Format(time.RFC3339))
	return availableOAuthToken(result)
}

func (r *Runtime) tryRefreshToken(ctx context.Context, sess *Session) tokenAvailabilityResult {
	if sess == nil || sess.Tokens == nil || sess.Tokens.RefreshToken == "" {
		return preserveOAuthSession()
	}
	if r.ext == nil || r.ext.cfg == nil || r.ext.cfg.OAuth == nil || r.ext.cfg.OAuth.Client == nil {
		return preserveOAuthSession()
	}
	if fresh := r.tryLoadFreshTokenFromStore(ctx, sess); fresh.state != tokenPreserveWithoutInjection {
		return fresh
	}

	owner, provider := r.resolveRuntimeOAuthTokenOwnerResolution(ctx, sess)
	if owner.uncertain || owner.id == "" || provider == "" {
		return preserveOAuthSession()
	}
	tokenStore := r.ext.tokenStore
	if tokenStore != nil {
		storeCtx, cancel := authStoreContext(ctx)
		_, acquired, err := tokenStore.TryAcquireRefreshLease(storeCtx, owner.id, provider, runtimeWorkerID, 30*time.Second)
		cancel()
		if err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "lease_acquire",
				UserID:         owner.id,
				Provider:       provider,
				Classification: "lease",
				Action:         "preserve_cooldown",
			})
			r.storeRefreshRetryAt(sess, time.Now().Add(transientRefreshRetryWindow))
			r.putSessionDurable(ctx, sess)
			return preserveOAuthSession()
		}
		if !acquired {
			time.Sleep(2 * time.Second)
			return r.tryLoadFreshTokenFromStore(ctx, sess)
		}
		defer func() {
			releaseCtx, releaseCancel := authStoreContext(ctx)
			defer releaseCancel()
			if err := tokenStore.ReleaseRefreshLease(releaseCtx, owner.id, provider, runtimeWorkerID); err != nil {
				authlog.Log(ctx, authlog.Event{
					Op:             "lease_release",
					UserID:         owner.id,
					Provider:       provider,
					Classification: "lease",
					Action:         "preserve",
				})
			}
		}()
	}

	oauthCfg, err := loadOAuthClientConfig(ctx, r.ext.cfg.OAuth.Client.ConfigURL)
	if err != nil || oauthCfg == nil {
		r.storeRefreshRetryAt(sess, time.Now().Add(transientRefreshRetryWindow))
		r.putSessionDurable(ctx, sess)
		authlog.Log(ctx, authlog.Event{
			Op:             "refresh_config",
			UserID:         owner.id,
			Provider:       provider,
			Classification: "config",
			Action:         "preserve_cooldown",
		})
		return preserveOAuthSession()
	}
	scopes := oauthRefreshScopesForClient(sess, nil, r.ext.cfg.OAuth.Client)
	resource := oauthRefreshResource(sess, nil, oauthCfg.ClientID)
	refreshStarted := time.Now()
	refreshed, err := refreshOAuthToken(ctx, cloneOAuthConfigWithScopes(oauthCfg, scopes), &sess.Tokens.Token, scopes, resource)
	if err != nil {
		status, code := oauthRefreshErrorDetails(err)
		if isPermanentRefreshError(err) {
			authlog.Log(ctx, authlog.Event{
				Op:             "refresh_action",
				UserID:         owner.id,
				Provider:       provider,
				Endpoint:       oauthCfg.Endpoint.TokenURL,
				HTTPStatus:     status,
				OAuthErrorCode: code,
				Classification: "invalid_grant",
				Action:         "clear",
				Elapsed:        time.Since(refreshStarted),
			})
			r.invalidateSessionTokens(ctx, sess, owner.id, provider)
			return confirmedMissingOAuthToken()
		}
		authlog.Log(ctx, authlog.Event{
			Op:             "refresh_action",
			UserID:         owner.id,
			Provider:       provider,
			Endpoint:       oauthCfg.Endpoint.TokenURL,
			HTTPStatus:     status,
			OAuthErrorCode: code,
			Classification: oauthRefreshErrorClassification(err),
			Action:         "preserve_cooldown",
			Elapsed:        time.Since(refreshStarted),
		})
		r.storeRefreshRetryAt(sess, time.Now().Add(transientRefreshRetryWindow))
		r.putSessionDurable(ctx, sess)
		return preserveOAuthSession()
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = sess.Tokens.RefreshToken
	}
	previousIDToken := strings.TrimSpace(sess.Tokens.IDToken)
	refreshedIDToken := refreshedOAuthIDToken(refreshed, previousIDToken)
	result := &scyauth.Token{Token: *refreshed, IDToken: refreshedIDToken}
	if err := validateRefreshedOAuthScopes(r.ext.cfg.OAuth.Client, scopes, refreshed, refreshedIDToken); err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "scope_validate_candidate",
			UserID:         owner.id,
			Provider:       provider,
			Endpoint:       oauthCfg.Endpoint.TokenURL,
			Classification: "scope_validation",
			Action:         "preserve_cooldown",
			Elapsed:        time.Since(refreshStarted),
			Err:            err,
		})
		r.storeRefreshRetryAt(sess, time.Now().Add(transientRefreshRetryWindow))
		r.putSessionDurable(ctx, sess)
		return preserveOAuthSession()
	}
	if tokenStore != nil {
		storeCtx, cancel := authStoreContext(ctx)
		err := tokenStore.Put(storeCtx, &OAuthToken{
			Username:     owner.id,
			Provider:     provider,
			AccessToken:  refreshed.AccessToken,
			IDToken:      refreshedIDToken,
			RefreshToken: refreshed.RefreshToken,
			ExpiresAt:    refreshed.Expiry,
		})
		cancel()
		if err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "store_put_candidate",
				UserID:         owner.id,
				Provider:       provider,
				Classification: "persistence",
				Action:         "preserve_cooldown",
				Elapsed:        time.Since(refreshStarted),
			})
			r.storeRefreshRetryAt(sess, time.Now().Add(transientRefreshRetryWindow))
			r.putSessionDurable(ctx, sess)
			return preserveOAuthSession()
		}
	}
	sess.Tokens = result
	if len(sess.Scopes) == 0 {
		sess.Scopes = append([]string(nil), scopes...)
	}
	r.clearRefreshRetryAt(sess)
	sess.Provider = provider
	r.putSessionDurable(ctx, sess)
	logx.Debugf("token-refresh", "refresh ok user=%q newExpiry=%v access_fp=%s id_fp=%s id_rotated=%v",
		owner.id,
		refreshed.Expiry.Format(time.RFC3339),
		tokenFingerprint(refreshed.AccessToken),
		tokenFingerprint(refreshedIDToken),
		strings.TrimSpace(refreshedIDToken) != previousIDToken,
	)
	return availableOAuthToken(result)
}

func refreshedOAuthIDToken(refreshed *oauth2.Token, current string) string {
	if refreshed == nil {
		current = strings.TrimSpace(current)
		if oauthJWTExpired(current, time.Now()) {
			return ""
		}
		return current
	}
	if raw := refreshed.Extra("id_token"); raw != nil {
		if token, ok := raw.(string); ok && strings.TrimSpace(token) != "" {
			token = strings.TrimSpace(token)
			if !oauthJWTExpired(token, time.Now()) {
				return token
			}
			return ""
		}
	}
	current = strings.TrimSpace(current)
	if oauthJWTExpired(current, time.Now()) {
		return ""
	}
	return current
}

func oauthJWTExpired(token string, now time.Time) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}
	expiresAt, ok := claimUnixTime(parseJWTClaims(token), "exp")
	return ok && !expiresAt.After(now.Add(30*time.Second))
}

func tokenFingerprint(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "none"
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:6])
}

// isPermanentRefreshError reports whether a parsed token-endpoint response
// proves that the refresh credential itself is no longer usable. Only
// invalid_grant has that meaning; all other failures preserve credentials.
func isPermanentRefreshError(err error) bool {
	if err == nil {
		return false
	}
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(re.ErrorCode), "invalid_grant") {
		return false
	}
	return re.Response == nil || re.Response.StatusCode < http.StatusInternalServerError
}

// invalidateSessionTokens wipes the session's token fields and removes the
// cached token from the persistent store. Called on permanent refresh
// failure so the next request takes the unauthenticated path (triggering
// re-auth) rather than retrying the same dead refresh token forever.
//
// Errors from the token-store delete are logged but not returned: we must
// always clear the in-memory session, and the store-side row will expire
// naturally if deletion happens to fail.
func (r *Runtime) invalidateSessionTokens(ctx context.Context, sess *Session, username, provider string) {
	if sess != nil {
		sess.Tokens = nil
		r.putSessionDurable(ctx, sess)
	}
	if r == nil || r.ext == nil || r.ext.tokenStore == nil {
		return
	}
	if username == "" || provider == "" {
		return
	}
	storeCtx, cancel := authStoreContext(ctx)
	defer cancel()
	if err := r.ext.tokenStore.Delete(storeCtx, username, provider); err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "store_delete",
			UserID:         username,
			Provider:       provider,
			Classification: "persistence",
			Action:         "session_cleared_store_preserved",
		})
	}
}

func (r *Runtime) startTokenRefreshWatcher(ctx context.Context) func() {
	if r == nil || r.sessions == nil {
		return func() {}
	}
	lead := r.cfg.tokenRefreshLead()
	if lead <= 0 {
		lead = 30 * time.Minute
	}
	interval, overrideActive := tokenRefreshWatchInterval(lead)
	logx.Debugf("token-watcher", "starting lead=%s interval=%s override_active=%t", lead, interval, overrideActive)
	watcherCtx := runtimediscovery.WithBackground(ctx)

	done := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Second)
		r.refreshExpiringSessions(watcherCtx)
		r.refreshTokenStore(watcherCtx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.refreshExpiringSessions(watcherCtx)
				r.refreshTokenStore(watcherCtx)
			case <-done:
				return
			case <-watcherCtx.Done():
				return
			}
		}
	}()
	return func() { close(done) }
}

func tokenRefreshWatchInterval(lead time.Duration) (time.Duration, bool) {
	defaultInterval := lead / 2
	rawInterval, ok := os.LookupEnv(authRefreshWatchIntervalEnv)
	if !ok {
		return defaultInterval, false
	}
	interval, err := time.ParseDuration(strings.TrimSpace(rawInterval))
	if err != nil {
		authlog.Log(context.Background(), authlog.Event{
			Op:             "watcher_config",
			Caller:         "background",
			Classification: "config",
			Action:         "use_default",
			Err:            err,
		})
		return defaultInterval, false
	}
	if interval <= 0 {
		authlog.Log(context.Background(), authlog.Event{
			Op:             "watcher_config",
			Caller:         "background",
			Classification: "config",
			Action:         "use_default",
			Err:            fmt.Errorf("refresh watch interval must be positive"),
		})
		return defaultInterval, false
	}
	return interval, true
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
		if r.tryRefreshToken(ctx, sess).state == tokenAvailable {
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
	if err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "store_scan",
			Caller:         "background",
			Classification: "persistence",
			Action:         "preserve",
		})
		return
	}
	if len(tokens) == 0 {
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
		if r.tryRefreshToken(ctx, sess).state == tokenAvailable {
			refreshed++
		}
	}
	if len(tokens) > 0 {
		logx.Debugf("token-watcher", "store_scan=%d store_refreshed=%d", len(tokens), refreshed)
	}
}
