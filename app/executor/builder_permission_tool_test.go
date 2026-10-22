package executor_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	internalregistry "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/forge/backend/types"
)

type builderPermissionRegistry struct {
	*internalregistry.Registry
	calls int
	name  string
	args  map[string]interface{}
}

func (r *builderPermissionRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	r.calls++
	r.name, r.args = name, args
	raw, err := json.Marshal(&permittedview.Snapshot{AuthorizationVersion: "current", ExpiresAt: time.Now().Add(time.Minute), Principal: map[string]any{"features": []string{"create"}, "roles": []string{"operator"}, "exposures": []string{"REPORTS_UI"}}})
	return string(raw), err
}

func TestBuilderDoesNotInstallLegacyResourceAuthorizationPreflight(t *testing.T) {
	root := t.TempDir()
	old := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(old) })
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("ui:\n  authorization:\n    tool: steward:ResourceAuthorization\n"), 0600))
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	base, err := internalregistry.NewWithManager(mgr)
	require.NoError(t, err)
	registry := &builderPermissionRegistry{Registry: base}
	rt, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithMCPManager(mgr).WithRegistry(registry).WithSkipRegistryInitialize(true).Build(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rt.Close(context.Background())) })
	require.Nil(t, rt.PermittedResolver)
	require.False(t, rt.UIBridge.HasWindowUIProjection())
	window := &types.Window{}
	require.NoError(t, json.Unmarshal([]byte(`{"windowKey":"advertiserList","authorization":{"scope":"principal","resourceType":"advertiser","requestedGlobalCapabilities":["create","bulkEdit"]},"view":{"content":{"id":"root","containers":[{"id":"create","visibleWhen":{"source":"authorization","field":"principal.features","contains":"create"}},{"id":"bulkEdit","visibleWhen":{"source":"authorization","field":"principal.features","contains":"bulkEdit"}},{"id":"operator","visibleWhen":{"source":"authorization","field":"principal.roles","contains":"operator"}},{"id":"reports","visibleWhen":{"source":"authorization","field":"principal.exposures","contains":"REPORTS_UI"}}]}}}`), window))
	window.Resource = &identity.ResolvedResource{ValidUntil: time.Now().Add(time.Minute)}
	got, err := rt.UIBridge.ProjectWindow(context.Background(), window)
	require.NoError(t, err)
	require.Zero(t, registry.calls, "legacy ResourceAuthorization must not be called for UI presentation")
	require.Len(t, got.View.Content.Containers, 4, "authored conditions remain for configured user-info adaptation")
}
