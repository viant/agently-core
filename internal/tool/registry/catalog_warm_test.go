package tool

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
)

type parallelCatalogClient struct {
	fakeClient
	name         string
	active, peak *atomic.Int64
	entered      chan<- string
	release      <-chan struct{}
}

func (c *parallelCatalogClient) ListTools(ctx context.Context, cursor *string, options ...client.RequestOption) (*schema.ListToolsResult, error) {
	current := c.active.Add(1)
	defer c.active.Add(-1)
	for previous := c.peak.Load(); current > previous; previous = c.peak.Load() {
		if c.peak.CompareAndSwap(previous, current) {
			break
		}
	}
	c.entered <- c.name
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.release:
	}
	return &schema.ListToolsResult{Tools: []schema.Tool{{Name: c.name}}}, nil
}

func TestToolCatalogPopulationIsBoundedParallelAndRetainsServerOrder(t *testing.T) {
	var active, peak atomic.Int64
	entered := make(chan string, 8)
	release := make(chan struct{})
	reg := &Registry{internal: map[string]client.Interface{}}
	servers := make([]string, 8)
	for i := range servers {
		name := fmt.Sprintf("server%d", i)
		servers[i] = name
		reg.internal[name] = &parallelCatalogClient{name: name, active: &active, peak: &peak, entered: entered, release: release}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan []toolCatalogResult, 1)
	go func() { done <- reg.listToolCatalogs(ctx, servers) }()
	for i := 0; i < catalogDiscoveryConcurrency; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("catalog population remained serial")
		}
	}
	require.EqualValues(t, 4, peak.Load())
	close(release)
	results := <-done
	require.EqualValues(t, 4, peak.Load(), "discovery must never exceed the configured concurrency bound")
	for i, result := range results {
		require.NoError(t, result.err)
		require.Equal(t, servers[i], result.tools[0].Name)
	}
}

type warmCredentialManager struct {
	discoveryManagerStub
	preflights atomic.Int64
}

func (m *warmCredentialManager) IsDelegatedAuth(_ context.Context, server string) bool {
	return server == "unlinked"
}
func (m *warmCredentialManager) PreflightCredential(context.Context, string) error {
	m.preflights.Add(1)
	return fmt.Errorf("oauth link required")
}
func TestUserCatalogWarmSkipsUnlinkedDelegatedConnection(t *testing.T) {
	mgr := &warmCredentialManager{}
	reg := &Registry{mgr: mgr, cache: map[string]*toolCacheEntry{}}
	require.NoError(t, reg.WarmToolCatalogs(discoveryUserContext(), []string{"unlinked"}))
	require.EqualValues(t, 1, mgr.preflights.Load())
	require.Empty(t, mgr.getCallsSnapshot(), "missing credentials must not create a transport or begin remote login")
	require.Empty(t, reg.cache)
}

func TestProactiveCatalogFailureDoesNotDelayExplicitDiscovery(t *testing.T) {
	mgr := &discoveryManagerStub{getFunc: func(string, string) (client.Interface, error) { return nil, fmt.Errorf("backend unavailable") }}
	reg := &Registry{mgr: mgr, cache: map[string]*toolCacheEntry{}}
	require.Error(t, reg.WarmToolCatalogs(discoveryUserContext(), []string{"remote"}))
	require.Empty(t, reg.discoveryFailUntil, "proactive failure must not put ordinary user discovery on cooldown")
}
