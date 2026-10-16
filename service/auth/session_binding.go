package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	iauth "github.com/viant/agently-core/internal/auth"
)

// meSessionBinding is a cache/lifecycle fence, never an authorization grant.
// Only the authenticated ingress context and server-owned session contribute.
// Credential rotation deliberately changes the fence even for the same user:
// consumers discard mounted/cached state and can restore durable conversations
// with fresh authorization. No cookie, ID token or refresh token is returned.
func meSessionBinding(ctx context.Context, provider, subject string, session *Session) string {
	if strings.TrimSpace(subject) == "" {
		return ""
	}
	canonical := iauth.CanonicalUserID(ctx)
	if actor := iauth.User(ctx); actor != nil {
		subject = strings.TrimSpace(firstNonEmpty(actor.Subject, actor.Email))
	}
	if currentProvider := iauth.Provider(ctx); currentProvider != "" {
		provider = currentProvider
	}
	sessionID := ""
	credential := strings.TrimSpace(IDToken(ctx))
	if session != nil {
		sessionID = session.ID
		if canonical == "" {
			canonical = strings.TrimSpace(session.UserID)
		}
		if credential == "" && session.Tokens != nil {
			credential = strings.TrimSpace(session.Tokens.IDToken)
			if credential == "" {
				credential = strings.TrimSpace(session.Tokens.AccessToken)
			}
		}
	}
	if credential == "" {
		credential = strings.TrimSpace(MCPAuthToken(ctx, false))
	}
	encoded, _ := json.Marshal([]string{"agently-auth-session-v1", provider, subject, canonical, sessionID, credential})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
