package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/viant/agently-core/service/auth/providerregistry"
	"github.com/viant/agently-core/workspace/repository/oauthprovider"
	authcfg "github.com/viant/mcp/client/auth/config"
	"golang.org/x/oauth2"
)

func opaqueResponse(subject string, now time.Time) *oauth2.Token {
	return (&oauth2.Token{AccessToken: "opaque-fixture", TokenType: "bearer", RefreshToken: "refresh-fixture", Expiry: now.Add(time.Hour)}).WithExtra(map[string]interface{}{
		"data": map[string]interface{}{"gid": subject},
	})
}

func TestTokenResponseRefreshPreservesOwnerAndRejectsAccountChange(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "valid"
		if changed {
			name = "changed account"
		}
		t.Run(name, func(t *testing.T) {
			store := newFakeDelegatedStore()
			original := seededDev6Token(time.Now().Add(-time.Minute))
			store.seed(original)
			doc := dev6ProviderDoc(false)
			doc.TokenResponse = &oauthprovider.TokenResponse{TokenURL: "https://idp-dev6.example.com/token", Resource: "https://mcp6.example.com/mcp", SubjectPath: "data.gid"}
			resolver := newTestResolver(t, store, doc)
			resolver.loadClientConfig = func(context.Context, string) (*oauth2.Config, error) {
				return &oauth2.Config{ClientID: "fixture", ClientSecret: "secret", Endpoint: oauth2.Endpoint{TokenURL: doc.TokenResponse.TokenURL}}, nil
			}
			resolver.refreshToken = func(_ context.Context, _ *oauth2.Config, base *oauth2.Token, _ []string, resource string) (*oauth2.Token, error) {
				if base.RefreshToken != original.RefreshToken || resource != original.Resource {
					t.Fatal("refresh used wrong grant")
				}
				subject := ""
				if changed {
					subject = "another-user"
				}
				token := opaqueResponse(subject, time.Now())
				token.RefreshToken = "" // Retain the previously encrypted refresh token.
				return token, nil
			}
			_, err := resolver.Resolve(delegatedCtx(original.Username), dev6Requirement())
			if (err != nil) != changed {
				t.Fatalf("refresh error = %v", err)
			}
			stored, err := store.GetExact(context.Background(), original.Username, original.Provider)
			if err != nil || stored == nil {
				t.Fatal("grant disappeared", err)
			}
			if stored.Subject != original.Subject || stored.Username != original.Username || stored.RefreshToken != original.RefreshToken {
				t.Fatal("refresh changed ownership")
			}
			if changed && stored.AccessToken != original.AccessToken {
				t.Fatal("invalid refresh overwrote original token")
			}
			if !changed && stored.AccessToken != "opaque-fixture" {
				t.Fatal("valid refresh not persisted")
			}
		})
	}
}

func TestPinnedTokenExchangeRejectsRedirect(t *testing.T) {
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	ctx := pinnedTokenExchangeContext(context.Background())
	client := ctx.Value(oauth2.HTTPClient).(*http.Client)
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect || hits != 0 {
		t.Fatal("token endpoint redirect followed")
	}
}

func TestValidateTokenResponse(t *testing.T) {
	now := time.Now()
	policy := &oauthprovider.TokenResponse{SubjectPath: "data.gid"}
	requirement := &authcfg.Requirement{Issuer: "https://app.asana.com", TokenType: authcfg.TokenTypeAccessToken}
	for _, tc := range []struct {
		name      string
		mutate    func(*oauth2.Token)
		previous  *OAuthToken
		wantError bool
	}{
		{name: "exchange"},
		{name: "missing subject", mutate: func(tok *oauth2.Token) { *tok = *opaqueResponse("", now) }, wantError: true},
		{name: "expired", mutate: func(tok *oauth2.Token) { tok.Expiry = now.Add(-time.Second) }, wantError: true},
		{name: "no expiry", mutate: func(tok *oauth2.Token) { tok.Expiry = time.Time{} }, wantError: true},
		{name: "wrong token type", mutate: func(tok *oauth2.Token) { tok.TokenType = "mac" }, wantError: true},
		{name: "empty token", mutate: func(tok *oauth2.Token) { tok.AccessToken = "" }, wantError: true},
		{name: "account changed", previous: &OAuthToken{Subject: "other-user"}, wantError: true},
		{name: "refresh retains subject", mutate: func(tok *oauth2.Token) { *tok = *opaqueResponse("", now) }, previous: &OAuthToken{Subject: "user-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := opaqueResponse("user-1", now)
			if tc.mutate != nil {
				tc.mutate(token)
			}
			grant, err := validateTokenResponse(policy, requirement, token, tc.previous, now)
			if (err != nil) != tc.wantError {
				t.Fatalf("validation error = %v", err)
			}
			if err == nil && grant.subject != "user-1" {
				t.Fatal("subject not retained")
			}
		})
	}
	requirement.Scopes = []string{"tasks:read"}
	if _, err := validateTokenResponse(policy, requirement, opaqueResponse("user-1", now), nil, now); err == nil {
		t.Fatal("missing scopes accepted")
	}
	previous := &OAuthToken{Subject: "user-1", Scopes: requirement.Scopes}
	if _, err := validateTokenResponse(policy, requirement, opaqueResponse("user-1", now), previous, now); err != nil {
		t.Fatal(err)
	}
	emptyScopes := opaqueResponse("user-1", now).WithExtra(map[string]interface{}{"scope": "", "data": map[string]interface{}{"gid": "user-1"}})
	if _, err := validateTokenResponse(policy, requirement, emptyScopes, previous, now); err == nil {
		t.Fatal("explicit scope reduction accepted")
	}
}

func TestTokenResponsePolicyPins(t *testing.T) {
	for _, name := range []string{"valid", "http", "wrong endpoint", "wrong resource", "wrong issuer", "id token", "no secret", "disabled"} {
		t.Run(name, func(t *testing.T) {
			doc := dev6ProviderDoc(false)
			doc.TokenResponse = &oauthprovider.TokenResponse{TokenURL: "https://idp-dev6.example.com/token", Resource: "https://mcp6.example.com/mcp", SubjectPath: "data.gid"}
			cfg := &oauth2.Config{ClientID: "fixture", ClientSecret: "fixture-secret", Endpoint: oauth2.Endpoint{TokenURL: doc.TokenResponse.TokenURL}}
			req := dev6Requirement()
			req.Issuer = doc.Issuer
			resolver := newTestResolver(t, newFakeDelegatedStore(), doc)
			resolved, err := resolver.resolveProvider(context.Background(), &req)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "http":
				doc.TokenResponse.TokenURL = "http://idp-dev6.example.com/token"
				cfg.Endpoint.TokenURL = doc.TokenResponse.TokenURL
			case "wrong endpoint":
				cfg.Endpoint.TokenURL = "https://other.example/token"
			case "wrong resource":
				req.Resource = "https://other.example/mcp"
			case "wrong issuer":
				req.Issuer = "https://other.example"
			case "id token":
				req.TokenType = authcfg.TokenTypeIDToken
			case "no secret":
				cfg.ClientSecret = ""
			case "disabled":
				doc.Disabled = true
			}
			resolver.registry.Invalidate()
			_, err = tokenResponsePolicy(context.Background(), resolver.registry, resolved, &req, cfg)
			if (err == nil) != (name == "valid") {
				t.Fatalf("policy error = %v", err)
			}
		})
	}
}

func TestMCPTokenResponseCallbackRejectsImportAndReplay(t *testing.T) {
	mcpConfig := testDelegatedMCPConfig(false)
	registeredProvider := mcpConfig.Auth.InlineProvider
	mcpConfig.Auth.ProviderRef = registeredProvider.ID
	mcpConfig.Auth.InlineProvider = nil
	fixture := newMCPLinkFixtureWithConfig(t, mcpConfig)
	ctx := context.Background()
	provider := testDelegatedMCPConfig(false).Auth.InlineProvider
	doc := &oauthprovider.Document{OAuthProvider: *provider, TokenResponse: &oauthprovider.TokenResponse{
		TokenURL: "https://idp.test/token", Resource: testMCPResource, SubjectPath: "data.gid",
	}}
	registry := providerregistry.NewWithLoader(&fakeProviderLoader{docs: map[string]*oauthprovider.Document{provider.ID: doc}})
	fixture.delegated.registry = registry
	fixture.delegated.resolver.registry = registry
	fixture.service.loadClientConfig = func(context.Context, string) (*oauth2.Config, error) {
		return &oauth2.Config{ClientID: "fixture", ClientSecret: "secret", Endpoint: oauth2.Endpoint{AuthURL: "https://idp.test/authorize", TokenURL: doc.TokenResponse.TokenURL}}, nil
	}
	token := opaqueResponse("asana-user", time.Now()).WithExtra(map[string]interface{}{"data": map[string]interface{}{"gid": "asana-user"}, "scope": "plan:read"})
	exchanges := 0
	fixture.service.exchangeCode = func(_ context.Context, _ *oauth2.Config, _, _, verifier, resource string) (*oauth2.Token, error) {
		if verifier == "" || resource != testMCPResource {
			t.Fatal("exchange lost PKCE/resource binding")
		}
		exchanges++
		return token, nil
	}
	if _, err := fixture.service.completeOOBLink(ctx, testMCPServer, token, fixture.canonical, ""); err == nil {
		t.Fatal("caller-supplied opaque token accepted")
	}
	link, err := fixture.service.resolveServer(ctx, testMCPServer)
	if err != nil {
		t.Fatal(err)
	}
	init, err := fixture.service.initiate(ctx, link, fixture.canonical, fixture.session.ID, "/", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(init.AuthorizationURL)
	state := parsed.Query().Get("state")
	if _, err = fixture.service.completeCallback(ctx, "code", state, fixture.session, fixture.canonical, ""); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.delegated.resolver.store.GetExact(ctx, fixture.canonical, link.resolved.storageKey)
	if err != nil || stored == nil || stored.Subject != "asana-user" || stored.Resource != testMCPResource {
		t.Fatal("bound grant not stored", err)
	}
	if _, err = fixture.service.completeCallback(ctx, "code", state, fixture.session, fixture.canonical, ""); err == nil {
		t.Fatal("callback replay accepted")
	}
	if exchanges != 1 {
		t.Fatalf("exchange calls = %d", exchanges)
	}
}
