package token

import (
	"context"

	scyauth "github.com/viant/scy/auth"
)

// Broker handles token refresh and exchange operations.
// When nil on Manager, the manager operates in cache-only mode.
type Broker interface {
	// Refresh uses a refresh token to obtain new access/ID tokens.
	Refresh(ctx context.Context, key Key, refreshToken string) (*scyauth.Token, error)
	// Exchange converts an authorization code to tokens (for OOB/scheduled flows).
	Exchange(ctx context.Context, key Key, code string) (*scyauth.Token, error)
}
