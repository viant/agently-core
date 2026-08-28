package tool

import (
	"context"
	"errors"
	"testing"

	"github.com/viant/agently-core/internal/auth/mcpauth"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	mcpclient "github.com/viant/mcp/client"
	authcfg "github.com/viant/mcp/client/auth/config"
)

// delegatedManagerStub reports delegated auth for every server and still
// injects a workspace token through WithAuthTokenContext, proving the
// registry-side gate (not the manager) prevents attachment.
type delegatedManagerStub struct {
	executeAuthManagerStub
}

func (m *delegatedManagerStub) IsDelegatedAuth(ctx context.Context, serverName string) bool {
	return true
}

func TestExecute_DelegatedServerNeverAttachesWorkspaceToken(t *testing.T) {
	client := &authCaptureClient{}
	reg := &Registry{
		mgr:           &delegatedManagerStub{executeAuthManagerStub{client: client}},
		cache:         map[string]*toolCacheEntry{},
		internal:      map[string]mcpclient.Interface{},
		recentResults: map[string]map[string]recentItem{},
	}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	out, err := reg.Execute(ctx, "helper/ping", map[string]interface{}{"q": "x"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "ok" {
		t.Fatalf("Execute() output = %q", out)
	}
	if client.options == nil {
		t.Fatalf("expected captured request options")
	}
	if client.options.StringToken != "" {
		t.Fatalf("workspace token %q must never be attached to a delegated server call", client.options.StringToken)
	}
}

// delegatedLinkErrManagerStub returns a viant/mcp link-required error from Get
// so Execute must surface the stable Agently-typed error.
type delegatedLinkErrManagerStub struct {
	executeAuthManagerStub
}

func (m *delegatedLinkErrManagerStub) IsDelegatedAuth(ctx context.Context, serverName string) bool {
	return true
}

func (m *delegatedLinkErrManagerStub) Get(ctx context.Context, convID, serverName string) (mcpclient.Interface, error) {
	return nil, &authcfg.OAuthLinkRequiredError{
		ServerName:  serverName,
		ProviderRef: "adelphic-dev6",
		Resource:    "https://mcp6.example.com/mcp",
	}
}

func TestExecute_MapsLinkRequiredToTypedError(t *testing.T) {
	reg := &Registry{
		mgr:           &delegatedLinkErrManagerStub{},
		cache:         map[string]*toolCacheEntry{},
		internal:      map[string]mcpclient.Interface{},
		recentResults: map[string]map[string]recentItem{},
	}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	_, err := reg.Execute(ctx, "helper/ping", map[string]interface{}{})
	if err == nil {
		t.Fatalf("expected link-required error")
	}
	var typed *mcpauth.LinkRequiredError
	if !errors.As(err, &typed) {
		t.Fatalf("expected Agently-typed link error, got %T: %v", err, err)
	}
	if typed.ServerName != "helper" || typed.ProviderRef != "adelphic-dev6" {
		t.Fatalf("typed error identity = %+v", typed)
	}
}
