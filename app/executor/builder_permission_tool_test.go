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
	raw, err := json.Marshal(&permittedview.Snapshot{AuthorizationVersion: "current", ExpiresAt: time.Now().Add(time.Minute), GlobalCapabilities: map[string]bool{"create": true, "bulkEdit": false}})
	return string(raw), err
}

func TestBuilderConfiguresPrincipalPermissionToolWithoutEntityBootstrap(t *testing.T) {
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
	require.NotNil(t, rt.PermittedResolver, "configured principal permission tool does not require a row bootstrap")
	require.True(t, rt.UIBridge.HasWindowOpenAdmission())
	window := &types.Window{}
	require.NoError(t, json.Unmarshal([]byte(`{"windowKey":"advertiserList","authorization":{"scope":"principal","resourceType":"advertiser","requestedGlobalCapabilities":["create","bulkEdit"]},"view":{"content":{"id":"root","containers":[{"id":"create","visibleWhen":{"source":"authorization","field":"globalCapabilities.create","equals":true}},{"id":"bulkEdit","visibleWhen":{"source":"authorization","field":"globalCapabilities.bulkEdit","equals":true}}]}}}`), window))
	window.Resource = &identity.ResolvedResource{ValidUntil: time.Now().Add(time.Minute)}
	got, err := rt.UIBridge.AdmitWindowOpen(context.Background(), window, nil)
	require.NoError(t, err)
	require.Equal(t, 1, registry.calls)
	require.Equal(t, "steward:ResourceAuthorization", registry.name)
	require.Equal(t, []string{"create", "bulkEdit"}, registry.args["RequestedGlobalCapabilities"])
	require.Empty(t, registry.args["ResourceIDs"])
	require.Len(t, got.View.Content.Containers, 1, "configured tool's false capability still hides authored UI")
	require.Equal(t, "create", got.View.Content.Containers[0].ID)
	rowWindow := &types.Window{}
	require.NoError(t, json.Unmarshal([]byte(`{"windowKey":"advertiser","authorization":{"scope":"resource","resource":{"type":"advertiser","id":{"source":"resource","selector":"Id"}},"requestedCapabilities":["read"]},"view":{"content":{"id":"detail"}}}`), rowWindow))
	rowWindow.Resource = window.Resource
	_, err = rt.UIBridge.AdmitWindowOpen(context.Background(), rowWindow, map[string]any{"Id": 42})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	require.Equal(t, 1, registry.calls, "unverified request parameters cannot substitute for an explicit row bootstrap")
}
