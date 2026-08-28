package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/viant/datly"
)

// TestNewDelegatedMCPAuthKeyDerivation covers the encryption-key contract:
// explicit auth.tokenEncryptionKey enables delegated auth for JWT/local
// workspaces, the legacy configURL salt keeps working, and a workspace with
// neither installs a fail-loud resolver instead of silently disabling.
func TestNewDelegatedMCPAuthKeyDerivation(t *testing.T) {
	dao := &datly.Service{}

	// No DAO: no persistence layer at all; the manager then fails loudly with
	// "no credential resolver is installed" for delegated configs.
	if delegated := NewDelegatedMCPAuth(&Config{}, nil); delegated != nil {
		t.Fatalf("nil dao must disable construction entirely")
	}

	// JWT-only workspace with an explicit key: fully configured.
	jwtOnly := NewDelegatedMCPAuth(&Config{JWT: &JWT{Enabled: true}, TokenEncryptionKey: "jwt-key"}, dao)
	if jwtOnly == nil || jwtOnly.resolver == nil || jwtOnly.resolver.initErr != nil || jwtOnly.resolver.store == nil {
		t.Fatalf("explicit tokenEncryptionKey must enable delegated auth for jwt workspaces: %+v", jwtOnly)
	}

	// Legacy fallback: workspace OAuth configURL-derived salt.
	legacy := NewDelegatedMCPAuth(&Config{OAuth: &OAuth{Client: &OAuthClient{ConfigURL: "scy://workspace"}}}, dao)
	if legacy == nil || legacy.resolver == nil || legacy.resolver.initErr != nil || legacy.resolver.store == nil {
		t.Fatalf("configURL fallback must keep delegated auth enabled: %+v", legacy)
	}

	// Neither: fail loudly when delegated configuration is used.
	unkeyed := NewDelegatedMCPAuth(&Config{JWT: &JWT{Enabled: true}}, dao)
	if unkeyed == nil || unkeyed.resolver == nil {
		t.Fatalf("missing key must not silently disable delegated auth")
	}
	if _, err := unkeyed.Resolver().Resolve(delegatedCtx("uuid-1"), dev6Requirement()); err == nil ||
		!strings.Contains(err.Error(), "tokenEncryptionKey") {
		t.Fatalf("resolution without an encryption key must fail with the configuration requirement, got %v", err)
	}
	row := seededDev6Token(time.Now().Add(time.Minute))
	if err := unkeyed.TokenRefresher().RefreshStoredDelegatedToken(context.Background(), row); err == nil ||
		!strings.Contains(err.Error(), "tokenEncryptionKey") {
		t.Fatalf("background refresh without an encryption key must fail loudly, got %v", err)
	}
}
