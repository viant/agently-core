package auth

import (
	"context"
	"strings"
)

// resolveOAuthTokenOwnerID maps a session back to the canonical token-store owner.
// Prefer the stable oauth subject/provider mapping first, then fall back to
// username lookups for older records.
func resolveOAuthTokenOwnerID(ctx context.Context, users UserService, provider string, sess *Session) string {
	if sess == nil {
		return ""
	}
	subject := strings.TrimSpace(sess.Subject)
	provider = strings.TrimSpace(provider)
	if users != nil && subject != "" && provider != "" {
		if user, err := users.GetBySubjectAndProvider(ctx, subject, provider); err == nil && user != nil && strings.TrimSpace(user.ID) != "" {
			return strings.TrimSpace(user.ID)
		}
	}
	username := strings.TrimSpace(sess.Username)
	if users != nil && username != "" {
		if user, err := users.GetByUsername(ctx, username); err == nil && user != nil && strings.TrimSpace(user.ID) != "" {
			return strings.TrimSpace(user.ID)
		}
	}
	return strings.TrimSpace(firstNonEmpty(subject, username))
}
