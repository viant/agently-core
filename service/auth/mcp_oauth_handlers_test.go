package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	oauthwrite "github.com/viant/agently-core/pkg/agently/user/oauth/write"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/datly"
	mcp "github.com/viant/mcp"
	authcfg "github.com/viant/mcp/client/auth/config"
	"golang.org/x/oauth2"
)

const (
	testMCPServer   = "viant-mcp-dev6"
	testMCPResource = "https://mcp.test/mcp"
	testIssuer      = "https://idp.test/"
	testClientID    = "client-123"
)

// fakeMCPConfigProvider serves one delegated MCP definition.
type fakeMCPConfigProvider struct {
	configs map[string]*mcpcfg.MCPClient
}

func (p *fakeMCPConfigProvider) Options(_ context.Context, name string) (*mcpcfg.MCPClient, error) {
	cfg, ok := p.configs[strings.TrimSpace(name)]
	if !ok {
		return nil, fmt.Errorf("mcp %q not found", name)
	}
	return cfg, nil
}

func testDelegatedMCPConfig(disabled bool) *mcpcfg.MCPClient {
	provider := &authcfg.OAuthProvider{
		ID:            "test-provider",
		Issuer:        testIssuer,
		DefaultClient: "web",
		Clients: map[string]*authcfg.OAuthClient{
			"web": {
				ConfigURL:    "mem://oauth/client.json",
				RedirectURI:  "https://app.test/v1/api/auth/mcp/callback",
				Confidential: true,
				UsePKCE:      true,
			},
		},
	}
	options := &mcp.ClientOptions{Name: testMCPServer}
	options.Transport.Type = "streamable"
	options.Transport.URL = testMCPResource
	options.Auth = &mcp.ClientAuth{
		Mode:           authcfg.ModeOAuth,
		InlineProvider: provider,
		Resource:       testMCPResource,
		Scopes:         []string{"plan:read"},
		TokenType:      string(authcfg.TokenTypeAccessToken),
	}
	return &mcpcfg.MCPClient{ClientOptions: options, DisableDelegatedAuth: disabled}
}

func makeTestJWT(t *testing.T, claims map[string]interface{}) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

type mcpLinkFixture struct {
	ext       *authExtension
	service   *mcpLinkService
	delegated *DelegatedMCPAuth
	dao       *datly.Service
	canonical string
	session   *Session
}

func newMCPLinkFixture(t *testing.T) *mcpLinkFixture {
	t.Helper()
	ctx := context.Background()
	dao := newMCPLinkTestDAO(t)
	if err := DefineOAuthLinkStateComponents(ctx, dao); err != nil {
		t.Fatalf("DefineOAuthLinkStateComponents() error = %v", err)
	}
	if _, err := oauthwrite.DefineComponent(ctx, dao); err != nil {
		t.Fatalf("oauth write DefineComponent() error = %v", err)
	}
	cfg := &Config{
		Enabled:            true,
		CookieName:         "agently_session",
		TokenEncryptionKey: "unit-test-encryption-key",
	}
	users := NewDatlyUserService(dao)
	// The in-memory schema is shared process-wide; a per-test identity keeps
	// credentials from leaking between fixtures.
	unique := strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	canonical, err := users.UpsertWithProvider(ctx, "linkuser-"+unique, "Link User", unique+"@example.test", "oauth", "link-subject-"+unique)
	if err != nil || canonical == "" {
		t.Fatalf("UpsertWithProvider() = %q, %v", canonical, err)
	}
	delegated := NewDelegatedMCPAuth(cfg, dao)
	if delegated == nil {
		t.Fatalf("NewDelegatedMCPAuth() = nil")
	}
	delegated.SetUserLookup(users)
	states := NewOAuthStateStoreDatly(dao)
	configs := &fakeMCPConfigProvider{configs: map[string]*mcpcfg.MCPClient{testMCPServer: testDelegatedMCPConfig(false)}}
	service := newMCPLinkService(cfg, delegated, states, configs, users)
	if service == nil {
		t.Fatalf("newMCPLinkService() = nil")
	}
	service.loadClientConfig = func(_ context.Context, configURL string) (*oauth2.Config, error) {
		return &oauth2.Config{
			ClientID: testClientID,
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://idp.test/authorize",
				TokenURL: "https://idp.test/token",
			},
		}, nil
	}
	service.verifyJWT = func(_ context.Context, _ *resolvedProvider, token string) (map[string]interface{}, error) {
		if err := checkJWTAlgorithmAllowlist(token); err != nil {
			return nil, err
		}
		claims := parseJWTClaims(token)
		if len(claims) == 0 {
			return nil, fmt.Errorf("no claims")
		}
		return claims, nil
	}
	now := time.Now()
	accessToken := makeTestJWT(t, map[string]interface{}{
		"iss":   testIssuer,
		"sub":   "provider-subject-1",
		"aud":   []string{testMCPResource},
		"exp":   now.Add(time.Hour).Unix(),
		"iat":   now.Unix(),
		"scope": "plan:read",
	})
	idToken := makeTestJWT(t, map[string]interface{}{
		"iss": testIssuer,
		"sub": "provider-subject-1",
		"aud": testClientID,
		"exp": now.Add(2 * time.Hour).Unix(),
		"iat": now.Unix(),
	})
	service.exchangeCode = func(_ context.Context, _ *oauth2.Config, code, redirectURI, codeVerifier string) (*oauth2.Token, error) {
		if code != "good-code" {
			return nil, fmt.Errorf("invalid code")
		}
		if strings.TrimSpace(codeVerifier) == "" {
			return nil, fmt.Errorf("missing pkce verifier")
		}
		token := &oauth2.Token{
			AccessToken:  accessToken,
			RefreshToken: "refresh-1",
			Expiry:       now.Add(time.Hour),
		}
		return token.WithExtra(map[string]interface{}{"id_token": idToken, "scope": "plan:read"}), nil
	}
	sessions := NewManager(time.Hour, nil)
	ext := newAuthExtension(cfg, sessions, "", nil, users)
	ext.mcpLink = service
	sess := &Session{
		ID:        "sess-link-1",
		UserID:    canonical,
		Username:  "linkuser",
		Subject:   "link-subject",
		Provider:  "oauth",
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	sessions.Put(ctx, sess)
	return &mcpLinkFixture{ext: ext, service: service, delegated: delegated, dao: dao, canonical: canonical, session: sess}
}

func (f *mcpLinkFixture) mux() *http.ServeMux {
	mux := http.NewServeMux()
	f.ext.Register(mux)
	return mux
}

func (f *mcpLinkFixture) request(method, target string, cookie bool, csrf string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	if cookie {
		r.AddCookie(&http.Cookie{Name: "agently_session", Value: f.session.ID})
	}
	if csrf != "" {
		r.Header.Set(MCPLinkCSRFHeader, csrf)
	}
	return r
}

func decodeJSONBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	payload := map[string]interface{}{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response %q is not JSON: %v", recorder.Body.String(), err)
	}
	return payload
}

func TestMCPLinkEndpoints_AuthenticationAndCSRF(t *testing.T) {
	fixture := newMCPLinkFixture(t)
	mux := fixture.mux()

	// Unauthenticated initiate: 401 (the /v1/api/auth prefix bypasses the
	// middleware, so the handler itself must reject).
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodPost, "/v1/api/auth/mcp/"+testMCPServer+"/initiate", false, ""))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated initiate = %d, want 401", recorder.Code)
	}

	// Bearer-only callers receive unsupported_flow.
	recorder = httptest.NewRecorder()
	request := fixture.request(http.MethodPost, "/v1/api/auth/mcp/"+testMCPServer+"/initiate", false, "")
	request.Header.Set("Authorization", "Bearer some-token")
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("bearer initiate = %d, want 400", recorder.Code)
	}
	if payload := decodeJSONBody(t, recorder); payload["code"] != "unsupported_flow" {
		t.Fatalf("bearer initiate code = %v, want unsupported_flow", payload["code"])
	}

	// Cookie session without CSRF: 403.
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodPost, "/v1/api/auth/mcp/"+testMCPServer+"/initiate", true, ""))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("initiate without CSRF = %d, want 403", recorder.Code)
	}

	// Disconnect requires CSRF too.
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodDelete, "/v1/api/auth/mcp/"+testMCPServer, true, ""))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("disconnect without CSRF = %d, want 403", recorder.Code)
	}
}

func TestMCPLinkEndpoints_FullLinkFlow(t *testing.T) {
	fixture := newMCPLinkFixture(t)
	mux := fixture.mux()

	// Status yields the CSRF token and starts unlinked.
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodGet, "/v1/api/auth/mcp/"+testMCPServer+"/status", true, ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	status := decodeJSONBody(t, recorder)
	if connected, _ := status["connected"].(bool); connected {
		t.Fatalf("fresh status connected = true, want false")
	}
	csrf, _ := status["csrfToken"].(string)
	if csrf == "" {
		t.Fatalf("status carries no csrfToken")
	}

	// Initiate: the owner receives the authorization URL.
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodPost, "/v1/api/auth/mcp/"+testMCPServer+"/initiate", true, csrf))
	if recorder.Code != http.StatusOK {
		t.Fatalf("initiate = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	initiate := decodeJSONBody(t, recorder)
	if initiate["status"] != "connect" {
		t.Fatalf("initiate status = %v, want connect", initiate["status"])
	}
	authURL, _ := initiate["authorizationURL"].(string)
	if authURL == "" {
		t.Fatalf("initiate returned no authorizationURL")
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("authorizationURL parse error: %v", err)
	}
	stateBlob := parsed.Query().Get("state")
	if stateBlob == "" {
		t.Fatalf("authorization URL carries no state")
	}
	if parsed.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization URL is not S256 PKCE: %s", authURL)
	}

	// A concurrent initiation (same user/provider/resource/scopes) is pending
	// and receives no state blob or URL.
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodPost, "/v1/api/auth/mcp/"+testMCPServer+"/initiate", true, csrf))
	if recorder.Code != http.StatusOK {
		t.Fatalf("second initiate = %d", recorder.Code)
	}
	pending := decodeJSONBody(t, recorder)
	if pending["status"] != "pending" {
		t.Fatalf("second initiate status = %v, want pending", pending["status"])
	}
	if _, hasURL := pending["authorizationURL"]; hasURL {
		t.Fatalf("pending response leaks an authorization URL")
	}

	// Status now reports the pending flow.
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodGet, "/v1/api/auth/mcp/"+testMCPServer+"/status", true, ""))
	status = decodeJSONBody(t, recorder)
	if pendingFlag, _ := status["pending"].(bool); !pendingFlag {
		t.Fatalf("status pending = %v, want true", status["pending"])
	}

	// Callback with the provider redirect completes the link.
	callbackTarget := "/v1/api/auth/mcp/callback?code=good-code&state=" + url.QueryEscape(stateBlob)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodGet, callbackTarget, true, ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("callback = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); strings.Contains(body, "refresh-1") || strings.Contains(body, "good-code") {
		t.Fatalf("callback response leaks token or code material")
	}

	// The credential is persisted under the canonical user with full
	// delegated metadata, including the verified ID-token expiry.
	storageKey := DelegatedProviderStorageKey("default", "test-provider")
	stored, err := fixture.delegated.resolver.store.GetExact(context.Background(), fixture.canonical, storageKey)
	if err != nil || stored == nil {
		t.Fatalf("GetExact() = %+v, %v; want persisted credential", stored, err)
	}
	if stored.Subject != "provider-subject-1" || stored.Issuer == "" || stored.Resource != testMCPResource {
		t.Fatalf("stored metadata incomplete: %+v", stored)
	}
	if stored.IDTokenExpiresAt.IsZero() || stored.IssuedAt.IsZero() {
		t.Fatalf("stored expiries incomplete: idTokenExp=%v issuedAt=%v", stored.IDTokenExpiresAt, stored.IssuedAt)
	}
	if !scopesCover(stored.Scopes, []string{"plan:read"}) {
		t.Fatalf("stored scopes = %v, want plan:read coverage", stored.Scopes)
	}

	// Replayed callback state is rejected.
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodGet, callbackTarget, true, ""))
	if recorder.Code == http.StatusOK {
		t.Fatalf("replayed callback succeeded; single-use state is broken")
	}

	// Status reports connected with scopes and expiry.
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodGet, "/v1/api/auth/mcp/"+testMCPServer+"/status", true, ""))
	status = decodeJSONBody(t, recorder)
	if connected, _ := status["connected"].(bool); !connected {
		t.Fatalf("post-link status connected = false, body=%v", status)
	}
	if status["provider"] != "test-provider" {
		t.Fatalf("post-link status provider = %v", status["provider"])
	}
	if status["expiresAt"] == "" {
		t.Fatalf("post-link status carries no expiry")
	}

	// Disconnect removes only the delegated credential and stays idempotent.
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodDelete, "/v1/api/auth/mcp/"+testMCPServer, true, csrf))
	if recorder.Code != http.StatusOK {
		t.Fatalf("disconnect = %d", recorder.Code)
	}
	stored, err = fixture.delegated.resolver.store.GetExact(context.Background(), fixture.canonical, storageKey)
	if err != nil {
		t.Fatalf("GetExact() after disconnect error = %v", err)
	}
	if stored != nil {
		t.Fatalf("credential survived disconnect: %+v", stored)
	}
	// The workspace session is untouched.
	if fixture.ext.sessions.Get(context.Background(), fixture.session.ID) == nil {
		t.Fatalf("workspace session was removed by disconnect")
	}
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodDelete, "/v1/api/auth/mcp/"+testMCPServer, true, csrf))
	if recorder.Code != http.StatusOK {
		t.Fatalf("repeated disconnect = %d, want 200 (idempotent)", recorder.Code)
	}
}

func TestMCPLinkEndpoints_CallbackCrossSessionRejected(t *testing.T) {
	fixture := newMCPLinkFixture(t)
	mux := fixture.mux()
	csrf := fixture.service.keyring.mcpCSRFToken(fixture.session.ID)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodPost, "/v1/api/auth/mcp/"+testMCPServer+"/initiate", true, csrf))
	initiate := decodeJSONBody(t, recorder)
	authURL, _ := initiate["authorizationURL"].(string)
	parsed, _ := url.Parse(authURL)
	stateBlob := parsed.Query().Get("state")
	if stateBlob == "" {
		t.Fatalf("no state in authorization URL")
	}

	// A different workspace session cannot complete the flow.
	other := &Session{
		ID:        "sess-other",
		UserID:    fixture.canonical,
		Username:  "linkuser",
		Subject:   "link-subject",
		Provider:  "oauth",
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	fixture.ext.sessions.Put(context.Background(), other)
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/api/auth/mcp/callback?code=good-code&state="+url.QueryEscape(stateBlob), nil)
	request.AddCookie(&http.Cookie{Name: "agently_session", Value: other.ID})
	mux.ServeHTTP(recorder, request)
	if recorder.Code == http.StatusOK {
		t.Fatalf("cross-session callback succeeded; session binding is broken")
	}

	// The owner session still completes: the failed cross-session attempt did
	// not consume the state.
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodGet, "/v1/api/auth/mcp/callback?code=good-code&state="+url.QueryEscape(stateBlob), true, ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("owner callback after cross-session attempt = %d, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMCPLinkEndpoints_NonEnumerableStatus(t *testing.T) {
	fixture := newMCPLinkFixture(t)
	mux := fixture.mux()

	shapes := map[string]map[string]interface{}{}
	for _, server := range []string{testMCPServer, "unknown-server"} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, fixture.request(http.MethodGet, "/v1/api/auth/mcp/"+server+"/status", true, ""))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status %q = %d, want 200", server, recorder.Code)
		}
		shapes[server] = decodeJSONBody(t, recorder)
	}
	// Unknown servers and unlinked users share the same external shape.
	for server, shape := range shapes {
		if connected, _ := shape["connected"].(bool); connected {
			t.Fatalf("status %q connected = true", server)
		}
		if _, ok := shape["provider"]; ok {
			t.Fatalf("status %q leaks a provider for a non-connected credential", server)
		}
	}
}

func TestMCPLinkEndpoints_ProviderDisabledKillSwitch(t *testing.T) {
	fixture := newMCPLinkFixture(t)
	fixture.service.configs = &fakeMCPConfigProvider{configs: map[string]*mcpcfg.MCPClient{testMCPServer: testDelegatedMCPConfig(true)}}
	mux := fixture.mux()
	csrf := fixture.service.keyring.mcpCSRFToken(fixture.session.ID)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodPost, "/v1/api/auth/mcp/"+testMCPServer+"/initiate", true, csrf))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("kill-switched initiate = %d, want 403", recorder.Code)
	}
	if payload := decodeJSONBody(t, recorder); payload["code"] != "provider_disabled" {
		t.Fatalf("kill-switched initiate code = %v, want provider_disabled", payload["code"])
	}
}

func TestMCPLinkEndpoints_RateLimited(t *testing.T) {
	fixture := newMCPLinkFixture(t)
	fixture.service.limits.initiateUser = newFixedWindowLimiter(1, time.Minute)
	mux := fixture.mux()
	csrf := fixture.service.keyring.mcpCSRFToken(fixture.session.ID)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodPost, "/v1/api/auth/mcp/"+testMCPServer+"/initiate", true, csrf))
	if recorder.Code != http.StatusOK {
		t.Fatalf("first initiate = %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodPost, "/v1/api/auth/mcp/"+testMCPServer+"/initiate", true, csrf))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited initiate = %d, want 429", recorder.Code)
	}
}

func TestMCPLinkEndpoints_DisabledUserCannotLink(t *testing.T) {
	fixture := newMCPLinkFixture(t)
	// Disable the canonical user directly in the store.
	conn, err := fixture.dao.Resource().Connector("agently")
	if err != nil {
		t.Fatalf("Connector() error = %v", err)
	}
	db, err := conn.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE users SET disabled = 1 WHERE id = ?`, fixture.canonical); err != nil {
		t.Fatalf("disable user error = %v", err)
	}
	mux := fixture.mux()
	csrf := fixture.service.keyring.mcpCSRFToken(fixture.session.ID)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, fixture.request(http.MethodPost, "/v1/api/auth/mcp/"+testMCPServer+"/initiate", true, csrf))
	if recorder.Code == http.StatusOK {
		t.Fatalf("disabled user initiated a link flow")
	}
}
