package auth

import (
	"context"
	"github.com/google/uuid"
	"github.com/viant/agently-core/internal/authlog"
	"strings"
	"time"
)

// ConditionalTokenClear is optional: a refresh failure must never erase a
// credential replaced after its lease was acquired. Explicit logout is separate.
type ConditionalTokenClear interface {
	CASClear(context.Context, string, string, int64, string) (bool, error)
}

func exactRefreshToken(ctx context.Context, store TokenStore, user, provider string) (*OAuthToken, error) {
	if exact, ok := store.(interface {
		GetExact(context.Context, string, string) (*OAuthToken, error)
	}); ok {
		return exact.GetExact(ctx, user, provider)
	}
	token, err := store.Get(ctx, user, provider)
	if token != nil && strings.TrimSpace(token.Provider) != provider {
		return nil, err
	}
	return token, err
}
func refreshLeaseOwner() string { return runtimeWorkerID + ":" + uuid.NewString() }
func sameRefreshCredential(sess *Session, attempt string) bool {
	return sess != nil && sess.Tokens != nil && sess.Tokens.RefreshToken == attempt
}
func (r *Runtime) recoverRefreshWinner(ctx context.Context, sess *Session) tokenAvailabilityResult {
	result := r.loadFreshTokenFromStore(ctx, sess, true)
	if result.state == tokenPreserveWithoutInjection {
		r.storeRefreshRetryAt(sess, time.Now().Add(transientRefreshRetryWindow))
	}
	return result
}
func (r *Runtime) invalidateOwnedRefresh(ctx context.Context, sess *Session, user, provider, attempt string, version int64, lease string) tokenAvailabilityResult {
	if r.ext.tokenStore == nil {
		if sameRefreshCredential(sess, attempt) {
			sess.Tokens = nil
			r.putSessionDurable(ctx, sess)
			return confirmedMissingOAuthToken()
		}
		return preserveOAuthSession()
	}
	conditional, ok := r.ext.tokenStore.(ConditionalTokenClear)
	if !ok {
		return r.recoverRefreshWinner(ctx, sess)
	}
	storeCtx, cancel := authStoreContext(ctx)
	cleared, err := conditional.CASClear(storeCtx, user, provider, version, lease)
	cancel()
	if err != nil || !cleared {
		return r.recoverRefreshWinner(ctx, sess)
	}
	authlog.Log(ctx, authlog.Event{Op: "store_delete", UserID: user, Provider: provider, Source: refreshSource(ctx), Classification: "persistence", Action: "deleted", Reason: "matching_refresh_lease"})
	if sameRefreshCredential(sess, attempt) {
		sess.Tokens = nil
		r.putSessionDurable(ctx, sess)
		return confirmedMissingOAuthToken()
	}
	return r.recoverRefreshWinner(ctx, sess)
}

func changedStoredCredential(sess *Session, stored *OAuthToken) bool {
	if stored == nil {
		return false
	}
	if sess == nil || sess.Tokens == nil {
		return true
	}
	held := sess.Tokens
	return held.RefreshToken != stored.RefreshToken || held.AccessToken != stored.AccessToken || held.IDToken != stored.IDToken
}
