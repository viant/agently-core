package manager

import (
	"context"
	"testing"

	authctx "github.com/viant/agently-core/internal/auth"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

func TestWithAuthTokenContextPreservesRegisteredLocalRequest(t *testing.T) {
	refresh := &authTokenProviderStub{}
	mgr, err := New(nil, WithTokenProvider(refresh))
	if err != nil {
		t.Fatal(err)
	}
	ctx := authctx.WithUserInfo(authctx.WithIDToken(context.Background(), "verified-current"), &authctx.UserInfo{Subject: "owned-user"})
	if err = mgr.RegisterLocal(ctx, "arbitrary-process-provider", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "owned"}, func(context.Context) (mcpclient.Interface, error) {
		t.Fatal("context binding must not construct a client")
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if !mgr.IsLocalClient("arbitrary-process-provider") || mgr.IsLocalClient("internal") {
		t.Fatal("local distinction must follow registration only")
	}
	if bound := mgr.WithAuthTokenContext(ctx, "arbitrary-process-provider"); bound != ctx || authctx.IDToken(bound) != "verified-current" {
		t.Fatal("registered local operation replaced caller context")
	}
	if refresh.calls != 0 {
		t.Fatal("registered local operation consulted OAuth refresh provider")
	}
}
