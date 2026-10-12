package executor

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	forge "github.com/viant/agently-core/service/primitiveprovider"
	reporting "github.com/viant/agently-core/service/reporting"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/forge/backend/reporting/registry"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultReportsAndOwningBuilderUseSameNativeGateway(t *testing.T) {
	root := t.TempDir()
	old := workspace.Root()
	workspace.SetRoot(root)
	defer workspace.SetRoot(old)
	files := map[string]string{
		"extension/forge/windows/metrics.yaml":   "id: metrics\nwindowKey: metrics\nreportBuilderRef: metrics\nview: {content: {id: metrics, kind: dashboard.reportBuilder, dashboard: {reportBuilderRef: metrics}}}\nresources: {dataSources: [items]}\n",
		"extension/forge/datasources/items.yaml": "id: items\nbackend: {kind: inline, rows: [{id: 1}]}\n",
		"extension/forge/reporting/builder.yaml": "kind: forge.reporting.builder\nid: metrics\ndataSourceRef: items\nreportBuilder: {document: {title: Metrics, blocks: [{id: intro, kind: markdownBlock, markdown: Original}]}}\n",
		"extension/forge/reporting/report.yaml":  "kind: forge.reporting.report\nid: original\nnamespace: public\nname: original\nresourceUri: report://public/original\nbuilderRef: metrics\nlabel: Original\ndocumentPatch: {title: Original}\n",
	}
	for file, raw := range files {
		path := filepath.Join(root, file)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, []byte(raw), 0600))
	}
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	rt := &Runtime{MCPManager: mgr, UIBridge: forge.NewService(&forge.Config{}), Reporting: reporting.New(reporting.Options{Store: reporting.NewMemoryStore(), Compiler: reporting.NewReportSpecCompiler(time.Now), Exporter: reporting.NewForgeExporter(nil)})}
	require.NoError(t, NewBuilder().configurePrimitiveProviders(rt))
	defer rt.PrimitiveProviders.Close()
	got, err := resources.ResolveDefinition[registry.ReportEnvelope](context.Background(), rt.PrimitiveProviders, "report", "report://public/original", nil)
	require.NoError(t, err)
	require.Equal(t, "report://public/original", got.Resource.URI)
	require.NotEmpty(t, got.Definition.DataSources["items"])
	result, err := rt.Reporting.ExecuteResource(context.Background(), &reporting.ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: "report://public/original"}})
	require.NoError(t, err)
	require.Equal(t, got.Resource.URI, result.Resource.URI)
	require.NotEmpty(t, result.ReportFill)
	require.NotEmpty(t, result.ReportPrint)
	require.Len(t, result.Datasets, 1)
	for _, dataset := range result.Datasets {
		require.Len(t, dataset.Rows, 1)
		require.EqualValues(t, 1, dataset.Rows[0]["id"])
	}
}
