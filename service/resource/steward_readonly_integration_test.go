package resource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	manager "github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	reporting "github.com/viant/agently-core/service/reporting"
	reportcatalog "github.com/viant/agently-core/service/reporting/catalog"
	workspacewindow "github.com/viant/agently-core/service/ui/window"
	"github.com/viant/forge/backend/reporting/registry"
	meta "github.com/viant/forge/backend/service/meta"
	"github.com/viant/forge/backend/types"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

// Explicit opt-in reads original authored assets; it never runs a backend,
// starts a server, rewrites YAML, or uses production credentials/authorization.
func TestStewardReadonlyInternalGatewayOriginalYAML(t *testing.T) {
	root := os.Getenv("STEWARD_NATIVE_YAML_ROOT")
	if root == "" {
		t.Skip("set STEWARD_NATIVE_YAML_ROOT to the unchanged Steward workspace")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	setupStart := time.Now()
	before := stewardAssetDigest(t, root)
	t.Logf("setup original asset hashing=%s", time.Since(setupStart))
	defer func() { require.Equal(t, before, stewardAssetDigest(t, root), "original Steward assets changed") }()
	raw, err := os.ReadFile(filepath.Join(root, "authorization/window-resources.json"))
	require.NoError(t, err)
	var inventory []workspacewindow.ResourceBinding
	require.NoError(t, json.Unmarshal(raw, &inventory))
	selected := []workspacewindow.ResourceBinding{}
	for _, entry := range inventory {
		if entry.WindowKey == "advertiserList" || entry.WindowKey == "campaign" {
			selected = append(selected, entry)
		}
	}
	require.Len(t, selected, 2)
	nativeSetupStart := time.Now()
	definitions, err := SnapshotWindowDefinitions(ctx, root, selected, nil)
	require.NoError(t, err)
	t.Logf("setup confined binding index=%s", time.Since(nativeSetupStart))
	builderWindows := map[string]string{"forecastingCubeBuilder": "forecastingCubeBuilder", "metricsCubeBuilder": "metricReportBuilder", "spoCubeBuilder": "spoReportBuilder", "supplyHygieneBuilder": "supplyHygieneReportBuilder"}
	reportOptions := reportcatalog.ReportResourceOptions{Options: registry.Options{WorkspaceRoot: root}, BuilderWindows: builderWindows, DataSources: func(ctx context.Context, builder *registry.Asset) (map[string]json.RawMessage, error) {
		return workspacewindow.LoadWorkspaceDatasourceDescriptorsAt(ctx, root, builderWindows[builder.ID], nil)
	}}
	reports, err := reportcatalog.NewReportResourceSource(reportOptions)
	require.NoError(t, err)
	reportSetupStart := time.Now()
	reportInventory, err := reports.List(ctx)
	require.NoError(t, err)
	require.Len(t, reportInventory, 10)
	t.Logf("setup original report inventory=%s", time.Since(reportSetupStart))
	rawReportBindings, err := os.ReadFile(filepath.Join(root, "authorization/report-resources.json"))
	require.NoError(t, err)
	var reportPaths []struct {
		URI  string `json:"resourceUri"`
		File string `json:"sourcePath"`
	}
	require.NoError(t, json.Unmarshal(rawReportBindings, &reportPaths))
	paths := map[string]string{}
	for _, entry := range reportPaths {
		paths[entry.URI] = entry.File
	}
	for _, entry := range reportInventory {
		require.Equal(t, "steward", entry.Namespace)
		require.NotEmpty(t, paths[entry.URI])
		definitions = append(definitions, NativeSnapshotDefinition{URI: entry.URI, Title: entry.Title, FormatVersion: 1, File: paths[entry.URI], Load: NativeReportLoader(entry.URI, reportOptions, nil)})
	}
	preloadStart := time.Now()
	snapshot, err := NewNativeAssetSnapshot(ctx, root, definitions, NativeSnapshotOptions{ImmutableUntilRestart: true})
	require.NoError(t, err)
	t.Logf("startup nativepreload elapsed=%s compiles=%d", time.Since(preloadStart), snapshot.CompileCount())
	bindings, err := snapshot.Bindings("internal", extensionPolicy)
	require.NoError(t, err)
	actor := identity.VerifiedActor{Subject: "synthetic-readonly", Issuer: "https://synthetic.invalid", TenantID: "fixture", AccountID: "fixture", IdentityRevision: "readonly-1", ValidUntil: time.Now().Add(20 * time.Minute)}
	actorResolver := func(context.Context) (identity.VerifiedActor, error) { return actor, nil }
	verify := func(context.Context, identity.VerifiedActor) error { return nil }
	provider, err := NewLocalProvider(LocalConfig{ProviderIdentity: "internal", Actor: actorResolver, Verify: verify, Authorize: func(_ context.Context, _ identity.VerifiedActor, uri identity.ResourceURI, _ string) error {
		if uri.Namespace != "steward" {
			return identity.ErrResourceDenied
		}
		return nil
	}, Bindings: bindings, WindowIndex: snapshot.WindowIndex, WindowListVisibility: func(context.Context, identity.VerifiedActor, LocalResourceBinding) (bool, error) { return true, nil }, Validators: map[string]LocalResourceValidator{"window": ValidateWindowBundle, "report": ValidateReportEnvelope}})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	require.NoError(t, mgr.RegisterLocal(ctx, "internal", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "internal"}, func(context.Context) (mcpclient.Interface, error) { return NewLocalMCPClient(provider) }))
	gateway := NewGateway(mgr, actorResolver, verify, "synthetic-gateway")
	defer gateway.Close()
	reportGateway := &ReportGatewayCatalog{Gateway: gateway, Admission: func(context.Context, string, identity.ResolvedResource, json.RawMessage) error { return nil }}
	reportService := reporting.New(reporting.Options{Store: reporting.NewMemoryStore(), ReportCatalog: reportGateway, Compiler: reporting.NewReportSpecCompiler(time.Now)})
	reportService.SetResourceReaderFactory(reportGateway.Reader)

	kinds := []string{"window", "report"}
	if os.Getenv("STEWARD_NATIVE_YAML_REPORTS_ONLY") == "1" {
		kinds = []string{"report"}
	}
	for _, kind := range kinds {
		phaseStart := time.Now()
		rows, err := gateway.List(ctx, kind, "steward")
		t.Logf("gateway %s list elapsed=%s", kind, time.Since(phaseStart))
		require.NoError(t, err)
		expected := 2
		if kind == "report" {
			expected = 10
		}
		require.Len(t, rows, expected)
		for _, row := range rows {
			t.Run(row.Resource.URI, func(t *testing.T) {
				require.Equal(t, "internal", row.Connection.ProviderIdentity)
				getStart := time.Now()
				got, err := gateway.Get(ctx, row.Connection, identity.ResourceRef{URI: row.Resource.URI}, nil)
				t.Logf("gateway get elapsed=%s", time.Since(getStart))
				require.NoError(t, err)
				require.Equal(t, "steward", got.Resource.Namespace)
				require.Equal(t, identity.WorkingCandidate, got.ResolvedResource.Kind)
				require.Empty(t, got.ResolvedResource.Revision)
				if kind == "window" {
					key := ""
					for _, entry := range selected {
						if entry.URI == row.Resource.URI {
							key = entry.WindowKey
						}
					}
					for _, target := range []types.WindowTarget{{Platform: "web"}, {Platform: "ios", FormFactor: "phone"}, {Platform: "android", FormFactor: "tablet"}} {
						variant, err := types.SelectWindowResource(got.Resource.Definition, &target)
						require.NoError(t, err)
						legacy, err := workspacewindow.LoadWorkspaceWindowWithEnricherAt(ctx, root, key, &meta.TargetContext{Platform: target.Platform, FormFactor: target.FormFactor}, nil)
						require.NoError(t, err)
						require.NotNil(t, legacy)
						actual := *variant.Window
						actual.Resource = nil
						actual.ResourceTarget = nil
						actual.ResourceDependencies = nil
						legacy.Resource = nil
						legacy.ResourceTarget = nil
						legacy.ResourceDependencies = nil
						a, err := json.Marshal(actual)
						require.NoError(t, err)
						b, err := json.Marshal(legacy)
						require.NoError(t, err)
						require.JSONEq(t, string(b), string(a), "native authored window diverged for target %s", target.ProfileKey())
						t.Logf("native target=%s datasourceDescriptors=%d fingerprint=%s", target.ProfileKey(), len(variant.DataSources), variant.Fingerprint)
					}
				} else {
					original, err := reports.Materialize(ctx, row.Resource.URI)
					require.NoError(t, err)
					require.Equal(t, []byte(original), got.Resource.DefinitionBytes)
					serviceGet, err := reportService.GetReport(ctx, &reporting.GetReportInput{ResolvedResource: got.ResolvedResource})
					require.NoError(t, err)
					require.Equal(t, got.ResolvedResource.ProviderIdentity, serviceGet.Resource.ProviderIdentity)
					compiled, err := reportService.Compile(ctx, &reporting.CompileRequest{ResolvedResource: got.ResolvedResource})
					require.NoError(t, err)
					require.Equal(t, got.ResolvedResource.ProviderIdentity, compiled.Resource.ProviderIdentity)
					require.Equal(t, got.ResolvedResource.ResourceCandidate, compiled.Resource.ResourceCandidate)
					require.Equal(t, got.ResolvedResource.AuthorityBinding, compiled.Resource.AuthorityBinding)
					require.False(t, compiled.Resource.ValidUntil.After(got.ResolvedResource.ValidUntil))
					spec := compiled.ReportSpec
					var document map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(spec, &document))
					require.NotEmpty(t, document["datasets"])
					t.Logf("authored report lowered bytes=%d candidate=%s", len(spec), got.ResolvedResource.ContentFingerprint)
				}
			})
		}
	}
	serviceList, err := reportService.ListReports(ctx, &reporting.ListReportsInput{Namespace: "steward"})
	require.NoError(t, err)
	require.Len(t, serviceList.Reports, 10)
	require.EqualValues(t, len(definitions), snapshot.CompileCount(), "serving must not recompile native assets")
	t.Logf("readonly original assets sha256=%s", before)
}
func stewardAssetDigest(t *testing.T, root string) string {
	t.Helper()
	var names []string
	require.NoError(t, filepath.WalkDir(filepath.Join(root, "extension/forge"), func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			names = append(names, name)
		}
		return nil
	}))
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		raw, err := os.ReadFile(name)
		require.NoError(t, err)
		relative, err := filepath.Rel(root, name)
		require.NoError(t, err)
		hash.Write([]byte(relative))
		hash.Write([]byte{0})
		hash.Write(raw)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
