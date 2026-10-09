package executor

import (
	"context"
	"github.com/stretchr/testify/require"
	manager "github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	service "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/forge/backend/types"
	"strings"
	"testing"
	"time"
)

type primitiveLocalCatalog struct{}

func (primitiveLocalCatalog) AuthzReady() bool { return true }
func (primitiveLocalCatalog) List(context.Context, *service.WindowDefinitionListInput) (*service.WindowDefinitionListOutput, error) {
	return &service.WindowDefinitionListOutput{}, nil
}
func (primitiveLocalCatalog) Get(context.Context, *service.WindowDefinitionGetInput) (*service.WindowDefinitionGetOutput, error) {
	return nil, identity.ErrResourceDenied
}
func TestBuilderInstallsOneSourceAwarePrimitiveWindowPath(t *testing.T) {
	actor := identity.VerifiedActor{Issuer: "https://fixture.invalid", Subject: "alice", TenantID: "tenant", AccountID: "account", IdentityRevision: "1", ValidUntil: time.Now().Add(time.Minute)}
	mgr, e := manager.New(nil)
	require.NoError(t, e)
	proof, e := types.NewWindowTargetHMAC([]byte(strings.Repeat("k", 32)))
	require.NoError(t, e)
	bridge := service.NewService(&service.Config{WindowDefinitions: primitiveLocalCatalog{}, DynamicWindowAuthorizer: func(context.Context, string) (bool, error) { return true, nil }})
	runtime := &Runtime{MCPManager: mgr, UIBridge: bridge}
	builder := NewBuilder().WithPrimitiveAuthority(func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, func(context.Context, identity.VerifiedActor) error { return nil }, "local-workspace").WithPrimitiveGatewayIdentity("gateway").WithPrimitiveWindows(func(context.Context, identity.ResolvedResource, *types.Window) error { return nil }, proof)
	require.NoError(t, builder.configurePrimitiveProviders(runtime))
	require.NotNil(t, runtime.PrimitiveProviders)
	require.NotNil(t, runtime.PrimitiveWindows)
	require.IsType(t, primitiveLocalCatalog{}, runtime.PrimitiveWindows.Local)
	require.True(t, bridge.AuthzReady())
	require.NotNil(t, runtime.DatasourceDefinitionResolver)
	require.NotNil(t, runtime.DatasourceResourceRevalidator)
	require.NotNil(t, runtime.DatasourceDefinitionAuthorizer)
	require.NotNil(t, runtime.DatasourceProviderExecutor)
	_, e = runtime.DatasourceProviderExecutor(context.Background(), nil, nil)
	require.ErrorIs(t, e, identity.ErrResourceDenied, "runtime execution must reject a caller without the original window pin and target")
	before := runtime.PrimitiveWindows
	require.NoError(t, builder.configurePrimitiveProviders(runtime))
	require.Same(t, before, runtime.PrimitiveWindows)
	runtime.PrimitiveProviders.Close()
	_, e = runtime.PrimitiveProviders.Discover(context.Background())
	require.ErrorIs(t, e, identity.ErrResourceDenied)
}
func TestBuilderRejectsIncompletePrimitiveHostGuards(t *testing.T) {
	builder := NewBuilder()
	builder.primitiveWindowAdmission = func(context.Context, identity.ResolvedResource, *types.Window) error { return nil }
	require.Error(t, builder.configurePrimitiveProviders(&Runtime{}))
}
