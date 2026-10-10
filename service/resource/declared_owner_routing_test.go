package resource

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	mcpclient "github.com/viant/mcp/client"
)

func TestDeclaredOwnerRoutingDoesNotContactUnrelatedOfflineProvider(t *testing.T) {
	for _, collision := range []bool{false, true} {
		t.Run(map[bool]string{false: "offline-independent", true: "declared-collision"}[collision], func(t *testing.T) {
			local := provider("native-owner")
			remoteOwner := "studio-owner"
			if collision {
				remoteOwner = "native-owner"
			}
			options := fixtureOptions{"internal": &cfg.MCPClient{PrimitiveProviderIdentity: "native-owner"}, "studio": &cfg.MCPClient{PrimitiveProviderIdentity: remoteOwner}}
			remoteCalls := 0
			mgr, err := manager.New(options, manager.WithClientFactory(func(_ context.Context, _ string, name string) (mcpclient.Interface, error) {
				if name == "studio" {
					remoteCalls++
					return nil, errors.New("offline fixture")
				}
				return local, nil
			}))
			require.NoError(t, err)
			defer mgr.CloseConversation("")
			actor := identity.VerifiedActor{Subject: "alice", Issuer: "fixture-issuer", TenantID: "fixture-tenant", AccountID: "21", IdentityRevision: "original", ValidUntil: time.Now().Add(time.Minute)}
			revoked := false
			g := NewGateway(mgr, func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, func(_ context.Context, expected identity.VerifiedActor) error {
				if revoked || expected != actor {
					return identity.ErrResourceDenied
				}
				return nil
			}, "gateway-owner")
			defer g.Close()
			connection, err := g.ConnectionForProvider(context.Background(), "native-owner")
			if collision {
				require.ErrorIs(t, err, ErrCollision)
				require.Zero(t, remoteCalls)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "internal", connection.Name)
			require.Zero(t, remoteCalls)
			uri, err := identity.ParseResourceURI("window://example/sales")
			require.NoError(t, err)
			selected, err := g.ConnectionForResource(context.Background(), uri, "native-owner")
			require.NoError(t, err)
			require.Equal(t, connection, selected)
			require.Zero(t, remoteCalls)
			opened, err := g.Get(context.Background(), selected, identity.ResourceRef{URI: uri.String()}, nil)
			require.NoError(t, err)
			require.NotNil(t, opened.ResolvedResource)
			local.after = func(method string) {
				if method == "windows/get" {
					revoked = true
				}
			}
			_, err = g.Get(context.Background(), selected, identity.ResourceRef{URI: uri.String()}, opened.ResolvedResource)
			require.ErrorIs(t, err, identity.ErrResourceDenied)
			require.Zero(t, remoteCalls)
			revoked = false
			local.after = nil
			_, err = g.Discover(context.Background())
			require.ErrorContains(t, err, "offline fixture", "complete inventory still reports unrelated provider failure rather than hiding it")
			require.Equal(t, 1, remoteCalls)
		})
	}
}
