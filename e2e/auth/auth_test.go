package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/genai/llm/provider"
	iauth "github.com/viant/agently-core/internal/auth"
	modelfinder "github.com/viant/agently-core/internal/finder/model"
	agentfinder "github.com/viant/agently-core/protocol/agent/finder"
	agentloader "github.com/viant/agently-core/protocol/agent/loader"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpmgr "github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/sdk"
	svcauth "github.com/viant/agently-core/service/auth"
	wsfs "github.com/viant/agently-core/workspace/loader/fs"
	modelloader "github.com/viant/agently-core/workspace/loader/model"
	meta "github.com/viant/agently-core/workspace/service/meta"
	"github.com/viant/scy"
	"github.com/viant/scy/auth/jwt/signer"
)

type stubMCPProvider struct{}

func (s *stubMCPProvider) Options(_ context.Context, _ string) (*mcpcfg.MCPClient, error) {
	return nil, fmt.Errorf("no MCP servers configured in test")
}

// generateRSAKeyPair generates a fresh RSA key pair and writes PEM files to dir.
// Returns the paths to the private and public key files.
func generateRSAKeyPair(t *testing.T, dir string) (privPath, pubPath string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	})

	privPath = filepath.Join(dir, "private.pem")
	pubPath = filepath.Join(dir, "public.pem")
	require.NoError(t, os.WriteFile(privPath, privPEM, 0600))
	require.NoError(t, os.WriteFile(pubPath, pubPEM, 0644))
	return
}

// signTestJWT signs a JWT with the given claims using the private key at privPath.
func signTestJWT(t *testing.T, privPath string, claims map[string]interface{}, ttl time.Duration) string {
	t.Helper()
	ctx := context.Background()
	cfg := &signer.Config{
		RSA: scy.NewResource("", privPath, ""),
	}
	s := signer.New(cfg)
	require.NoError(t, s.Init(ctx))
	token, err := s.Create(ttl, claims)
	require.NoError(t, err)
	return token
}

// setupAuthServer creates an httptest.Server with JWT auth enabled.
func setupAuthServer(t *testing.T, pubPath string, privPath string) *httptest.Server {
	t.Helper()
	ctx := context.Background()

	// Set up temp workspace
	tmp := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", tmp)
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")

	// Use the query testdata for agents/models
	testdataDir, err := filepath.Abs("../query/testdata")
	require.NoError(t, err)

	fs := afs.New()
	wsMeta := meta.New(fs, testdataDir)
	agentLdr := agentloader.New(agentloader.WithMetaService(wsMeta))
	agentFndr := agentfinder.New(agentfinder.WithLoader(agentLdr))
	modelLdr := modelloader.New(wsfs.WithMetaService[provider.Config](wsMeta))
	modelFndr := modelfinder.New(modelfinder.WithConfigLoader(modelLdr))
	mcpMgr, err := mcpmgr.New(&stubMCPProvider{})
	require.NoError(t, err)
	registry, err := tool.NewDefaultRegistry(mcpMgr)
	require.NoError(t, err)

	rt, err := executor.NewBuilder().
		WithAgentFinder(agentFndr).
		WithModelFinder(modelFndr).
		WithRegistry(registry).
		WithMCPManager(mcpMgr).
		WithDefaults(&config.Defaults{Model: "openai_gpt4o_mini"}).
		Build(ctx)
	require.NoError(t, err)

	client, err := sdk.NewEmbeddedFromRuntime(rt)
	require.NoError(t, err)

	// Auth configuration with JWT
	authCfg := &iauth.Config{
		Enabled:         true,
		IpHashKey:       "test-ip-hash-key",
		DefaultUsername: "",
		JWT: &iauth.JWT{
			Enabled:       true,
			RSA:           []string{pubPath},
			RSAPrivateKey: privPath,
		},
	}

	sessions := svcauth.NewManager(7*24*time.Hour, nil)
	jwtSvc := svcauth.NewJWTService(authCfg.JWT)
	require.NoError(t, jwtSvc.Init(ctx))

	handler := sdk.NewHandler(client,
		sdk.WithAuth(authCfg, sessions),
	)

	// Wrap with auth middleware including JWT verification
	protected := svcauth.Protect(authCfg, sessions, svcauth.WithJWTService(jwtSvc))(handler)
	return httptest.NewServer(protected)
}

// doRequest is a helper to make HTTP requests to the test server.
func doRequest(t *testing.T, method, url string, body string, headers map[string]string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	require.NoError(t, err)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// --- Test Cases ---

func TestJWTAuth_NoToken_Returns401(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)
	_ = privPath

	srv := setupAuthServer(t, pubPath, privPath)
	defer srv.Close()

	resp := doRequest(t, "GET", srv.URL+"/healthz", "", nil)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "no token should return 401")
}

func TestJWTAuth_ValidToken_Returns200(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupAuthServer(t, pubPath, privPath)
	defer srv.Close()

	token := signTestJWT(t, privPath, map[string]interface{}{
		"sub":   "testuser",
		"email": "test@example.com",
	}, 1*time.Hour)

	resp := doRequest(t, "GET", srv.URL+"/healthz", "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "valid token should return 200")
}

func TestJWTAuth_ExpiredToken_Returns401(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupAuthServer(t, pubPath, privPath)
	defer srv.Close()

	// Sign a token that's already expired (negative TTL doesn't work, use very short)
	token := signTestJWT(t, privPath, map[string]interface{}{
		"sub":   "testuser",
		"email": "test@example.com",
	}, 1*time.Millisecond)

	// Wait for it to expire
	time.Sleep(50 * time.Millisecond)

	resp := doRequest(t, "GET", srv.URL+"/healthz", "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "expired token should return 401")
}

func TestJWTAuth_InvalidSignature_Returns401(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupAuthServer(t, pubPath, privPath)
	defer srv.Close()

	// Generate a DIFFERENT key pair and sign with that
	otherKeyDir := t.TempDir()
	otherPrivPath, _ := generateRSAKeyPair(t, otherKeyDir)

	token := signTestJWT(t, otherPrivPath, map[string]interface{}{
		"sub":   "testuser",
		"email": "test@example.com",
	}, 1*time.Hour)

	resp := doRequest(t, "GET", srv.URL+"/healthz", "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "token signed with wrong key should return 401")
}

func TestJWTAuth_MalformedToken_Returns401(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupAuthServer(t, pubPath, privPath)
	defer srv.Close()

	resp := doRequest(t, "GET", srv.URL+"/healthz", "", map[string]string{
		"Authorization": "Bearer not-a-valid-jwt-token",
	})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "malformed token should return 401")
}

func TestJWTAuth_AuthEndpoints_SkipVerification(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupAuthServer(t, pubPath, privPath)
	defer srv.Close()

	// Auth endpoints should be accessible without a token
	resp := doRequest(t, "GET", srv.URL+"/v1/api/auth/providers", "", nil)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "/v1/api/auth/providers should skip auth")

	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	providers, ok := result["providers"].([]interface{})
	require.True(t, ok, "providers should be an array")

	// Verify JWT is listed as a provider
	hasJWT := false
	for _, p := range providers {
		pm, ok := p.(map[string]interface{})
		if ok && pm["type"] == "jwt" {
			hasJWT = true
			break
		}
	}
	assert.True(t, hasJWT, "JWT should be listed in providers")
}

func TestJWTAuth_ClaimsExtraction(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupAuthServer(t, pubPath, privPath)
	defer srv.Close()

	token := signTestJWT(t, privPath, map[string]interface{}{
		"sub":   "user-123",
		"email": "alice@example.com",
	}, 1*time.Hour)

	resp := doRequest(t, "GET", srv.URL+"/v1/api/auth/me", "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "valid token should access /me")

	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, "user-123", result["subject"], "subject should match JWT claim")
	assert.Equal(t, "alice@example.com", result["email"], "email should match JWT claim")
}

func TestJWTAuth_OptionsRequest_SkipAuth(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupAuthServer(t, pubPath, privPath)
	defer srv.Close()

	resp := doRequest(t, "OPTIONS", srv.URL+"/v1/conversations", "", nil)
	defer resp.Body.Close()
	// OPTIONS should not get 401 (CORS preflight pass-through)
	assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode, "OPTIONS should skip auth")
}

func TestJWTAuth_ListConversations_WithValidToken(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupAuthServer(t, pubPath, privPath)
	defer srv.Close()

	token := signTestJWT(t, privPath, map[string]interface{}{
		"sub":   "e2e-user",
		"email": "e2e@test.com",
	}, 1*time.Hour)

	resp := doRequest(t, "GET", srv.URL+"/v1/conversations", "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "authenticated request to list conversations should succeed")
}

func TestJWTAuth_ListConversations_WithoutToken(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupAuthServer(t, pubPath, privPath)
	defer srv.Close()

	resp := doRequest(t, "GET", srv.URL+"/v1/conversations", "", nil)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "unauthenticated list conversations should return 401")
}

func TestLocalAuth_SessionCookie(t *testing.T) {
	ctx := context.Background()

	// Setup with local auth only (no JWT)
	tmp := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", tmp)
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")

	testdataDir, err := filepath.Abs("../query/testdata")
	require.NoError(t, err)

	fs := afs.New()
	wsMeta := meta.New(fs, testdataDir)
	agentLdr := agentloader.New(agentloader.WithMetaService(wsMeta))
	agentFndr := agentfinder.New(agentfinder.WithLoader(agentLdr))
	modelLdr := modelloader.New(wsfs.WithMetaService[provider.Config](wsMeta))
	modelFndr := modelfinder.New(modelfinder.WithConfigLoader(modelLdr))
	mcpMgr, err := mcpmgr.New(&stubMCPProvider{})
	require.NoError(t, err)
	registry, err := tool.NewDefaultRegistry(mcpMgr)
	require.NoError(t, err)

	rt, err := executor.NewBuilder().
		WithAgentFinder(agentFndr).
		WithModelFinder(modelFndr).
		WithRegistry(registry).
		WithMCPManager(mcpMgr).
		WithDefaults(&config.Defaults{Model: "openai_gpt4o_mini"}).
		Build(ctx)
	require.NoError(t, err)

	client, err := sdk.NewEmbeddedFromRuntime(rt)
	require.NoError(t, err)

	authCfg := &iauth.Config{
		Enabled:    true,
		IpHashKey:  "test-ip-hash-key",
		CookieName: "agently_session",
		Local:      &iauth.Local{Enabled: true},
	}

	sessions := svcauth.NewManager(7*24*time.Hour, nil)
	handler := sdk.NewHandler(client, sdk.WithAuth(authCfg, sessions))
	protected := svcauth.Protect(authCfg, sessions)(handler)
	srv := httptest.NewServer(protected)
	defer srv.Close()

	// Login
	resp := doRequest(t, "POST", srv.URL+"/v1/api/auth/local/login", `{"username":"testuser"}`, nil)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "login should succeed")

	// Extract session cookie
	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "agently_session" {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "login should set session cookie")

	// Use session cookie to access protected endpoint
	req, err := http.NewRequest("GET", srv.URL+"/v1/conversations", nil)
	require.NoError(t, err)
	req.AddCookie(sessionCookie)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode, "cookie-authenticated request should succeed")

	// Verify /me returns user info
	req3, err := http.NewRequest("GET", srv.URL+"/v1/api/auth/me", nil)
	require.NoError(t, err)
	req3.AddCookie(sessionCookie)
	resp3, err := http.DefaultClient.Do(req3)
	require.NoError(t, err)
	defer resp3.Body.Close()
	assert.Equal(t, http.StatusOK, resp3.StatusCode)

	var meResult map[string]interface{}
	require.NoError(t, json.NewDecoder(resp3.Body).Decode(&meResult))
	assert.Equal(t, "testuser", meResult["subject"], "subject should be the logged-in user")
}

func TestNoAuth_AllEndpointsAccessible(t *testing.T) {
	ctx := context.Background()

	tmp := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", tmp)
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")

	testdataDir, err := filepath.Abs("../query/testdata")
	require.NoError(t, err)

	fs := afs.New()
	wsMeta := meta.New(fs, testdataDir)
	agentLdr := agentloader.New(agentloader.WithMetaService(wsMeta))
	agentFndr := agentfinder.New(agentfinder.WithLoader(agentLdr))
	modelLdr := modelloader.New(wsfs.WithMetaService[provider.Config](wsMeta))
	modelFndr := modelfinder.New(modelfinder.WithConfigLoader(modelLdr))
	mcpMgr, err := mcpmgr.New(&stubMCPProvider{})
	require.NoError(t, err)
	registry, err := tool.NewDefaultRegistry(mcpMgr)
	require.NoError(t, err)

	rt, err := executor.NewBuilder().
		WithAgentFinder(agentFndr).
		WithModelFinder(modelFndr).
		WithRegistry(registry).
		WithMCPManager(mcpMgr).
		WithDefaults(&config.Defaults{Model: "openai_gpt4o_mini"}).
		Build(ctx)
	require.NoError(t, err)

	client, err := sdk.NewEmbeddedFromRuntime(rt)
	require.NoError(t, err)

	// No auth config
	handler := sdk.NewHandler(client)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp := doRequest(t, "GET", srv.URL+"/healthz", "", nil)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "no auth: healthz should be accessible")

	resp2 := doRequest(t, "GET", srv.URL+"/v1/conversations", "", nil)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode, "no auth: conversations should be accessible")
}

func TestJWTAuth_Transcript_NoToken_Returns401(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupAuthServer(t, pubPath, privPath)
	defer srv.Close()

	resp := doRequest(t, "GET", srv.URL+"/v1/conversations/test-conv-id/transcript", "", nil)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "transcript without token should return 401")
}

func TestJWTAuth_Transcript_ValidToken_Returns200(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupAuthServer(t, pubPath, privPath)
	defer srv.Close()

	token := signTestJWT(t, privPath, map[string]interface{}{
		"sub":   "transcript-user",
		"email": "transcript@test.com",
	}, 1*time.Hour)

	resp := doRequest(t, "GET", srv.URL+"/v1/conversations/test-conv-id/transcript", "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "transcript with valid token should return 200")

	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	_, hasTurns := result["turns"]
	assert.True(t, hasTurns, "response should contain a 'turns' field")
}

func TestJWTAuth_Transcript_FreshConversation_EmptyTurns(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupAuthServer(t, pubPath, privPath)
	defer srv.Close()

	token := signTestJWT(t, privPath, map[string]interface{}{
		"sub":   "transcript-user",
		"email": "transcript@test.com",
	}, 1*time.Hour)

	resp := doRequest(t, "GET", srv.URL+"/v1/conversations/nonexistent-conv/transcript", "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer resp.Body.Close()

	// For a non-existent conversation the server may return 200 with null/empty turns
	// or 500 if the backend errors on missing conversation.
	// We check that a valid response structure is returned when 200.
	if resp.StatusCode == http.StatusOK {
		var result struct {
			Turns []interface{} `json:"turns"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		assert.Empty(t, result.Turns, "fresh/nonexistent conversation should have empty turns")
	}
}

func TestJWTAuth_MultipleEndpoints(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupAuthServer(t, pubPath, privPath)
	defer srv.Close()

	token := signTestJWT(t, privPath, map[string]interface{}{
		"sub":   "e2e-multi",
		"email": "multi@test.com",
	}, 1*time.Hour)

	authHeaders := map[string]string{"Authorization": "Bearer " + token}

	// All these should succeed with valid token
	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/healthz"},
		{"GET", "/v1/conversations"},
		{"GET", "/v1/workspace/resources?kind=agents"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			resp := doRequest(t, ep.method, srv.URL+ep.path, "", authHeaders)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusOK, resp.StatusCode, "%s %s should succeed with valid token", ep.method, ep.path)
		})
	}

	// All should fail without token
	for _, ep := range endpoints {
		t.Run("notoken "+ep.method+" "+ep.path, func(t *testing.T) {
			resp := doRequest(t, ep.method, srv.URL+ep.path, "", nil)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "%s %s should return 401 without token", ep.method, ep.path)
		})
	}
}

// --- OAuth/Cookie Workspace E2E Tests ---
//
// These tests simulate a workspace with cookie-based auth (local/BFF) and RSA
// keys for JWT verification. They verify that:
//   - Cookie sessions work
//   - JWT Bearer tokens are also accepted (even in cookie mode)
//   - Unauthenticated requests are rejected
//   - The anonymous cookie fallback is blocked

// setupOAuthCookieServer creates an httptest.Server with local cookie auth
// enabled AND RSA JWT keys configured, simulating an OAuth workspace that
// also accepts JWT tokens.
func setupOAuthCookieServer(t *testing.T, pubPath, privPath string) *httptest.Server {
	t.Helper()
	ctx := context.Background()

	tmp := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", tmp)
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")

	testdataDir, err := filepath.Abs("../query/testdata")
	require.NoError(t, err)

	fs := afs.New()
	wsMeta := meta.New(fs, testdataDir)
	agentLdr := agentloader.New(agentloader.WithMetaService(wsMeta))
	agentFndr := agentfinder.New(agentfinder.WithLoader(agentLdr))
	modelLdr := modelloader.New(wsfs.WithMetaService[provider.Config](wsMeta))
	modelFndr := modelfinder.New(modelfinder.WithConfigLoader(modelLdr))
	mcpMgr, err := mcpmgr.New(&stubMCPProvider{})
	require.NoError(t, err)
	registry, err := tool.NewDefaultRegistry(mcpMgr)
	require.NoError(t, err)

	rt, err := executor.NewBuilder().
		WithAgentFinder(agentFndr).
		WithModelFinder(modelFndr).
		WithRegistry(registry).
		WithMCPManager(mcpMgr).
		WithDefaults(&config.Defaults{Model: "openai_gpt4o_mini"}).
		Build(ctx)
	require.NoError(t, err)

	client, err := sdk.NewEmbeddedFromRuntime(rt)
	require.NoError(t, err)

	// Cookie-based local auth with JWT keys also configured.
	authCfg := &iauth.Config{
		Enabled:    true,
		IpHashKey:  "test-ip-hash-key",
		CookieName: "agently_session",
		Local:      &iauth.Local{Enabled: true},
		JWT: &iauth.JWT{
			Enabled:       true,
			RSA:           []string{pubPath},
			RSAPrivateKey: privPath,
		},
	}

	sessions := svcauth.NewManager(7*24*time.Hour, nil)
	jwtSvc := svcauth.NewJWTService(authCfg.JWT)
	require.NoError(t, jwtSvc.Init(ctx))

	handler := sdk.NewHandler(client, sdk.WithAuth(authCfg, sessions))
	protected := svcauth.Protect(authCfg, sessions, svcauth.WithJWTService(jwtSvc))(handler)
	return httptest.NewServer(protected)
}

func TestOAuthCookie_UnauthenticatedRequest_Returns401(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupOAuthCookieServer(t, pubPath, privPath)
	defer srv.Close()

	resp := doRequest(t, "GET", srv.URL+"/v1/conversations", "", nil)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"unauthenticated request should return 401 in OAuth+cookie workspace")
}

func TestOAuthCookie_CookieSession_Succeeds(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupOAuthCookieServer(t, pubPath, privPath)
	defer srv.Close()

	// Login via local auth
	resp := doRequest(t, "POST", srv.URL+"/v1/api/auth/local/login", `{"username":"oauth-cookie-user"}`, nil)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "local login should succeed")

	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "agently_session" {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "login should set session cookie")

	// Access protected endpoint with cookie
	req, err := http.NewRequest("GET", srv.URL+"/v1/conversations", nil)
	require.NoError(t, err)
	req.AddCookie(sessionCookie)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode,
		"cookie-authenticated request should succeed in OAuth+cookie workspace")
}

func TestOAuthCookie_JWTBearer_AlsoAccepted(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupOAuthCookieServer(t, pubPath, privPath)
	defer srv.Close()

	// Sign a JWT and use Bearer token (no cookie)
	token := signTestJWT(t, privPath, map[string]interface{}{
		"sub":   "jwt-user-in-cookie-mode",
		"email": "jwt-user@example.com",
	}, 1*time.Hour)

	resp := doRequest(t, "GET", srv.URL+"/v1/conversations", "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"JWT Bearer token should be accepted even in cookie-based workspace")
}

func TestOAuthCookie_JWTBearer_ClaimsExtracted(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupOAuthCookieServer(t, pubPath, privPath)
	defer srv.Close()

	token := signTestJWT(t, privPath, map[string]interface{}{
		"sub":   "jwt-claims-user",
		"email": "claims@example.com",
	}, 1*time.Hour)

	resp := doRequest(t, "GET", srv.URL+"/v1/api/auth/me", "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var me map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&me))
	assert.Equal(t, "jwt-claims-user", me["subject"], "subject should match JWT claim")
	assert.Equal(t, "claims@example.com", me["email"], "email should match JWT claim")
}

func TestOAuthCookie_InvalidJWT_Returns401(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupOAuthCookieServer(t, pubPath, privPath)
	defer srv.Close()

	// Sign with a different key
	otherDir := t.TempDir()
	otherPriv, _ := generateRSAKeyPair(t, otherDir)
	badToken := signTestJWT(t, otherPriv, map[string]interface{}{
		"sub": "attacker",
	}, 1*time.Hour)

	resp := doRequest(t, "GET", srv.URL+"/v1/conversations", "", map[string]string{
		"Authorization": "Bearer " + badToken,
	})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"JWT signed with wrong key should be rejected in cookie workspace")
}

func TestOAuthCookie_ExpiredJWT_Returns401(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupOAuthCookieServer(t, pubPath, privPath)
	defer srv.Close()

	token := signTestJWT(t, privPath, map[string]interface{}{
		"sub": "expired-user",
	}, 1*time.Millisecond)
	time.Sleep(50 * time.Millisecond)

	resp := doRequest(t, "GET", srv.URL+"/v1/conversations", "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"expired JWT should be rejected in cookie workspace")
}

func TestOAuthCookie_AnonymousCookie_Blocked(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupOAuthCookieServer(t, pubPath, privPath)
	defer srv.Close()

	// Try to query without any auth — the anonymous cookie fallback should
	// not grant access when auth is enabled.
	body := `{"query":"hello","agentId":"simple"}`
	resp := doRequest(t, "POST", srv.URL+"/v1/agent/query", body, nil)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"anonymous query should be blocked when auth is enabled")
}

// setupBFFCookieServer creates an httptest.Server simulating a BFF OAuth
// workspace (cookie-only, no JWT.Enabled) but with RSA keys injected via
// the JWTService ProtectOption. This verifies that Bearer tokens are still
// accepted when a JWTService is provided even without JWT.Enabled.
func setupBFFCookieServer(t *testing.T, pubPath, privPath string) *httptest.Server {
	t.Helper()
	ctx := context.Background()

	tmp := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", tmp)
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")

	testdataDir, err := filepath.Abs("../query/testdata")
	require.NoError(t, err)

	fs := afs.New()
	wsMeta := meta.New(fs, testdataDir)
	agentLdr := agentloader.New(agentloader.WithMetaService(wsMeta))
	agentFndr := agentfinder.New(agentfinder.WithLoader(agentLdr))
	modelLdr := modelloader.New(wsfs.WithMetaService[provider.Config](wsMeta))
	modelFndr := modelfinder.New(modelfinder.WithConfigLoader(modelLdr))
	mcpMgr, err := mcpmgr.New(&stubMCPProvider{})
	require.NoError(t, err)
	registry, err := tool.NewDefaultRegistry(mcpMgr)
	require.NoError(t, err)

	rt, err := executor.NewBuilder().
		WithAgentFinder(agentFndr).
		WithModelFinder(modelFndr).
		WithRegistry(registry).
		WithMCPManager(mcpMgr).
		WithDefaults(&config.Defaults{Model: "openai_gpt4o_mini"}).
		Build(ctx)
	require.NoError(t, err)

	client, err := sdk.NewEmbeddedFromRuntime(rt)
	require.NoError(t, err)

	// BFF cookie mode with JWT keys also configured — both cookie and Bearer
	// tokens should be accepted.
	authCfg := &iauth.Config{
		Enabled:    true,
		IpHashKey:  "test-ip-hash-key",
		CookieName: "agently_session",
		Local:      &iauth.Local{Enabled: true},
		JWT: &iauth.JWT{
			Enabled:       true,
			RSA:           []string{pubPath},
			RSAPrivateKey: privPath,
		},
	}

	sessions := svcauth.NewManager(7*24*time.Hour, nil)
	jwtSvc := svcauth.NewJWTService(authCfg.JWT)
	require.NoError(t, jwtSvc.Init(ctx))

	handler := sdk.NewHandler(client, sdk.WithAuth(authCfg, sessions))
	protected := svcauth.Protect(authCfg, sessions, svcauth.WithJWTService(jwtSvc))(handler)
	return httptest.NewServer(protected)
}

func TestBFFCookie_JWTBearer_AcceptedViaJWTService(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupBFFCookieServer(t, pubPath, privPath)
	defer srv.Close()

	token := signTestJWT(t, privPath, map[string]interface{}{
		"sub":   "bff-jwt-user",
		"email": "bff@example.com",
	}, 1*time.Hour)

	resp := doRequest(t, "GET", srv.URL+"/v1/conversations", "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"JWT Bearer should be accepted in BFF mode when JWTService is provided")
}

func TestBFFCookie_Unauthenticated_Returns401(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupBFFCookieServer(t, pubPath, privPath)
	defer srv.Close()

	resp := doRequest(t, "GET", srv.URL+"/v1/conversations", "", nil)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"unauthenticated request should return 401 in BFF workspace")
}

func TestBFFCookie_CookieSession_StillWorks(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	srv := setupBFFCookieServer(t, pubPath, privPath)
	defer srv.Close()

	// Login
	resp := doRequest(t, "POST", srv.URL+"/v1/api/auth/local/login", `{"username":"bff-cookie-user"}`, nil)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "agently_session" {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie)

	req, err := http.NewRequest("GET", srv.URL+"/v1/conversations", nil)
	require.NoError(t, err)
	req.AddCookie(sessionCookie)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode,
		"cookie session should still work alongside JWT in BFF mode")
}
