package token

import (
	"context"
	"encoding/json"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

// SecurityData is the JSON-serializable auth state saved to run.SecurityContext.
type SecurityData struct {
	AccessToken  string    `json:"accessToken,omitempty"`
	IDToken      string    `json:"idToken,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
	Subject      string    `json:"subject,omitempty"`
	Provider     string    `json:"provider,omitempty"`
}

// MarshalSecurityContext serializes the auth tokens from context into a JSON string
// suitable for storing in run.SecurityContext.
func MarshalSecurityContext(ctx context.Context) (string, error) {
	sd := SecurityData{}

	if tok := iauth.TokensFromContext(ctx); tok != nil {
		sd.AccessToken = tok.AccessToken
		sd.IDToken = tok.IDToken
		sd.RefreshToken = tok.RefreshToken
		sd.ExpiresAt = tok.Expiry
	} else {
		// Fall back to individual context values.
		sd.AccessToken = iauth.Bearer(ctx)
		sd.IDToken = iauth.IDToken(ctx)
	}

	if ui := iauth.User(ctx); ui != nil {
		sd.Subject = ui.Subject
		if sd.Subject == "" {
			sd.Subject = ui.Email
		}
	}
	sd.Provider = "default"

	// Only marshal if we have at least one token.
	if sd.AccessToken == "" && sd.IDToken == "" {
		return "", nil
	}

	data, err := json.Marshal(sd)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// RestoreSecurityContext deserializes auth state from a run.SecurityContext
// string and injects tokens into the context.
func RestoreSecurityContext(ctx context.Context, data string) (context.Context, *SecurityData, error) {
	if data == "" {
		return ctx, nil, nil
	}
	var sd SecurityData
	if err := json.Unmarshal([]byte(data), &sd); err != nil {
		return ctx, nil, err
	}

	tok := &scyauth.Token{
		Token: oauth2.Token{
			AccessToken:  sd.AccessToken,
			RefreshToken: sd.RefreshToken,
			Expiry:       sd.ExpiresAt,
		},
		IDToken: sd.IDToken,
	}
	ctx = injectTokens(ctx, tok)

	if sd.Subject != "" {
		ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: sd.Subject})
	}

	return ctx, &sd, nil
}
