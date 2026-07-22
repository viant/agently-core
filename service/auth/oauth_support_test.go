package auth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func fakeJWTWithExp(t *testing.T, exp time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"exp": exp.Unix()})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return "x." + base64.RawURLEncoding.EncodeToString(payload) + ".y"
}

func TestResolveTokenExpiry_ExplicitValueWins(t *testing.T) {
	jwtExp := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	explicit := time.Now().Add(30 * time.Minute).UTC().Truncate(time.Second)
	got := resolveTokenExpiry(explicit.Format(time.RFC3339), fakeJWTWithExp(t, jwtExp), "")
	if !got.Equal(explicit) {
		t.Fatalf("resolveTokenExpiry() = %v, want explicit expiry %v", got, explicit)
	}
}

func TestResolveTokenExpiry_FallsBackToJWTExp(t *testing.T) {
	jwtExp := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	got := resolveTokenExpiry("", fakeJWTWithExp(t, jwtExp), "")
	if !got.Equal(jwtExp) {
		t.Fatalf("resolveTokenExpiry() = %v, want jwt expiry %v", got, jwtExp)
	}
}

func TestTokenScopesFromStrings_MergesAcrossTokens(t *testing.T) {
	idToken := fakeJWTWithScopeOnlyClaims(t, map[string]any{
		"scope": "openid profile email",
	})
	accessToken := fakeJWTWithScopeOnlyClaims(t, map[string]any{
		"scope": "openid ROLE_STEWARD_WEB",
	})

	got := tokenScopesFromStrings(idToken, accessToken)
	want := []string{"openid", "profile", "email", "ROLE_STEWARD_WEB"}
	if len(got) != len(want) {
		t.Fatalf("tokenScopesFromStrings() len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tokenScopesFromStrings()[%d] = %q, want %q (%v)", i, got[i], want[i], got)
		}
	}
}

func TestValidateConfiguredOAuthScopes_AcceptsDiscriminatorFromAccessToken(t *testing.T) {
	client := &OAuthClient{
		Scopes:         []string{"openid", "profile", "email"},
		WebUIScopes:    []string{"ROLE_STEWARD_WEB"},
		MobileUIScopes: []string{"ROLE_STEWARD_MOBILE"},
	}
	idToken := fakeJWTWithScopeOnlyClaims(t, map[string]any{
		"scope": "openid profile email",
	})
	accessToken := fakeJWTWithScopeOnlyClaims(t, map[string]any{
		"scope": "openid ROLE_STEWARD_WEB",
	})

	if err := validateConfiguredOAuthScopes(client, []string{"openid", "profile", "email", "ROLE_STEWARD_WEB"}, idToken, accessToken, ""); err != nil {
		t.Fatalf("validateConfiguredOAuthScopes() unexpected error = %v", err)
	}
}

func fakeJWTWithScopeOnlyClaims(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return "x." + base64.RawURLEncoding.EncodeToString(payload) + ".y"
}
