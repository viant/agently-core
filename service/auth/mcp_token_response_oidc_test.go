package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/viant/agently-core/service/auth/providerregistry"
	"github.com/viant/agently-core/workspace/repository/oauthprovider"
	"golang.org/x/oauth2"
)

func oidcResponseFixture(t *testing.T) (*mcpLinkFixture, *sessionOAuthAuthority, *resolvedMCPLink) {
	t.Helper()
	authority := newSessionOAuthAuthority(t)
	config := testDelegatedMCPConfig(false)
	provider := config.Auth.InlineProvider
	provider.Issuer = authority.server.URL
	provider.DiscoveryURL = authority.server.URL + "/discovery"
	config.Auth.Scopes = []string{"openid", "plan:read"}
	config.Auth.ProviderRef = provider.ID
	config.Auth.InlineProvider = nil
	fixture := newMCPLinkFixtureWithConfig(t, config)
	doc := &oauthprovider.Document{OAuthProvider: *provider, TokenResponse: &oauthprovider.TokenResponse{
		TokenURL: "https://issuer.example.test/token", Resource: testMCPResource, SubjectFromIDToken: true,
	}}
	registry := providerregistry.NewWithLoader(&fakeProviderLoader{docs: map[string]*oauthprovider.Document{provider.ID: doc}})
	fixture.delegated.registry = registry
	fixture.delegated.resolver.registry = registry
	fixture.service.loadClientConfig = func(context.Context, string) (*oauth2.Config, error) {
		return &oauth2.Config{ClientID: testClientID, ClientSecret: "synthetic-secret", Endpoint: oauth2.Endpoint{
			AuthURL: "https://issuer.example.test/authorize?access_type=offline&prompt=consent", TokenURL: doc.TokenResponse.TokenURL,
		}}, nil
	}
	fixture.delegated.resolver.loadClientConfig = fixture.service.loadClientConfig
	fixture.service.verifyJWT = fixture.service.defaultVerifyJWT()
	link, err := fixture.service.resolveServer(context.Background(), testMCPServer)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, authority, link
}

func TestOIDCTokenResponseBoundCallbackAndRejection(t *testing.T) {
	for _, scenario := range []string{"valid", "signature", "issuer", "audience", "missing nonce", "nonce", "expiry", "subject", "hash", "empty hash", "party", "not before", "scope", "no identity"} {
		t.Run(scenario, func(t *testing.T) {
			fixture, authority, link := oidcResponseFixture(t)
			ctx := context.Background()
			initial, err := fixture.service.initiate(ctx, link, fixture.canonical, fixture.session.ID, "/", "", false, false)
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(initial.AuthorizationURL)
			if u.Query().Get("access_type") != "offline" || u.Query().Get("prompt") != "consent" {
				t.Fatal("existing authorization parameters were lost")
			}
			claims := authority.claims(testClientID)
			claims["nonce"] = u.Query().Get("nonce")
			hash := sha256.Sum256([]byte("opaque-access-fixture"))
			claims["at_hash"] = base64.RawURLEncoding.EncodeToString(hash[:len(hash)/2])
			scope := "openid plan:read"
			switch scenario {
			case "issuer":
				claims["iss"] = "https://other.example.test"
			case "audience":
				claims["aud"] = "another-client"
			case "missing nonce":
				delete(claims, "nonce")
			case "nonce":
				claims["nonce"] = "another-nonce"
			case "expiry":
				claims["exp"] = time.Now().Add(-time.Hour).Unix()
			case "subject":
				delete(claims, "sub")
			case "hash":
				claims["at_hash"] = "different-access-token"
			case "empty hash":
				claims["at_hash"] = ""
			case "party":
				claims["aud"] = []string{testClientID, "other-client"}
				claims["azp"] = "other-client"
			case "not before":
				claims["nbf"] = time.Now().Add(time.Hour).Unix()
			case "scope":
				scope = "openid"
			}
			identity := authority.sign(t, claims)
			if scenario == "signature" {
				parts := strings.Split(identity, ".")
				parts[2] = base64.RawURLEncoding.EncodeToString([]byte("invalid-signature"))
				identity = strings.Join(parts, ".")
			}
			extra := map[string]interface{}{"scope": scope, "id_token": identity}
			if scenario == "no identity" {
				delete(extra, "id_token")
			}
			token := (&oauth2.Token{AccessToken: "opaque-access-fixture", TokenType: "Bearer", RefreshToken: "opaque-refresh-fixture", Expiry: time.Now().Add(time.Hour)}).WithExtra(extra)
			exchanges := 0
			fixture.service.exchangeCode = func(_ context.Context, _ *oauth2.Config, _, _, verifier, resource string) (*oauth2.Token, error) {
				if verifier == "" || resource != testMCPResource {
					t.Fatal("bound exchange lost scope")
				}
				exchanges++
				return token, nil
			}
			if _, err := fixture.service.completeOOBLink(ctx, testMCPServer, token, fixture.canonical, ""); err == nil {
				t.Fatal("caller-supplied opaque credential bypassed the server exchange")
			}
			_, err = fixture.service.completeCallback(ctx, "synthetic-code", u.Query().Get("state"), fixture.session, fixture.canonical, "")
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("callback success=%v for %s", err == nil, scenario)
			}
			stored, readErr := fixture.delegated.resolver.store.GetExact(ctx, fixture.canonical, link.resolved.storageKey)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if scenario == "valid" {
				if stored == nil || stored.Subject != "user-123" || stored.Username != fixture.canonical || stored.AccessToken != token.AccessToken {
					t.Fatal("verified grant lost its canonical owner")
				}
			} else if stored != nil {
				t.Fatal("rejected identity was persisted")
			}
			if _, err := fixture.service.completeCallback(ctx, "synthetic-code", u.Query().Get("state"), fixture.session, fixture.canonical, ""); err == nil || exchanges != 1 {
				t.Fatal("callback replay reached the token exchange")
			}
		})
	}
}

func TestOIDCTokenResponseRefreshPreservesIdentityAndUserIsolation(t *testing.T) {
	for _, scenario := range []string{"omitted identity", "verified identity", "changed identity", "malformed identity"} {
		t.Run(scenario, func(t *testing.T) {
			fixture, authority, link := oidcResponseFixture(t)
			ctx := context.Background()
			initial, err := fixture.service.initiate(ctx, link, fixture.canonical, fixture.session.ID, "/", "", false, false)
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(initial.AuthorizationURL)
			claims := authority.claims(testClientID)
			claims["nonce"] = u.Query().Get("nonce")
			fixture.service.exchangeCode = func(context.Context, *oauth2.Config, string, string, string, string) (*oauth2.Token, error) {
				return (&oauth2.Token{AccessToken: "original-opaque", RefreshToken: "original-refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}).WithExtra(map[string]interface{}{"scope": "openid plan:read", "id_token": authority.sign(t, claims)}), nil
			}
			if _, err = fixture.service.completeCallback(ctx, "code", u.Query().Get("state"), fixture.session, fixture.canonical, ""); err != nil {
				t.Fatal(err)
			}
			store := fixture.delegated.resolver.store
			stored, err := store.GetExact(ctx, fixture.canonical, link.resolved.storageKey)
			if err != nil || stored == nil {
				t.Fatal("initial verified grant missing", err)
			}
			stored.ExpiresAt = time.Now().Add(-time.Minute)
			if err := store.Put(ctx, stored); err != nil {
				t.Fatal(err)
			}
			refreshes := 0
			fixture.delegated.resolver.refreshToken = func(context.Context, *oauth2.Config, *oauth2.Token, []string, string) (*oauth2.Token, error) {
				refreshes++
				extra := map[string]interface{}{}
				if scenario == "verified identity" || scenario == "changed identity" {
					identity := authority.claims(testClientID)
					if scenario == "changed identity" {
						identity["sub"] = "another-user"
					}
					extra["id_token"] = authority.sign(t, identity)
				}
				if scenario == "malformed identity" {
					extra["id_token"] = "unsigned-subject"
				}
				return (&oauth2.Token{AccessToken: "refreshed-opaque", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}).WithExtra(extra), nil
			}
			if _, err := fixture.delegated.resolver.Resolve(delegatedCtx("another-user"), *link.requirement); err == nil || refreshes != 0 {
				t.Fatal("another workspace user used this connection")
			}
			_, err = fixture.delegated.resolver.Resolve(delegatedCtx(fixture.canonical), *link.requirement)
			wantOK := scenario == "omitted identity" || scenario == "verified identity"
			if (err == nil) != wantOK || refreshes != 1 {
				t.Fatalf("refresh success=%v calls=%d", err == nil, refreshes)
			}
			next, err := store.GetExact(ctx, fixture.canonical, link.resolved.storageKey)
			if err != nil || next == nil || next.Username != fixture.canonical || next.Subject != stored.Subject || next.RefreshToken != "original-refresh" {
				t.Fatal("refresh changed connection ownership", err)
			}
			if wantOK && next.AccessToken != "refreshed-opaque" {
				t.Fatal("valid refresh not stored")
			}
			if !wantOK && next.AccessToken != "original-opaque" {
				t.Fatal("invalid refresh replaced the original grant")
			}
		})
	}
}

func TestOIDCTokenResponsePolicyRequiresExplicitIdentityAndScope(t *testing.T) {
	for _, scenario := range []string{"valid", "both identity sources", "neither identity source", "missing openid"} {
		t.Run(scenario, func(t *testing.T) {
			doc := sampleProviderDoc(false)
			doc.TokenResponse = &oauthprovider.TokenResponse{TokenURL: "https://idp.example.invalid/token", Resource: "https://mcp.example.invalid/mcp", SubjectFromIDToken: true}
			request := sampleRequirement()
			request.Issuer = doc.Issuer
			request.Scopes = append(request.Scopes, "openid")
			switch scenario {
			case "both identity sources":
				doc.TokenResponse.SubjectPath = "data.gid"
			case "neither identity source":
				doc.TokenResponse.SubjectFromIDToken = false
			case "missing openid":
				request.Scopes = []string{"plan:read"}
			}
			resolver := newTestResolver(t, newFakeDelegatedStore(), doc)
			resolved, err := resolver.resolveProvider(context.Background(), &request)
			if err != nil {
				t.Fatal(err)
			}
			client := &oauth2.Config{ClientID: "fixture", ClientSecret: "synthetic-secret", Endpoint: oauth2.Endpoint{TokenURL: doc.TokenResponse.TokenURL}}
			_, err = tokenResponsePolicy(context.Background(), resolver.registry, resolved, &request, client)
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("policy success=%v for %s", err == nil, scenario)
			}
		})
	}
}
