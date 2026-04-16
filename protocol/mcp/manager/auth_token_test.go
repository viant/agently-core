package manager

import (
	"context"
	"testing"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/mcp"
	authtransport "github.com/viant/mcp/client/auth/transport"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

type authTokenProviderStub struct {
	calls int
	key   token.Key
}

func (s *authTokenProviderStub) EnsureTokens(ctx context.Context, key token.Key) (context.Context, error) {
	s.calls++
	s.key = key
	return authctx.WithTokens(ctx, &scyauth.Token{
		Token: oauth2.Token{
			AccessToken: "access-token",
			Expiry:      time.Now().Add(time.Hour),
		},
		IDToken: "id-token",
	}), nil
}

func (s *authTokenProviderStub) Store(ctx context.Context, key token.Key, tok *scyauth.Token) error {
	return nil
}

func (s *authTokenProviderStub) Invalidate(ctx context.Context, key token.Key) error {
	return nil
}

type authTokenProviderConfigStub struct {
	cfg *mcpcfg.MCPClient
}

func (s *authTokenProviderConfigStub) Options(ctx context.Context, serverName string) (*mcpcfg.MCPClient, error) {
	return s.cfg, nil
}

func TestWithAuthTokenContext_RefreshesTokensForCurrentUser(t *testing.T) {
	tp := &authTokenProviderStub{}
	mgr, err := New(&authTokenProviderConfigStub{cfg: &mcpcfg.MCPClient{
		ClientOptions: &mcp.ClientOptions{
			Auth: &mcp.ClientAuth{UseIdToken: true},
			Transport: mcp.ClientTransport{
				Type:                "streamable",
				ClientTransportHTTP: mcp.ClientTransportHTTP{URL: "http://example.com/mcp"},
			},
		},
	}}, WithTokenProvider(tp))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()
	ctx = authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "user-123"})
	ctx = authctx.WithProvider(ctx, "oauth")

	next := mgr.WithAuthTokenContext(ctx, "guardian")

	if tp.calls != 1 {
		t.Fatalf("EnsureTokens() calls = %d, want 1", tp.calls)
	}
	if tp.key.Subject != "user-123" || tp.key.Provider != "oauth" {
		t.Fatalf("EnsureTokens() key = (%q, %q), want (%q, %q)", tp.key.Subject, tp.key.Provider, "user-123", "oauth")
	}
	if got, _ := next.Value(authtransport.ContextAuthTokenKey).(string); got != "id-token" {
		t.Fatalf("context auth token = %q, want %q", got, "id-token")
	}
}
