package auth

import (
	"context"
	"fmt"
	"strings"
)

// JWT-only workspaces have one configured issuer/key authority. OAuth imports
// retain their existing provider contract; mixed issuer selection is separate.
func requiresJWTSessionVerification(cfg *Config) bool {
	return cfg != nil && cfg.JWT != nil && cfg.JWT.Enabled && (cfg.OAuth == nil || cfg.OAuth.Client == nil)
}
func verifyJWTSessionImport(ctx context.Context, cfg *Config, tokens ...string) (*UserInfo, error) {
	if !requiresJWTSessionVerification(cfg) {
		return nil, nil
	}
	verifier := NewJWTService(cfg.JWT)
	if err := verifier.Init(ctx); err != nil {
		return nil, fmt.Errorf("JWT session verification unavailable")
	}
	var identity *UserInfo
	seen := map[string]bool{}
	for _, raw := range tokens {
		raw = strings.TrimSpace(raw)
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		claims, err := verifier.Verify(ctx, raw)
		if err != nil || claims == nil || strings.TrimSpace(claims.Subject) == "" {
			return nil, fmt.Errorf("invalid or expired JWT session credential")
		}
		if identity != nil && identity.Subject != claims.Subject {
			return nil, fmt.Errorf("JWT session credential identity mismatch")
		}
		if identity == nil {
			identity = claims
		}
	}
	if identity == nil {
		return nil, fmt.Errorf("JWT session credential required")
	}
	return identity, nil
}
