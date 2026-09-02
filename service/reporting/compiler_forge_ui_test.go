package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	authsvc "github.com/viant/agently-core/service/auth"
)

type forgeUIViewResolverStub struct {
	resolved *ResolvedForgeUIView
	ref      string
	target   ForgeTargetContext
}

func (s *forgeUIViewResolverStub) ResolveForgeUIView(_ context.Context, ref string, target ForgeTargetContext, _ []string) (*ResolvedForgeUIView, error) {
	s.ref, s.target = ref, target
	return s.resolved, nil
}

func TestCompileAndExportForgeUIResolvesReferenceAndMergesOverrides(t *testing.T) {
	resolver := &forgeUIViewResolverStub{resolved: &ResolvedForgeUIView{
		UI:          json.RawMessage(`{"containers":[{"id":"rows","kind":"dashboard.table","title":"Rows","dataSourceRef":"rows","columns":[{"id":"id","name":"ID"}]}]}`),
		DataSources: map[string]json.RawMessage{"rows": json.RawMessage(`[{"id":1}]`)},
	}}
	service := New(Options{
		Exporter: NewForgeExporter(nil), Store: NewStoreAdapter(reportmemory.New()),
		ForgeUIViewResolver: resolver, NewID: func() string { return "forge-ui-id" },
	})
	ctx := authsvc.InjectUser(context.Background(), "user-1")
	result, err := service.CompileAndExportForgeUI(ctx, &CompileAndExportForgeUIRequest{
		ViewRef: "feed://catalog", DataSourceRefs: []string{"rows"},
		DataSourceOverrides: map[string]json.RawMessage{"rows": json.RawMessage(`[{"id":2}]`)},
		ReportID:            "catalog", Title: "Catalog", Format: ExportFormatPDF,
		ConversationID: "conversation-1", Target: ForgeTargetContext{Platform: "ios", FormFactor: "phone"},
	})
	require.NoError(t, err)
	require.Equal(t, "feed://catalog", resolver.ref)
	require.Equal(t, "ios", resolver.target.Platform)
	require.Equal(t, JobStatusSucceeded, result.Job.Status)
	stored, err := service.GetArtifact(ctx, result.Artifact.ArtifactID)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(stored.Data, []byte("%PDF-")))
}

func TestCompileAndExportForgeUIFailsClosedForMissingDatasource(t *testing.T) {
	service := New(Options{
		Store:               NewStoreAdapter(reportmemory.New()),
		ForgeUIViewResolver: &forgeUIViewResolverStub{resolved: &ResolvedForgeUIView{UI: json.RawMessage(`{"containers":[{"id":"rows","kind":"dashboard.table","dataSourceRef":"rows"}]}`)}},
	})
	_, err := service.CompileAndExportForgeUI(context.Background(), &CompileAndExportForgeUIRequest{
		ViewRef: "feed://catalog", DataSourceRefs: []string{"rows"}, ReportID: "catalog", Format: ExportFormatPDF,
	})
	require.ErrorContains(t, err, "datasource rows is unavailable")
}

func TestCompileAndExportForgeUIPrefersExactRenderedUIOverWorkspaceReload(t *testing.T) {
	resolver := &forgeUIViewResolverStub{resolved: &ResolvedForgeUIView{
		UI: json.RawMessage(`{"containers":[{"id":"wrong","title":"Reloaded workspace view"}]}`),
	}}
	service := New(Options{
		Exporter: NewForgeExporter(nil), Store: NewStoreAdapter(reportmemory.New()),
		ForgeUIViewResolver: resolver, NewID: func() string { return "inline-ui-id" },
	})
	ctx := authsvc.InjectUser(context.Background(), "user-1")
	result, err := service.CompileAndExportForgeUI(ctx, &CompileAndExportForgeUIRequest{
		ViewRef:        "feed://media-plan",
		UI:             json.RawMessage(`{"containers":[{"id":"exact","title":"Exact rendered view","dataSourceRef":"rows","columns":[{"key":"name","label":"Name"}]}]}`),
		DataSourceRefs: []string{"rows"},
		DataSourceOverrides: map[string]json.RawMessage{
			"rows": json.RawMessage(`{"collection":[{"name":"Visible"}]}`),
		},
		ReportID: "media-plan", Title: "Media Plan", Format: ExportFormatPDF,
	})
	require.NoError(t, err)
	require.Empty(t, resolver.ref)
	require.Equal(t, JobStatusSucceeded, result.Job.Status)
}
