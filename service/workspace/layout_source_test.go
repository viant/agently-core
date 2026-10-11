package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/policy"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/service/ui/permittedview"
	ws "github.com/viant/agently-core/workspace"
)

func TestCapturedLayoutUsesSameStartupBytesAndCurrentApplicationGate(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "ui/layout.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
	original := []byte(`version: 1
id: original
left: {width: {default: 200, min: 100, max: 300}}
topbar: {actions: [{id: header, title: Original Header, action: {type: window, windowKey: header}}]}
windowProviders:
  - {id: operations-ui, type: mcp, serverRef: operations, catalogTool: list_ui_windows, windowTool: get_ui_window}
applications:
  - id: original
    title: Original
    authorization: {resourceType: application}
    visibleWhen: {source: authorization, field: principal.features, contains: OPERATIONS_UI}
    menus:
      - id: operations
        title: Operations
        action: {type: window, provider: operations-ui, windowKey: overview, parameters: {opaque: same-original}}
`)
	require.NoError(t, os.WriteFile(file, original, 0600))
	old := ws.Root()
	ws.SetRoot(t.TempDir())
	t.Cleanup(func() { ws.SetRoot(old) })
	handler := NewMetadataHandler(nil, nil, "test")
	handler.SetAuthorizationRuntimes(nil, &permittedview.Runtime{Resolver: layoutResolver{features: []string{"OPERATIONS_UI"}}})
	def, err := resources.SnapshotLayoutMenuDefinition("ui/layout.yaml", "menu://team/layout")
	require.NoError(t, err)
	uri, _ := identity.ParseResourceURI(def.URI)
	var snapshot *resources.NativeAssetSnapshot
	captures := 0
	def.Load = resources.NativeLayoutMenuLoaderWithCapture(func(raw []byte) error {
		captures++
		return handler.SetCapturedLayout(root, "ui/layout.yaml", raw, func(ctx context.Context) error { _, err := snapshot.Candidates(ctx, uri); return err })
	})
	snapshot, err = resources.NewNativeAssetSnapshot(context.Background(), root, []resources.NativeSnapshotDefinition{def}, resources.NativeSnapshotOptions{AssetPaths: []string{"ui/layout.yaml"}, ImmutableUntilRestart: true})
	require.NoError(t, err)
	require.Equal(t, 1, captures)
	for i := 0; i < 4; i++ {
		got, err := handler.Layout(context.Background(), &LayoutRequest{})
		require.NoError(t, err)
		require.Equal(t, root, got.WorkspaceID)
		require.Len(t, got.Layout.Applications, 1)
		require.Equal(t, "Original Header", got.Layout.Topbar.Actions[0].Title)
		require.Equal(t, "same-original", got.Layout.Applications[0].Menus[0].Action.Parameters["opaque"])
	}
	require.NoError(t, handler.AuthorizeRemoteWindow(context.Background(), "operations-ui", "overview"))
	handler.SetPermittedRuntime(&permittedview.Runtime{Resolver: layoutResolver{}})
	require.ErrorIs(t, handler.AuthorizeRemoteWindow(context.Background(), "operations-ui", "overview"), policy.ErrDenied)
	denied, err := handler.Layout(context.Background(), &LayoutRequest{})
	require.NoError(t, err)
	require.Empty(t, denied.Layout.Applications)
	require.Equal(t, 1, captures, "request consumers do not parse/load authored YAML")
	after, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, original, after)
	require.NoError(t, os.WriteFile(file, append(original, []byte("\n# changed original\n")...), 0600))
	_, err = handler.Layout(context.Background(), &LayoutRequest{})
	require.Error(t, err)
	require.Error(t, handler.AuthorizeRemoteWindow(context.Background(), "operations-ui", "overview"))
	require.Equal(t, 1, captures, "changed source fails closed without hot YAML reload")
}

func TestPrivateExternalMenuKeepsCapturedEmbeddedShellAndOriginalOpenGate(t *testing.T) {
	root := t.TempDir()
	raw := []byte(`version: 1
id: private-shell
topbar: {actions: [{id: header, title: Original Header, action: {type: window, windowKey: header}}]}
windowProviders:
  - {id: operations-ui, type: mcp, serverRef: operations, catalogTool: list_ui_windows, windowTool: get_ui_window}
applications:
  - id: original
    title: Original
    authorization: {resourceType: application}
    visibleWhen: {source: authorization, field: principal.features, contains: OPERATIONS_UI}
    menus: [{id: operations, title: Operations, action: {type: window, provider: operations-ui, windowKey: overview}}]
`)
	captured, err := resources.CaptureNativeLayoutSource(context.Background(), root, raw)
	require.NoError(t, err)
	handler := NewMetadataHandler(nil, nil, "private-host")
	require.NoError(t, handler.SetCapturedLayout(captured.Root, captured.Source, captured.Raw, captured.Check))
	handler.SetAuthorizationRuntimes(nil, &permittedview.Runtime{Resolver: layoutResolver{features: []string{"OPERATIONS_UI"}}})
	// Explicit host-provided external menu remains authoritative; the startup
	// shell only supplies original header/providers and the direct OPEN gate.
	handler.SetLayoutMenuReader(func(context.Context) (*LayoutMenuRead, error) {
		return &LayoutMenuRead{Applications: []byte(`[{"id":"external","title":"External","menus":[{"id":"external-leaf","title":"External","action":{"type":"window","provider":"operations-ui","windowKey":"external"}}]}]`), Revision: "original-external", Close: func() error { return nil }, Revalidate: func(context.Context) error { return nil }}, nil
	})
	got, err := handler.Layout(context.Background(), &LayoutRequest{})
	require.NoError(t, err)
	require.Equal(t, "Original Header", got.Layout.Topbar.Actions[0].Title)
	require.Equal(t, "external", got.Layout.Applications[0].ID)
	require.NoError(t, handler.AuthorizeRemoteWindow(context.Background(), "operations-ui", "overview"))
	handler.SetPermittedRuntime(&permittedview.Runtime{Resolver: layoutResolver{}})
	require.ErrorIs(t, handler.AuthorizeRemoteWindow(context.Background(), "operations-ui", "overview"), policy.ErrDenied)
}
