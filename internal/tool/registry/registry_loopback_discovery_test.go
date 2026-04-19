package tool

import (
	"context"
	"errors"
	"testing"
	"time"

	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/mcp"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type loopbackDiscoveryManagerStub struct{}

func (m *loopbackDiscoveryManagerStub) Get(ctx context.Context, convID, serverName string) (mcpclient.Interface, error) {
	return nil, errors.New("not implemented")
}

func (m *loopbackDiscoveryManagerStub) Reconnect(ctx context.Context, convID, serverName string) (mcpclient.Interface, error) {
	return nil, errors.New("not implemented")
}

func (m *loopbackDiscoveryManagerStub) Touch(convID, serverName string) {}

func (m *loopbackDiscoveryManagerStub) Options(ctx context.Context, serverName string) (*mcpcfg.MCPClient, error) {
	return &mcpcfg.MCPClient{
		ClientOptions: &mcp.ClientOptions{
			Transport: mcp.ClientTransport{
				Type: "streamable",
				ClientTransportHTTP: mcp.ClientTransportHTTP{
					URL: "http://localhost:5002/mcp",
				},
			},
		},
	}, nil
}

func (m *loopbackDiscoveryManagerStub) UseIDToken(ctx context.Context, serverName string) bool {
	return true
}

func (m *loopbackDiscoveryManagerStub) WithAuthTokenContext(ctx context.Context, serverName string) context.Context {
	return ctx
}

func TestRegistryLoopbackDiscoveryFailureUsesServerScopedCooldown(t *testing.T) {
	r := &Registry{
		mgr:                &loopbackDiscoveryManagerStub{},
		discoveryFailUntil: map[string]time.Time{},
		discoveryFailErr:   map[string]string{},
		discoveryFailTTL:   30 * time.Second,
	}

	r.noteDiscoveryFailure("steward", "mcp-discovery:steward:1", errors.New(`failed to send request: Post "http://localhost:5002/mcp": dial tcp 127.0.0.1:5002: connect: connection refused`))

	if _, ok := r.discoveryFailUntil["steward"]; !ok {
		t.Fatalf("expected loopback discovery cooldown to use server-scoped key")
	}
	if _, ok := r.discoveryFailUntil["steward|mcp-discovery:steward:1"]; ok {
		t.Fatalf("did not expect scope-specific cooldown key for loopback transport")
	}
	if until := r.discoveryFailUntil["steward"]; time.Until(until) < 4*time.Minute {
		t.Fatalf("expected extended cooldown for loopback transport, got %s", time.Until(until))
	}
}

func TestRegistryRefreshServerTools_IgnoresLoopbackCooldown(t *testing.T) {
	stub := &discoveryManagerStub{
		getFunc: func(convID, server string) (mcpclient.Interface, error) {
			return &discoveryListClient{tools: []mcpschema.Tool{{Name: "alpha"}}}, nil
		},
	}
	r := &Registry{
		mgr:                stub,
		cache:              map[string]*toolCacheEntry{},
		discoveryFailUntil: map[string]time.Time{},
		discoveryFailErr:   map[string]string{},
		discoveryFailTTL:   30 * time.Second,
	}

	r.noteDiscoveryFailure("steward", "mcp-discovery:steward:1", errors.New(`failed to send request: Post "http://localhost:5002/mcp": dial tcp 127.0.0.1:5002: connect: connection refused`))

	if err := r.refreshServerTools(context.Background(), "steward"); err != nil {
		t.Fatalf("refreshServerTools() error = %v", err)
	}
	if _, ok := r.cache["steward/alpha"]; !ok {
		t.Fatalf("expected refreshed loopback tool to be cached")
	}
	if _, ok := r.discoveryFailUntil["steward"]; ok {
		t.Fatalf("expected loopback cooldown to clear after successful refresh")
	}
}
