package executor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	auth "github.com/viant/agently-core/service/auth"
	forge "github.com/viant/agently-core/service/primitiveprovider"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	"github.com/viant/forge/backend/types"
)

type menuPresentationSource struct {
	calls   int
	failure error
}

func (s *menuPresentationSource) ResolvePrincipal(ctx context.Context) (gating.Principal, error) {
	s.calls++
	principal := gating.Principal{AccountID: "21", Facts: authz.Facts{Subject: authctx.EffectiveUserID(ctx), Issuer: "fixture", Tenant: "https://issuer.example", ValidUntil: time.Now().Add(time.Minute)}}
	if principal.Facts.Subject == "editor" {
		principal.Facts.Roles = []string{"editor"}
		principal.Facts.Exposures = []string{"reports"}
	}
	return principal, s.failure
}
func menuPresentationContext(subject string) context.Context {
	return authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: subject})
}

var presentationMenu = json.RawMessage(`{"schemaVersion":1,"applications":[{"id":"public","action":{"type":"window","windowKey":"public"}},{"id":"role","visibleWhen":{"source":"authorization","field":"principal.roles","contains":"editor"},"menus":[{"id":"feature","visibleWhen":{"source":"authorization","field":"principal.features","contains":"reports"},"action":{"type":"window","windowKey":"report"}}]},{"id":"spec","authorization":{"resourceType":"advertiser","requestedGlobalCapabilities":["read"]},"visibleWhen":{"source":"authorization","field":"account.id","equals":"21"},"action":{"type":"window","windowKey":"account"}},{"id":"capability","authorization":{"resourceType":"advertiser","requestedGlobalCapabilities":["read"]},"visibleWhen":{"source":"authorization","field":"globalCapabilities.read","equals":true},"action":{"type":"window","windowKey":"unknown"}}]}`)

func TestPrincipalMenuProjectionResolvesOnceAndSurfacesFailure(t *testing.T) {
	source := &menuPresentationSource{}
	project := principalMenuProjection(source, nil)
	for i, subject := range []string{"editor", "viewer", "editor"} {
		visible, err := project(menuPresentationContext(subject), presentationMenu)
		require.NoError(t, err)
		require.Equal(t, i+1, source.calls, "one trusted principal resolution per whole menu projection")
		require.Contains(t, string(visible), `"id":"public"`)
		require.Contains(t, string(visible), `"id":"spec"`)
		if subject == "editor" {
			require.Contains(t, string(visible), `"id":"feature"`)
		} else {
			require.NotContains(t, string(visible), `"id":"role"`)
		}
		require.NotContains(t, string(visible), `"id":"capability"`, "principal facts never fabricate a global read capability")
	}
	source.failure = errors.New("userinfo unavailable")
	visible, err := project(menuPresentationContext("editor"), presentationMenu)
	require.ErrorIs(t, err, permittedview.ErrUnavailable)
	require.Nil(t, visible)
	require.Contains(t, string(presentationMenu), `"id":"capability"`, "authored input stays intact")
}
func TestNativePrincipalMenuIsolationAndUserCacheScopes(t *testing.T) {
	root := t.TempDir()
	old := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(old) })
	source := &menuPresentationSource{}
	builder := NewBuilder().WithNativeLayoutDefault(presentationMenu).WithPresentationPrincipalResolver(source)
	pool, err := manager.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { pool.CloseConversation("") })
	runtime := &Runtime{MCPManager: pool}
	require.NoError(t, builder.configurePrimitiveProviders(runtime))
	t.Cleanup(runtime.PrimitiveProviders.Close)
	options, err := pool.Options(menuPresentationContext("editor"), "internal")
	require.NoError(t, err)
	require.Equal(t, cfg.ResourceCacheUser, options.ResourceCacheScopes["workspace/navigation"])
	require.Equal(t, cfg.ResourceCacheUser, options.ResourceCacheScopes["workspace/window"])
	require.Equal(t, cfg.ResourceCacheUser, options.ResourceCacheScopes["system/window"])
	require.Equal(t, cfg.ResourceCachePublic, options.ResourceCacheScopes["workspace"])
	require.Equal(t, resources.InternalProviderIdentity, options.PrimitiveProviderIdentity)
	require.Empty(t, options.PrimitiveNamespaces, "presentation identity domain cannot become MCP namespace configuration")
	require.Nil(t, runtime.ComponentAuthority)
	require.Nil(t, runtime.ExecutionContext)
	require.Nil(t, runtime.PermittedResolver)
	require.False(t, runtime.DatasourceDisableCache)
	uri, _ := identity.ParseResourceURI("navigation://workspace/layout")
	for _, subject := range []string{"editor", "viewer", "editor"} {
		ctx := menuPresentationContext(subject)
		connection, err := runtime.PrimitiveProviders.ConnectionForResource(ctx, uri, resources.InternalProviderIdentity)
		require.NoError(t, err)
		result, err := resources.Get[json.RawMessage](ctx, runtime.PrimitiveProviders, connection, identity.ResourceRef{URI: uri.String()}, nil)
		require.NoError(t, err)
		require.Equal(t, uri.String(), result.ResolvedResource.URI)
		require.Equal(t, resources.InternalProviderIdentity, result.ResolvedResource.ProviderIdentity)
		if subject == "editor" {
			require.Contains(t, string(result.Definition), `"id":"feature"`)
		} else {
			require.NotContains(t, string(result.Definition), `"id":"role"`)
		}
	}
	source.failure = errors.New("userinfo unavailable")
	runtime.PrimitiveProviders.Invalidate("internal")
	ctx := menuPresentationContext("editor")
	connection, err := runtime.PrimitiveProviders.ConnectionForResource(ctx, uri, resources.InternalProviderIdentity)
	require.NoError(t, err)
	_, err = resources.Get[json.RawMessage](ctx, runtime.PrimitiveProviders, connection, identity.ResourceRef{URI: uri.String()}, nil)
	require.Error(t, err, "a facts-source failure must surface when the catalog generation is refreshed")
}

func TestPresentationIdentityDomainDoesNotChangeBFFWindowNamespace(t *testing.T) {
	source := &menuPresentationSource{}
	builder := NewBuilder().WithPresentationPrincipalResolver(source)
	runtime := &Runtime{UIBridge: forge.NewService(&forge.Config{}), AuthConfig: &auth.Config{Enabled: true}, publicPrimitiveDefault: true}
	builder.configureDefaultUIOwner(runtime)
	builder.configureWindowUIProjection(runtime)
	ctx := menuPresentationContext("editor")
	before, err := runtime.UIBridge.UIIdentity(ctx)
	require.NoError(t, err)
	require.Equal(t, "editor", before.Namespace)
	require.Zero(t, source.calls)
	projected, err := runtime.UIBridge.ProjectWindow(ctx, &types.Window{WindowKey: "plain"})
	require.NoError(t, err)
	require.Equal(t, 1, source.calls)
	require.Equal(t, "https://issuer.example", projected.AuthorizationSnapshot["principal"].(map[string]any)["tenantId"])
	after, err := runtime.UIBridge.UIIdentity(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "presentation domain must not replace the BFF user's existing UI namespace")
	require.Nil(t, runtime.PermittedResolver)
	require.Nil(t, runtime.ComponentAuthority)
	require.Nil(t, runtime.ExecutionContext)
	require.Nil(t, runtime.ToolAuthorizer)
	require.Nil(t, runtime.DatasourceDefinitionAuthorizer)
	require.False(t, runtime.DatasourceDisableCache)
}
