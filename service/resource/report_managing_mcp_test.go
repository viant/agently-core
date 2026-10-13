package resource

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type reportConsumerClient struct {
	mcpclient.Interface
	before func(context.Context, *schema.CallToolRequestParams) error
	gets   int
}

func (c *reportConsumerClient) CallTool(ctx context.Context, request *schema.CallToolRequestParams, options ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	if request.Name == "reports/get" {
		c.gets++
	}
	if c.before != nil {
		if err := c.before(ctx, request); err != nil {
			return nil, err
		}
	}
	return c.Interface.CallTool(ctx, request, options...)
}

// The policy/error hook is installed on the managing MCP client, never on the
// report consumer. Its context is the original transport request context.
func attachReportConsumerClient(t *testing.T, catalog *ReportGatewayCatalog, provider *LocalProvider, before func(context.Context, *schema.CallToolRequestParams) error) *reportConsumerClient {
	t.Helper()
	client, err := NewLocalMCPClient(provider)
	require.NoError(t, err)
	wrapped := &reportConsumerClient{Interface: client, before: before}
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	original := catalog.Gateway
	catalog.Gateway = NewGateway(mgr, original.Actor, original.Verify, "aggregate")
	catalog.Gateway.Now = original.Now
	t.Cleanup(catalog.Gateway.Close)
	require.NoError(t, mgr.RegisterLocal(context.Background(), "internal", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "internal"}, func(context.Context) (mcpclient.Interface, error) { return wrapped, nil }))
	return wrapped
}

func TestReportConsumerUsesOneManagingReadAndForwardsContext(t *testing.T) {
	catalog, _, _, _, provider := reportGatewayFixture(t)
	type credentialKey struct{}
	ctx := context.WithValue(context.Background(), credentialKey{}, "original-session")
	seen := []map[string]interface{}{}
	client := attachReportConsumerClient(t, catalog, provider, func(call context.Context, request *schema.CallToolRequestParams) error {
		require.Equal(t, "original-session", call.Value(credentialKey{}))
		if request.Name == "reports/get" {
			seen = append(seen, request.Arguments)
		}
		return nil
	})
	reader, err := catalog.Reader(ctx, "report.execute")
	require.NoError(t, err)
	raw, pin, err := reader.(*gatewayReportReader).ReadResource(ctx, identity.ResourceRef{URI: "report://steward/sales"}, nil)
	require.NoError(t, err)
	require.True(t, json.Valid(raw))
	require.Equal(t, 1, client.gets, "one owning-MCP read, no second read after consumer policy")
	require.Equal(t, map[string]interface{}{"uri": "report://steward/sales"}, seen[0])
	require.Equal(t, "internal", pin.ProviderIdentity)
	original := *pin
	_, fresh, err := reader.ReadResolved(ctx, *pin)
	require.NoError(t, err)
	require.Equal(t, 2, client.gets, "a separate read forwards its exact owning pin once")
	require.Equal(t, map[string]interface{}{"uri": pin.URI, "revision": identity.WorkingCandidate}, seen[1])
	require.Equal(t, original.ProviderIdentity, fresh.ProviderIdentity)
	require.Equal(t, original.AuthorityBinding, fresh.AuthorityBinding)
	require.Equal(t, original.ContentFingerprint, fresh.ContentFingerprint)
	require.False(t, fresh.ValidUntil.After(original.ValidUntil))
}

func TestReportConsumerHonorsCurrentManagingMCPDenial(t *testing.T) {
	catalog, _, _, _, provider := reportGatewayFixture(t)
	var denied atomic.Bool
	client := attachReportConsumerClient(t, catalog, provider, func(_ context.Context, request *schema.CallToolRequestParams) error {
		if request.Name == "reports/get" && denied.Load() {
			return identity.ErrResourceDenied
		}
		return nil
	})
	reader, err := catalog.Reader(context.Background(), "report.compile")
	require.NoError(t, err)
	pin, err := reader.Resolve(context.Background(), identity.ResourceRef{URI: "report://steward/sales"})
	require.NoError(t, err)
	denied.Store(true)
	raw, fresh, err := reader.ReadResolved(context.Background(), *pin)
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	require.Nil(t, raw)
	require.Nil(t, fresh)
	require.Equal(t, 2, client.gets, "server denial is freshly consulted without local callback or cached grant")
}

func TestReportConsumerPreservesManagingErrorsAndChecksIdentity(t *testing.T) {
	t.Run("server error", func(t *testing.T) {
		catalog, _, _, _, provider := reportGatewayFixture(t)
		unavailable := errors.New("managing MCP unavailable")
		attachReportConsumerClient(t, catalog, provider, func(_ context.Context, request *schema.CallToolRequestParams) error {
			if request.Name == "reports/get" {
				return unavailable
			}
			return nil
		})
		reader, err := catalog.Reader(context.Background(), "report.retrieve")
		require.NoError(t, err)
		pin, err := reader.Resolve(context.Background(), identity.ResourceRef{URI: "report://steward/sales"})
		require.ErrorIs(t, err, unavailable)
		require.Nil(t, pin)
	})
	t.Run("changed request identity", func(t *testing.T) {
		catalog, _, _, _, provider := reportGatewayFixture(t)
		client := attachReportConsumerClient(t, catalog, provider, nil)
		reader, err := catalog.Reader(context.Background(), "report.retrieve")
		require.NoError(t, err)
		pin, err := reader.Resolve(context.Background(), identity.ResourceRef{URI: "report://steward/sales"})
		require.NoError(t, err)
		catalog.Gateway.Verify = func(context.Context, identity.VerifiedActor) error { return identity.ErrResourceDenied }
		_, fresh, err := reader.ReadResolved(context.Background(), *pin)
		require.ErrorIs(t, err, identity.ErrResourceDenied)
		require.Nil(t, fresh)
		require.Equal(t, 1, client.gets, "invalid session must be rejected before dispatch")
	})
}
