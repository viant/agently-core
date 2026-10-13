package resource

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	authsvc "github.com/viant/agently-core/service/auth"
	dssvc "github.com/viant/agently-core/service/datasource"
)

func TestDatasourceDirectoryPreservesNativeDescriptorAndRejectsUnknownFields(t *testing.T) {
	actor := identity.VerifiedActor{Subject: "alice", Issuer: "fixture", TenantID: "team", AccountID: "account", IdentityRevision: "one", ValidUntil: time.Now().Add(time.Hour)}
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{{"valid", "id: authored-id\nselectors: {data: reply.rows}\nbackend:\n  kind: mcp_tool\n  service: arbitrary\n  method: original_method\n  pinned: {identity: 9007199254740993}\ncache: {ttl: 2m}\n", false}, {"fallback", "selectors: {data: reply.rows}\nbackend: {kind: mcp_tool, service: arbitrary, method: original_method}\n", false}, {"unknown", "id: source\nprivatePolicy: protected\nbackend: {kind: mcp_tool, service: arbitrary, method: original_method}\n", true}, {"conflictingPinned", "id: source\npinned: {Source: root}\nbackend: {kind: mcp_tool, service: arbitrary, method: original_method, pinned: {Source: nested}}\n", true}, {"malformedTemplate", "template: not-a-mapping\nid: source\nbackend: {kind: mcp_tool, service: arbitrary, method: original_method}\n", true}, {"unknownInherited", "template: &base\n  id: source\n  privatePolicy: granted\n  backend: {kind: mcp_tool, service: arbitrary, method: original_method}\n<<: *base\n", true}, {"unknownBackend", "id: source\nbackend: {kind: mcp_tool, service: arbitrary, method: original_method, permissions: granted}\n", true}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "sources"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(root, "sources/source.yaml"), []byte(tc.body), 0600))
			bindings, err := ExtensionBindings(context.Background(), root, []ExtensionDirectory{DatasourceDirectory("team", "sources")}, func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) (identity.ResourceRevisionPolicy, error) {
				return localFixturePolicy{actor: actor}, nil
			})
			require.NoError(t, err)
			require.Len(t, bindings, 1)
			resolver, err := bindings[0].Resolver(context.Background(), actor, "read")
			require.NoError(t, err)
			pin, err := resolver.Resolve(context.Background(), identity.ResourceRef{URI: bindings[0].URI})
			if tc.bad {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			raw, _, err := resolver.ReadResolved(context.Background(), *pin)
			require.NoError(t, err)
			descriptor, err := DecodeDatasourceDefinition(raw)
			require.NoError(t, err)
			require.Equal(t, "arbitrary", descriptor.Backend.Service)
			require.Equal(t, "original_method", descriptor.Backend.Method)
			if tc.name == "valid" {
				require.Equal(t, "authored-id", descriptor.ID)
				require.Equal(t, json.Number("9007199254740993"), descriptor.Backend.Pinned["identity"])
			} else {
				require.Equal(t, "source", descriptor.ID)
			}
		})
	}
}

func TestDatasourceTemplateAuthoringUsesConfinedExistingImports(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sources"), 0700))
	template := []byte("template: &base\n  id: $param(id)\n  backend: {kind: mcp_tool, service: arbitrary, method: original}\n  autoFetch: $param(autoFetch)\n<<: *base\nid: base\nautoFetch: false\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "sources/base.yaml"), template, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sources/imported.yaml"), []byte("\"$import('base.yaml:template', {id: imported, autoFetch: false})\"\n"), 0600))
	dirs := []ExtensionDirectory{DatasourceDirectory("workspace", "sources")}
	definitions, err := SnapshotExtensionDefinitions(context.Background(), root, dirs)
	require.NoError(t, err)
	snapshot, err := NewNativeAssetSnapshot(context.Background(), root, definitions, NativeSnapshotOptions{AssetPaths: []string{"sources"}, ImmutableUntilRestart: true})
	require.NoError(t, err)
	for _, name := range []string{"base", "imported"} {
		uri, err := identity.ParseResourceURI("datasource://workspace/" + name)
		require.NoError(t, err)
		candidates, err := snapshot.Candidates(context.Background(), uri)
		require.NoError(t, err)
		require.Len(t, candidates, 1)
		raw, err := snapshot.ReadCandidate(context.Background(), uri, candidates[0])
		require.NoError(t, err)
		ds, err := DecodeDatasourceDefinition(raw)
		require.NoError(t, err)
		require.Equal(t, name, ds.ID)
		require.NotNil(t, ds.AutoFetch)
		require.False(t, *ds.AutoFetch)
		require.Equal(t, "original", ds.Backend.Method)
	}
	actual, err := os.ReadFile(filepath.Join(root, "sources/base.yaml"))
	require.NoError(t, err)
	require.Equal(t, template, actual)
	// Imported template bytes belong to the same immutable startup snapshot.
	require.NoError(t, os.WriteFile(filepath.Join(root, "sources/base.yaml"), append(template, []byte("# changed authoring source\n")...), 0600))
	uri, err := identity.ParseResourceURI("datasource://workspace/imported")
	require.NoError(t, err)
	_, err = snapshot.Candidates(context.Background(), uri)
	require.Error(t, err)
}

func TestDatasourceOriginalWorkspaceSnapshotCompatibility(t *testing.T) {
	root := os.Getenv("AGENTLY_STEWARD_FIXTURE_ROOT")
	if root == "" {
		t.Skip("explicit read-only original workspace fixture not supplied")
	}
	definitions, err := SnapshotExtensionDefinitions(context.Background(), root, []ExtensionDirectory{DatasourceDirectory("workspace", "extension/forge/datasources")})
	require.NoError(t, err)
	require.NotEmpty(t, definitions)
	handle, err := os.OpenRoot(root)
	require.NoError(t, err)
	defer handle.Close()
	reader := &ExtensionReader{root: handle}
	defer reader.close()
	for _, definition := range definitions {
		_, loadErr := definition.Load(context.Background(), reader, definition.File)
		if loadErr != nil {
			t.Errorf("authored datasource %s: %v", definition.File, loadErr)
		}
	}
	if t.Failed() {
		return
	}
	_, err = NewNativeAssetSnapshot(context.Background(), root, definitions, NativeSnapshotOptions{AssetPaths: []string{"extension/forge/datasources"}, ImmutableUntilRestart: true})
	require.NoError(t, err)
	t.Logf("original datasource definitions compiled: %d", len(definitions))
}

type datasourceTemplateExecutor func(context.Context, string, map[string]interface{}) (string, error)

func (f datasourceTemplateExecutor) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	return f(ctx, name, args)
}
func TestDatasourceLegacyPinnedUsesExistingBackendDispatch(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "source.yaml"), []byte("id: source\npinned: {Source: FORGE_LINE_WORKSPACE}\nbackend: {kind: mcp_tool, service: arbitrary, method: original_method, pinned: {limit: 7}}\nselectors: {data: data}\ncache: {enabled: false}\n"), 0600))
	handle, err := os.OpenRoot(root)
	require.NoError(t, err)
	defer handle.Close()
	reader := &ExtensionReader{root: handle}
	defer reader.close()
	raw, err := NativeDatasourceLoader(context.Background(), reader, "source.yaml")
	require.NoError(t, err)
	ds, err := DecodeDatasourceDefinition(raw)
	require.NoError(t, err)
	store := dssvc.NewMemoryStore()
	store.Put(ds)
	calls := 0
	svc := dssvc.New(dssvc.Options{Store: store, Executor: datasourceTemplateExecutor(func(_ context.Context, name string, args map[string]interface{}) (string, error) {
		calls++
		require.Equal(t, "arbitrary:original_method", name)
		require.Equal(t, "FORGE_LINE_WORKSPACE", args["Source"])
		require.Equal(t, json.Number("7"), args["limit"])
		require.Equal(t, "original-input", args["filter"])
		return `{"data":[]}`, nil
	})})
	input := map[string]interface{}{"Source": "caller", "limit": 1, "filter": "original-input"}
	_, err = svc.Fetch(authsvc.InjectUser(context.Background(), "alice"), "source", input, dssvc.FetchOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, "caller", input["Source"])
	require.Equal(t, 1, input["limit"])
}
