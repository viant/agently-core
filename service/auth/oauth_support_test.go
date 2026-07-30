package auth

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
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

func TestValidateConfiguredOAuthScopes_ExplicitExpectedJWTRequiresCompleteSet(t *testing.T) {
	client := &OAuthClient{
		Scopes:         []string{"openid", "profile", "email"},
		WebUIScopes:    []string{"ROLE_STEWARD_WEB"},
		MobileUIScopes: []string{"ROLE_STEWARD_MOBILE"},
	}
	expected := []string{"openid", "profile", "email", "ROLE_STEWARD_WEB"}
	roleOnly := fakeJWTWithScopeOnlyClaims(t, map[string]any{
		"scope": "ROLE_STEWARD_WEB",
	})
	if err := validateConfiguredOAuthScopes(client, expected, roleOnly); err == nil {
		t.Fatal("validateConfiguredOAuthScopes() accepted role-only JWT for explicit expected scopes")
	}

	idToken := fakeJWTWithScopeOnlyClaims(t, map[string]any{
		"scope": "openid profile email",
	})
	accessToken := fakeJWTWithScopeOnlyClaims(t, map[string]any{
		"scope": "openid ROLE_STEWARD_WEB",
	})
	if err := validateConfiguredOAuthScopes(client, expected, idToken, accessToken, ""); err != nil {
		t.Fatalf("validateConfiguredOAuthScopes() rejected complete merged JWT scope set: %v", err)
	}
}

func TestValidateRefreshedOAuthScopes_ExplicitOpaqueResponseRequiresCompleteExpectedSet(t *testing.T) {
	expected := []string{"openid", "profile", "email", "ROLE_STEWARD_WEB"}
	tests := []struct {
		name    string
		scope   string
		wantErr bool
	}{
		{
			name:    "role only rejected",
			scope:   "ROLE_STEWARD_WEB",
			wantErr: true,
		},
		{
			name:  "full set accepted",
			scope: "ROLE_STEWARD_WEB email openid profile",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opaque := (&oauth2.Token{
				AccessToken: "opaque-access-token",
				Expiry:      time.Now().Add(time.Hour),
			}).WithExtra(map[string]interface{}{"scope": test.scope})
			err := validateRefreshedOAuthScopes(nil, expected, opaque, "")
			if test.wantErr && err == nil {
				t.Fatal("validateRefreshedOAuthScopes() error = nil, want missing-scope rejection")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("validateRefreshedOAuthScopes() error = %v", err)
			}
		})
	}
}

func TestValidateOAuthTokenScopes_ResponseScopeHandling(t *testing.T) {
	expected := []string{"openid", "profile", "email", "ROLE_STEWARD_WEB"}
	tests := []struct {
		name    string
		extra   any
		wantErr bool
	}{
		{
			name: "url values missing scope uses expected scopes",
			extra: url.Values{
				"access_token": {"opaque-access-token"},
				"token_type":   {"Bearer"},
			},
		},
		{
			name:    "partial nonempty scope rejected",
			extra:   map[string]any{"scope": "openid profile"},
			wantErr: true,
		},
		{
			name:  "complete scope accepted",
			extra: map[string]any{"scope": "ROLE_STEWARD_WEB email openid profile"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oauthToken := (&oauth2.Token{
				AccessToken: "opaque-access-token",
				Expiry:      time.Now().Add(time.Hour),
			}).WithExtra(test.extra)
			token := &scyauth.Token{Token: *oauthToken}

			err := ValidateOAuthTokenScopes(expected, token)
			if test.wantErr && err == nil {
				t.Fatal("ValidateOAuthTokenScopes() error = nil, want missing-scope rejection")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("ValidateOAuthTokenScopes() error = %v", err)
			}
		})
	}
}

func TestConfiguredOAuthScopeValidation_CombinesBaseWithSurfaceAlternatives(t *testing.T) {
	client := &OAuthClient{
		Scopes:         []string{"openid", "profile", "email"},
		WebUIScopes:    []string{"ROLE_STEWARD_WEB"},
		MobileUIScopes: []string{"ROLE_STEWARD_MOBILE"},
		CLIScopes:      []string{"ROLE_STEWARD_CLI"},
	}
	tests := []struct {
		name    string
		scopes  string
		wantErr bool
	}{
		{name: "role only rejected", scopes: "ROLE_STEWARD_WEB", wantErr: true},
		{name: "base only rejected when surfaces configured", scopes: "openid profile email", wantErr: true},
		{name: "web alternative", scopes: "openid profile email ROLE_STEWARD_WEB"},
		{name: "mobile alternative", scopes: "openid profile email ROLE_STEWARD_MOBILE"},
		{name: "cli alternative", scopes: "openid profile email ROLE_STEWARD_CLI"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opaque := (&oauth2.Token{
				AccessToken: "opaque-access-token",
				Expiry:      time.Now().Add(time.Hour),
			}).WithExtra(map[string]interface{}{"scope": test.scopes})
			err := validateRefreshedOAuthScopes(client, nil, opaque, "")
			if test.wantErr && err == nil {
				t.Fatal("configured scope validation error = nil, want rejection")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("configured scope validation error = %v", err)
			}
		})
	}
}

func TestConfiguredOAuthScopeValidation_BaseOnlyWithoutSurfaceScopes(t *testing.T) {
	client := &OAuthClient{Scopes: []string{"openid", "profile", "email"}}
	full := (&oauth2.Token{AccessToken: "opaque-full"}).WithExtra(map[string]interface{}{
		"scope": "email openid profile",
	})
	if err := validateRefreshedOAuthScopes(client, nil, full, ""); err != nil {
		t.Fatalf("base-only configured scope validation error = %v", err)
	}
	partial := (&oauth2.Token{AccessToken: "opaque-partial"}).WithExtra(map[string]interface{}{
		"scope": "openid profile",
	})
	if err := validateRefreshedOAuthScopes(client, nil, partial, ""); err == nil {
		t.Fatal("base-only configured scope validation error = nil, want missing email rejection")
	}
}

func TestConfiguredOAuthScopeValidation_JWTFallbackRequiresBaseAndSurface(t *testing.T) {
	client := &OAuthClient{
		Scopes:         []string{"openid", "profile", "email"},
		WebUIScopes:    []string{"ROLE_STEWARD_WEB"},
		MobileUIScopes: []string{"ROLE_STEWARD_MOBILE"},
	}
	roleOnly := fakeJWTWithScopeOnlyClaims(t, map[string]any{"scope": "ROLE_STEWARD_WEB"})
	if err := validateConfiguredOAuthScopes(client, nil, roleOnly); err == nil {
		t.Fatal("JWT fallback accepted role-only token")
	}
	for _, scopes := range []string{
		"openid profile email ROLE_STEWARD_WEB",
		"openid profile email ROLE_STEWARD_MOBILE",
	} {
		token := fakeJWTWithScopeOnlyClaims(t, map[string]any{"scope": scopes})
		if err := validateConfiguredOAuthScopes(client, nil, token); err != nil {
			t.Fatalf("JWT fallback rejected valid configured alternative %q: %v", scopes, err)
		}
	}
}

func TestRefreshedOAuthIDToken_DropsExpiredPreviousToken(t *testing.T) {
	expired := fakeJWTWithClaims(t, map[string]any{
		"exp": time.Now().Add(-time.Minute).Unix(),
	})
	refreshed := &oauth2.Token{
		AccessToken: "fresh-access",
		Expiry:      time.Now().Add(30 * time.Minute),
	}

	if got := refreshedOAuthIDToken(refreshed, expired); got != "" {
		t.Fatalf("refreshedOAuthIDToken() = %q, want empty expired ID token", got)
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
