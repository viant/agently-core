package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestCheckJWTAlgorithmAllowlist(t *testing.T) {
	makeToken := func(header string) string {
		return base64URLSegment(header) + "." + base64URLSegment(`{"sub":"x"}`) + ".sig"
	}
	if err := checkJWTAlgorithmAllowlist(makeToken(`{"alg":"RS256"}`)); err != nil {
		t.Fatalf("RS256 rejected: %v", err)
	}
	for _, header := range []string{`{"alg":"none"}`, `{"alg":"HS256"}`, `{}`} {
		if err := checkJWTAlgorithmAllowlist(makeToken(header)); err == nil {
			t.Fatalf("header %s passed the algorithm allowlist", header)
		}
	}
	if err := checkJWTAlgorithmAllowlist("opaque-token"); err == nil {
		t.Fatalf("non-JWS token passed the allowlist check")
	}
}

func base64URLSegment(payload string) string {
	return strings.TrimRight(base64RawURL([]byte(payload)), "=")
}

// callbackValidationFixture builds a link service wired for validateGrant
// tests: signature verification is faked (claims pass through) so the tests
// exercise the claim-level policy.
func callbackValidationFixture(t *testing.T) (*mcpLinkService, *resolvedMCPLink) {
	t.Helper()
	fixture := newMCPLinkFixture(t)
	link, err := fixture.service.resolveServer(context.Background(), testMCPServer)
	if err != nil {
		t.Fatalf("resolveServer() error = %v", err)
	}
	return fixture.service, link
}

func TestValidateGrant_OpaqueTokenFailsClosed(t *testing.T) {
	service, link := callbackValidationFixture(t)
	token := &oauth2.Token{AccessToken: "opaque-access-token", Expiry: time.Now().Add(time.Hour)}
	_, err := service.validateGrant(context.Background(), link, token, "nonce")
	if !errors.Is(err, errMCPUnsupportedOpaque) {
		t.Fatalf("opaque grant = %v, want errMCPUnsupportedOpaque (inline providers have no introspection)", err)
	}
}

func TestValidateGrant_IssuerAndScopePolicy(t *testing.T) {
	service, link := callbackValidationFixture(t)
	now := time.Now()

	wrongIssuer := makeTestJWT(t, map[string]interface{}{
		"iss":   "https://rogue.test/",
		"sub":   "subject",
		"aud":   []string{testMCPResource},
		"exp":   now.Add(time.Hour).Unix(),
		"scope": "plan:read",
	})
	if _, err := service.validateGrant(context.Background(), link, (&oauth2.Token{AccessToken: wrongIssuer, Expiry: now.Add(time.Hour)}).WithExtra(map[string]interface{}{"scope": "plan:read"}), ""); err == nil {
		t.Fatalf("issuer mismatch accepted")
	}

	wrongAudience := makeTestJWT(t, map[string]interface{}{
		"iss":   testIssuer,
		"sub":   "subject",
		"aud":   []string{"https://mcp.test/mcp2"},
		"exp":   now.Add(time.Hour).Unix(),
		"scope": "plan:read",
	})
	if _, err := service.validateGrant(context.Background(), link, (&oauth2.Token{AccessToken: wrongAudience, Expiry: now.Add(time.Hour)}).WithExtra(map[string]interface{}{"scope": "plan:read"}), ""); err == nil {
		t.Fatalf("audience /mcp2 satisfied a /mcp requirement; exact matching is broken")
	}

	insufficientScopes := makeTestJWT(t, map[string]interface{}{
		"iss":   testIssuer,
		"sub":   "subject",
		"aud":   []string{testMCPResource},
		"exp":   now.Add(time.Hour).Unix(),
		"scope": "other:scope",
	})
	if _, err := service.validateGrant(context.Background(), link, (&oauth2.Token{AccessToken: insufficientScopes, Expiry: now.Add(time.Hour)}).WithExtra(map[string]interface{}{"scope": "other:scope"}), ""); err == nil {
		t.Fatalf("insufficient scopes accepted")
	}

	expired := makeTestJWT(t, map[string]interface{}{
		"iss":   testIssuer,
		"sub":   "subject",
		"aud":   []string{testMCPResource},
		"exp":   now.Add(-time.Hour).Unix(),
		"scope": "plan:read",
	})
	if _, err := service.validateGrant(context.Background(), link, (&oauth2.Token{AccessToken: expired, Expiry: now.Add(-time.Hour)}).WithExtra(map[string]interface{}{"scope": "plan:read"}), ""); err == nil {
		t.Fatalf("expired token accepted")
	}

	missingSubject := makeTestJWT(t, map[string]interface{}{
		"iss":   testIssuer,
		"aud":   []string{testMCPResource},
		"exp":   now.Add(time.Hour).Unix(),
		"scope": "plan:read",
	})
	if _, err := service.validateGrant(context.Background(), link, (&oauth2.Token{AccessToken: missingSubject, Expiry: now.Add(time.Hour)}).WithExtra(map[string]interface{}{"scope": "plan:read"}), ""); err == nil {
		t.Fatalf("token without subject accepted")
	}
}

func TestMCPLinkCallback_ResourceConflictRejected(t *testing.T) {
	fixture := newMCPLinkFixture(t)
	mux := fixture.mux()
	csrf := fixture.service.keyring.mcpCSRFToken(fixture.session.ID)

	// An existing credential for the same storage key granted for another
	// resource must never be overwritten.
	storageKey := DelegatedProviderStorageKey("default", "test-provider")
	if err := fixture.delegated.resolver.store.Put(context.Background(), &OAuthToken{
		Username:    fixture.canonical,
		Provider:    storageKey,
		AccessToken: "existing-access",
		ExpiresAt:   time.Now().Add(time.Hour),
		Issuer:      testIssuer,
		Resource:    "https://another.test/mcp",
		ProviderRef: "test-provider",
	}); err != nil {
		t.Fatalf("seed Put() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodPost, "/v1/api/auth/mcp/"+testMCPServer+"/initiate", true, csrf))
	initiate := decodeJSONBody(t, recorder)
	authURL, _ := initiate["authorizationURL"].(string)
	parsed, _ := url.Parse(authURL)
	stateBlob := parsed.Query().Get("state")

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodGet, "/v1/api/auth/mcp/callback?code=good-code&state="+url.QueryEscape(stateBlob), true, ""))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("conflicting relink = %d, want 409", recorder.Code)
	}
	// The existing credential is intact.
	stored, err := fixture.delegated.resolver.store.GetExact(context.Background(), fixture.canonical, storageKey)
	if err != nil || stored == nil || stored.AccessToken != "existing-access" {
		t.Fatalf("existing credential was overwritten: %+v, %v", stored, err)
	}
}
