package executor_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	internalregistry "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	"github.com/viant/forge/backend/types"
)

type presentationPrincipalSource struct {
	principal gating.Principal
	failure   error
	calls     int
}

func (p *presentationPrincipalSource) ResolvePrincipal(context.Context) (gating.Principal, error) {
	p.calls++
	return p.principal, p.failure
}
func presentationRuntime(t *testing.T, source gating.PrincipalResolver) (*executor.Runtime, *builderPermissionRegistry) {
	t.Helper()
	root := t.TempDir()
	old := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(old) })
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("ui:\n  authorization:\n    tool: steward:ResourceAuthorization\n"), 0600))
	pool, err := manager.New(nil)
	require.NoError(t, err)
	base, err := internalregistry.NewWithManager(pool)
	require.NoError(t, err)
	registry := &builderPermissionRegistry{Registry: base}
	rt, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithMCPManager(pool).WithRegistry(registry).WithSkipRegistryInitialize(true).WithPresentationPrincipalResolver(source).Build(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rt.Close(context.Background())) })
	require.Nil(t, rt.PermittedResolver)
	require.False(t, rt.DatasourceDisableCache)
	require.Nil(t, rt.ToolAuthorizer)
	return rt, registry
}
func verifiedPresentationPrincipal() gating.Principal {
	return gating.Principal{AccountID: "21", Facts: authz.Facts{Subject: "verified-user", Issuer: "issuer", Tenant: "tenant", Roles: []string{"editor"}, Exposures: []string{"reports"}, ValidUntil: time.Now().Add(time.Minute)}}
}
func presentationWindow(t *testing.T) *types.Window {
	t.Helper()
	window := &types.Window{}
	require.NoError(t, json.Unmarshal([]byte(`{"windowKey":"record","authorization":{"scope":"resource","resourceType":"advertiser","requestedCapabilities":["read"]},"view":{"content":{"id":"root","containers":[{"id":"role","visibleWhen":{"source":"authorization","field":"principal.roles","contains":"editor"}},{"id":"exposure","visibleWhen":{"source":"authorization","field":"principal.features","contains":"reports"}},{"id":"account","visibleWhen":{"source":"authorization","field":"account.id","equals":"21"}},{"id":"denied","visibleWhen":{"source":"authorization","field":"principal.roles","contains":"missing"}}]}}}`), window))
	return window
}
func TestPresentationPrincipalBindingUsesOnlyVerifiedFactsAndUpdates(t *testing.T) {
	source := &presentationPrincipalSource{principal: verifiedPresentationPrincipal()}
	rt, registry := presentationRuntime(t, source)
	window := presentationWindow(t)
	first, err := rt.UIBridge.ProjectWindow(context.Background(), window)
	require.NoError(t, err)
	require.Equal(t, 1, source.calls)
	require.Zero(t, registry.calls)
	require.Equal(t, []string{"role", "exposure", "account"}, []string{first.View.Content.Containers[0].ID, first.View.Content.Containers[1].ID, first.View.Content.Containers[2].ID})
	require.Equal(t, "21", first.AuthorizationSnapshot["account"].(map[string]any)["id"])
	require.Nil(t, first.AuthorizationSnapshot["resources"])
	require.Nil(t, window.AuthorizationSnapshot)
	require.Len(t, window.View.Content.Containers, 4)
	source.principal.Facts.Roles[0] = "viewer"
	source.principal.Facts.Exposures = nil
	source.principal.AccountID = "22"
	next, err := rt.UIBridge.ProjectWindow(context.Background(), window)
	require.NoError(t, err)
	require.Equal(t, 2, source.calls)
	require.Empty(t, next.View.Content.Containers)
	require.NotEqual(t, first.AuthorizationSnapshot["authorizationVersion"], next.AuthorizationSnapshot["authorizationVersion"])
	require.Equal(t, "21", first.AuthorizationSnapshot["account"].(map[string]any)["id"], "previous snapshot remains owned")
	require.Zero(t, registry.calls)
}
func TestPresentationPrincipalBindingEagerUnguardedAndProducerPriority(t *testing.T) {
	source := &presentationPrincipalSource{principal: verifiedPresentationPrincipal()}
	rt, registry := presentationRuntime(t, source)
	unguarded := &types.Window{WindowKey: "plain"}
	projected, err := rt.UIBridge.ProjectWindow(context.Background(), unguarded)
	require.NoError(t, err)
	require.Equal(t, 1, source.calls)
	require.NotSame(t, unguarded, projected)
	require.Nil(t, unguarded.AuthorizationSnapshot)
	require.Equal(t, "21", projected.AuthorizationSnapshot["account"].(map[string]any)["id"])
	require.Equal(t, []any{"editor"}, projected.AuthorizationSnapshot["principal"].(map[string]any)["roles"])
	require.Equal(t, []any{"reports"}, projected.AuthorizationSnapshot["principal"].(map[string]any)["features"])
	source.failure = errors.New("verified userinfo unavailable")
	owned := presentationWindow(t)
	owned.AuthorizationSnapshot = map[string]any{"principal": map[string]any{"roles": []string{"producer-role"}}, "account": map[string]any{"id": "producer-account"}}
	got, err := rt.UIBridge.ProjectWindow(context.Background(), owned)
	require.NoError(t, err)
	require.Same(t, owned, got)
	require.Equal(t, 1, source.calls)
	for _, window := range []*types.Window{unguarded, presentationWindow(t)} {
		got, err = rt.UIBridge.ProjectWindow(context.Background(), window)
		require.ErrorIs(t, err, permittedview.ErrUnavailable)
		require.Nil(t, got)
	}
	require.Equal(t, 3, source.calls)
	require.Zero(t, registry.calls)
}
