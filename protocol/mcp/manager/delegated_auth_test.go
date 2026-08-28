package manager

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/mcp"
	authcfg "github.com/viant/mcp/client/auth/config"
	authtransport "github.com/viant/mcp/client/auth/transport"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

func delegatedConfig(disableSwitch bool) *mcpcfg.MCPClient {
	return &mcpcfg.MCPClient{
		DisableDelegatedAuth: disableSwitch,
		ClientOptions: &mcp.ClientOptions{
			Name: "viant-mcp-dev6",
			Auth: &mcp.ClientAuth{
				Mode:        authcfg.ModeOAuth,
				ProviderRef: "adelphic-dev6",
				ClientRef:   "steward-web",
				Resource:    "https://mcp6.example.com/mcp",
				Scopes:      []string{"plan:create"},
			},
			Transport: mcp.ClientTransport{
				Type:                "streamable",
				ClientTransportHTTP: mcp.ClientTransportHTTP{URL: "https://mcp6.example.com/mcp"},
			},
		},
	}
}

type staticCredentialResolver struct{}

func (staticCredentialResolver) Resolve(ctx context.Context, requirement authcfg.Requirement) (*authcfg.Credential, error) {
	return &authcfg.Credential{Token: "delegated-token"}, nil
}
func (staticCredentialResolver) Refresh(ctx context.Context, requirement authcfg.Requirement) (*authcfg.Credential, error) {
	return &authcfg.Credential{Token: "delegated-token"}, nil
}
func (staticCredentialResolver) Invalidate(ctx context.Context, requirement authcfg.Requirement) error {
	return nil
}

func TestIsDelegatedAuth(t *testing.T) {
	mgr, err := New(&authTokenProviderConfigStub{cfg: delegatedConfig(false)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !mgr.IsDelegatedAuth(context.Background(), "viant-mcp-dev6") {
		t.Fatalf("auth.mode=oauth with providerRef must classify as delegated")
	}

	legacy, err := New(&authTokenProviderConfigStub{cfg: &mcpcfg.MCPClient{
		ClientOptions: &mcp.ClientOptions{
			Auth: &mcp.ClientAuth{OAuth2ConfigURL: []string{"scy://legacy"}},
			Transport: mcp.ClientTransport{
				Type:                "streamable",
				ClientTransportHTTP: mcp.ClientTransportHTTP{URL: "https://legacy.example.com/mcp"},
			},
		},
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if legacy.IsDelegatedAuth(context.Background(), "legacy") {
		t.Fatalf("legacy OAuth2ConfigURL config must not classify as delegated")
	}
}

// TestWithAuthTokenContextSkipsDelegatedServers proves the workspace token is
// never injected into outbound context for a delegated server, while legacy
// behaviour is preserved for non-delegated ones.
func TestWithAuthTokenContextSkipsDelegatedServers(t *testing.T) {
	tp := &authTokenProviderStub{}
	mgr, err := New(&authTokenProviderConfigStub{cfg: delegatedConfig(false)}, WithTokenProvider(tp))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "user-123"})
	ctx = authctx.WithProvider(ctx, "oauth")
	ctx = authctx.WithTokens(ctx, &scyauth.Token{Token: oauth2.Token{
		AccessToken: "workspace-access",
		Expiry:      time.Now().Add(time.Hour),
	}})

	next := mgr.WithAuthTokenContext(ctx, "viant-mcp-dev6")
	if tp.calls != 0 {
		t.Fatalf("delegated server must not trigger workspace EnsureTokens (calls=%d)", tp.calls)
	}
	if tok, _ := next.Value(authtransport.ContextAuthTokenKey).(string); tok != "" {
		t.Fatalf("workspace token leaked into delegated transport context: %q", tok)
	}
	// The parent identity context is inherited unchanged.
	if got := authctx.EffectiveUserID(next); got != "user-123" {
		t.Fatalf("EffectiveUserID changed: %q", got)
	}
	if got := authctx.Provider(next); got != "oauth" {
		t.Fatalf("workspace provider changed: %q", got)
	}
}

func TestNewClientDelegatedRequiresResolver(t *testing.T) {
	mgr, err := New(&authTokenProviderConfigStub{cfg: delegatedConfig(false)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := mgr.newClient(context.Background(), "conv-1", "viant-mcp-dev6"); err == nil {
		t.Fatalf("delegated config without installed resolver must fail closed")
	} else if !strings.Contains(err.Error(), "credential resolver") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewClientDelegatedKillSwitch(t *testing.T) {
	mgr, err := New(&authTokenProviderConfigStub{cfg: delegatedConfig(true)},
		WithCredentialResolver(staticCredentialResolver{}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = mgr.newClient(context.Background(), "conv-1", "viant-mcp-dev6")
	if err == nil || !strings.Contains(err.Error(), "provider_disabled") {
		t.Fatalf("MCP-level kill switch must fail with provider_disabled, got %v", err)
	}
}

// TestNewClientDelegatedRequiresRegistryForProviderRef: a providerRef cannot
// be validated without a registry, so client creation fails fast instead of
// compiling an unvalidated requirement.
func TestNewClientDelegatedRequiresRegistryForProviderRef(t *testing.T) {
	mgr, err := New(&authTokenProviderConfigStub{cfg: delegatedConfig(false)},
		WithCredentialResolver(staticCredentialResolver{}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = mgr.newClient(context.Background(), "conv-1", "viant-mcp-dev6")
	if err == nil || !strings.Contains(err.Error(), "provider registry") {
		t.Fatalf("providerRef without an installed registry must fail fast, got %v", err)
	}
}

// TestNewClientDelegatedNormalizesLegacyUseIdToken: legacy useIdToken=true
// with an empty tokenType normalizes before requirement compilation, and an
// explicit conflicting tokenType fails validation.
func TestNewClientDelegatedNormalizesLegacyUseIdToken(t *testing.T) {
	conflicting := delegatedConfig(false)
	conflicting.ClientOptions.Auth.UseIdToken = true
	conflicting.ClientOptions.Auth.TokenType = string(authcfg.TokenTypeAccessToken)
	mgr, err := New(&authTokenProviderConfigStub{cfg: conflicting},
		WithCredentialResolver(staticCredentialResolver{}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = mgr.newClient(context.Background(), "conv-1", "viant-mcp-dev6")
	if err == nil || !strings.Contains(err.Error(), "useIdToken") {
		t.Fatalf("conflicting useIdToken/tokenType must fail validation, got %v", err)
	}

	normalized := delegatedConfig(false)
	normalized.ClientOptions.Auth.UseIdToken = true
	if err := normalized.NormalizeDelegatedAuth(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if normalized.ClientOptions.Auth.TokenType != string(authcfg.TokenTypeIDToken) {
		t.Fatalf("legacy useIdToken must normalize to tokenType=idToken, got %q", normalized.ClientOptions.Auth.TokenType)
	}
}

type recordingInlineRegistry struct {
	registered  []*authcfg.OAuthProvider
	registerErr error
}

func (r *recordingInlineRegistry) ResolveProvider(ctx context.Context, ref string) (*authcfg.OAuthProvider, error) {
	return nil, fmt.Errorf("not found: %s", ref)
}
func (r *recordingInlineRegistry) MatchIssuer(ctx context.Context, issuer string) (*authcfg.OAuthProvider, error) {
	return nil, fmt.Errorf("not found: %s", issuer)
}
func (r *recordingInlineRegistry) RegisterInline(ctx context.Context, provider *authcfg.OAuthProvider) error {
	if r.registerErr != nil {
		return r.registerErr
	}
	r.registered = append(r.registered, provider)
	return nil
}

// TestNewClientDelegatedInlineProviderRegistrationFailureFailsClosed: an
// overlay conflict reported by the registry fails client creation.
func TestNewClientDelegatedInlineProviderRegistrationFailureFailsClosed(t *testing.T) {
	cfg := delegatedConfig(false)
	cfg.ClientOptions.Auth.ProviderRef = ""
	cfg.ClientOptions.Auth.InlineProvider = &authcfg.OAuthProvider{
		ID:            "inline-dev6",
		Issuer:        "https://idp-inline.example.com",
		DefaultClient: "web",
		Clients:       map[string]*authcfg.OAuthClient{"web": {ConfigURL: "scy://inline"}},
	}
	registry := &recordingInlineRegistry{registerErr: fmt.Errorf("inline oauth provider %q conflicts", "inline-dev6")}
	mgr, err := New(&authTokenProviderConfigStub{cfg: cfg},
		WithCredentialResolver(staticCredentialResolver{}),
		WithProviderRegistry(registry))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = mgr.newClient(context.Background(), "conv-1", "viant-mcp-dev6")
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("inline registration conflict must fail client creation, got %v", err)
	}
	if len(registry.registered) != 0 {
		t.Fatalf("failed registration must not record the provider")
	}
}

func TestNewClientDelegatedRejectsConflictingLegacyAuth(t *testing.T) {
	cfg := delegatedConfig(false)
	cfg.ClientOptions.Auth.OAuth2ConfigURL = []string{"scy://legacy"}
	mgr, err := New(&authTokenProviderConfigStub{cfg: cfg},
		WithCredentialResolver(staticCredentialResolver{}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := mgr.newClient(context.Background(), "conv-1", "viant-mcp-dev6"); err == nil {
		t.Fatalf("delegated mode combined with legacy oauth2ConfigURL must fail validation")
	}
}
