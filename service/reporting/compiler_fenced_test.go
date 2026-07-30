package reporting

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	authsvc "github.com/viant/agently-core/service/auth"
)

func TestCompileFencedReportReturnsSubmitExportEnvelope(t *testing.T) {
	service := New(Options{
		Exporter: NewForgeExporter(nil),
		Store:    NewStoreAdapter(reportmemory.New()),
		NewID: func() string {
			return "fixed-id"
		},
	})
	content := "```forge-report\n" +
		`{"version":1,"id":"backend","sequence":1,"mode":"start","grammar":"report-document-v1","title":"Backend report","blocks":[]}` +
		"\n```\n```forge-data\n" +
		`{"version":2,"id":"rows","reportRef":"backend","sequence":2,"format":"json","mode":"replace","data":[{"name":"Display","spend":12.5}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"backend","sequence":3,"mode":"append","blocks":[{"id":"spend","kind":"kpiBlock","datasetRef":"rows","valueField":"spend","valueLabel":"Spend","valueFormat":"currency","title":"Spend"}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"backend","sequence":4,"mode":"commit"}` +
		"\n```"

	compiled, err := service.CompileFencedReport(context.Background(), &CompileFencedReportRequest{
		Content: content, ReportID: "backend", Format: ExportFormatPDF,
	})
	require.NoError(t, err)
	require.Equal(t, "backend", compiled.ReportID)
	require.NotNil(t, compiled.ReportExportRequest)
	require.Equal(t, "reportExportRequest", compiled.ReportExportRequest.Kind)

	ctx := authsvc.InjectUser(context.Background(), "user-1")
	job, err := service.SubmitExport(ctx, &SubmitExportRequest{ReportExportRequest: compiled.ReportExportRequest})
	require.NoError(t, err)
	require.Equal(t, JobStatusQueued, job.Status)
	job, err = service.RunExport(ctx, job.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, job.Status)
	artifact, err := service.GetArtifact(ctx, job.ArtifactID)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(artifact.Data, []byte("%PDF-")))
}
