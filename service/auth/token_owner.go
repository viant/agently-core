package auth

import (
	"context"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/authlog"
	scyauth "github.com/viant/scy/auth"
)

type tokenAvailabilityState uint8

const (
	tokenPreserveWithoutInjection tokenAvailabilityState = iota
	tokenAvailable
	tokenConfirmedMissing
)

type tokenAvailabilityResult struct {
	state tokenAvailabilityState
	token *scyauth.Token
}

func availableOAuthToken(token *scyauth.Token) tokenAvailabilityResult {
	return tokenAvailabilityResult{state: tokenAvailable, token: token}
}

func preserveOAuthSession() tokenAvailabilityResult {
	return tokenAvailabilityResult{state: tokenPreserveWithoutInjection}
}

func confirmedMissingOAuthToken() tokenAvailabilityResult {
	return tokenAvailabilityResult{state: tokenConfirmedMissing}
}

func currentSessionOAuthToken(client *OAuthClient, sess *Session) tokenAvailabilityResult {
	if sess == nil || sess.Tokens == nil {
		return preserveOAuthSession()
	}
	if !usableOAuthToken(sess.Tokens, time.Now()) {
		return preserveOAuthSession()
	}
	if err := validateConfiguredOAuthScopes(client, sess.Scopes, scopeValidationTokens(sess)...); err != nil {
		return preserveOAuthSession()
	}
	return availableOAuthToken(sess.Tokens)
}

func usableOAuthToken(token *scyauth.Token, now time.Time) bool {
	if token == nil {
		return false
	}
	if !token.Expiry.IsZero() && !token.Expiry.After(now) {
		return false
	}
	access := strings.TrimSpace(token.AccessToken)
	idToken := strings.TrimSpace(token.IDToken)
	accessUsable := access != "" && !oauthJWTExpired(access, now)
	idUsable := idToken != "" && !oauthJWTExpired(idToken, now)
	return accessUsable || idUsable
}

type oauthTokenOwnerResolution struct {
	id        string
	canonical bool
	uncertain bool
}

// resolveOAuthTokenOwnerID maps a session back to the canonical token-store owner.
// Prefer the stable oauth subject/provider mapping first, then fall back to
// username lookups for older records.
func resolveOAuthTokenOwnerID(ctx context.Context, users UserService, provider string, sess *Session) string {
	return resolveOAuthTokenOwner(ctx, users, provider, sess).id
}

// resolveOAuthTokenOwner preserves the legacy lookup/fallback order while also
// reporting whether subject+provider was positively mapped to users.id. A
// fallback key may retrieve a usable token, but a miss under that key is not
// proof that the canonical user's token is absent.
func resolveOAuthTokenOwner(ctx context.Context, users UserService, provider string, sess *Session) oauthTokenOwnerResolution {
	if sess == nil {
		return oauthTokenOwnerResolution{}
	}
	result := oauthTokenOwnerResolution{}
	subject := strings.TrimSpace(sess.Subject)
	provider = strings.TrimSpace(provider)
	if users != nil && subject != "" && provider != "" {
		started := time.Now()
		if user, err := users.GetBySubjectAndProvider(ctx, subject, provider); err == nil && user != nil && strings.TrimSpace(user.ID) != "" {
			return oauthTokenOwnerResolution{id: strings.TrimSpace(user.ID), canonical: true}
		} else if err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "owner_resolve_subject",
				UserID:         strings.TrimSpace(sess.UserID),
				Provider:       provider,
				Classification: "owner_resolution",
				Action:         "preserve_no_inject",
				Elapsed:        time.Since(started),
			})
			result.uncertain = true
		}
	}
	if userID := strings.TrimSpace(sess.UserID); userID != "" {
		result.id = userID
		return result
	}
	username := strings.TrimSpace(sess.Username)
	if users != nil && username != "" {
		started := time.Now()
		if user, err := users.GetByUsername(ctx, username); err == nil && user != nil && strings.TrimSpace(user.ID) != "" {
			result.id = strings.TrimSpace(user.ID)
			return result
		} else if err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "owner_resolve_username",
				UserID:         strings.TrimSpace(sess.UserID),
				Provider:       provider,
				Classification: "owner_resolution",
				Action:         "preserve_no_inject",
				Elapsed:        time.Since(started),
			})
			result.uncertain = true
		}
	}
	result.id = strings.TrimSpace(firstNonEmpty(subject, username))
	return result
}

func resolveStoredOAuthToken(ctx context.Context, store TokenStore, owner oauthTokenOwnerResolution, provider string, client *OAuthClient, expectedScopes []string) tokenAvailabilityResult {
	if owner.uncertain {
		return preserveOAuthSession()
	}
	owner.id = strings.TrimSpace(owner.id)
	provider = strings.TrimSpace(provider)
	if store == nil || owner.id == "" || provider == "" {
		return preserveOAuthSession()
	}
	dbTok, err := store.Get(ctx, owner.id, provider)
	if err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "store_get",
			UserID:         owner.id,
			Provider:       provider,
			Classification: "persistence",
			Action:         "preserve_no_inject",
		})
		return preserveOAuthSession()
	}
	if dbTok == nil {
		if owner.canonical && !owner.uncertain {
			return confirmedMissingOAuthToken()
		}
		return preserveOAuthSession()
	}
	if storedProvider := strings.TrimSpace(dbTok.Provider); storedProvider != "" && storedProvider != provider {
		return preserveOAuthSession()
	}
	token := &scyauth.Token{IDToken: strings.TrimSpace(dbTok.IDToken)}
	token.AccessToken = strings.TrimSpace(dbTok.AccessToken)
	token.RefreshToken = strings.TrimSpace(dbTok.RefreshToken)
	token.Expiry = dbTok.ExpiresAt
	if !usableOAuthToken(token, time.Now()) {
		return preserveOAuthSession()
	}
	if err := validateConfiguredOAuthScopes(client, expectedScopes, token.IDToken, token.AccessToken, token.RefreshToken); err != nil {
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
	return availableOAuthToken(token)
}

func resolveSessionOAuthToken(ctx context.Context, users UserService, store TokenStore, provider string, client *OAuthClient, sess *Session) tokenAvailabilityResult {
	if sess == nil {
		return preserveOAuthSession()
	}
	if current := currentSessionOAuthToken(client, sess); current.state == tokenAvailable {
		return current
	}
	owner := resolveOAuthTokenOwner(ctx, users, provider, sess)
	return resolveStoredOAuthToken(ctx, store, owner, provider, client, sess.Scopes)
}
