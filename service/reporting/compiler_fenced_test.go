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

func TestCompileFencedReportReturnsSubmitExportEnvelope(t *testing.T) {
	service := New(Options{
		Exporter: NewForgeExporter(nil),
		Store:    NewStoreAdapter(reportmemory.New()),
		NewID: func() string {
			return "fixed-id"
		},
	})
	content := validFencedReportContent()

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

func TestCompileAndExportFencedReportReturnsCompactArtifact(t *testing.T) {
	service := New(Options{
		Exporter: NewForgeExporter(nil),
		Store:    NewStoreAdapter(reportmemory.New()),
		NewID: func() string {
			return "fixed-id"
		},
	})
	ctx := authsvc.InjectUser(context.Background(), "user-1")
	result, err := service.CompileAndExportFencedReport(ctx, &CompileAndExportFencedReportRequest{
		Content:        validFencedReportContent(),
		ReportID:       "backend",
		Format:         ExportFormatPDF,
		ConversationID: "conversation-1",
		WorkspaceID:    "workspace-1",
	})
	require.NoError(t, err)
	require.Equal(t, "backend", result.ReportID)
	require.NotNil(t, result.Job)
	require.Equal(t, JobStatusSucceeded, result.Job.Status)
	require.Equal(t, "conversation-1", result.Job.ConversationID)
	require.Equal(t, "workspace-1", result.Job.WorkspaceID)
	require.Empty(t, result.Job.ReportSpec)
	require.Empty(t, result.Job.ReportFill)
	require.Empty(t, result.Job.ReportPrint)
	require.NotNil(t, result.Artifact)
	require.NotEmpty(t, result.Artifact.ArtifactID)
	require.Equal(t, "application/pdf", result.Artifact.ContentType)
	require.Empty(t, result.Artifact.Data)

	stored, err := service.GetArtifact(ctx, result.Artifact.ArtifactID)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(stored.Data, []byte("%PDF-")))
}

func TestCompileFencedReportAcceptsStringEncodedFencePayloads(t *testing.T) {
	service := New(Options{
		Exporter: NewForgeExporter(nil),
		Store:    NewStoreAdapter(reportmemory.New()),
	})
	asString := func(value string) json.RawMessage {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		return encoded
	}
	result, err := service.CompileFencedReport(context.Background(), &CompileFencedReportRequest{
		ReportID: "string-payload-report",
		Fences: []FencedReportFence{
			{
				Kind:  "forge-report",
				Index: 1,
				Payload: asString(
					`{"version":1,"scope":"test","id":"string-payload-report","sequence":1,"mode":"start","grammar":"report-document-v1","title":"String payload","blocks":[]}`,
				),
			},
			{
				Kind:  "forge-data",
				Index: 2,
				Payload: asString(
					`{"version":2,"scope":"test","id":"rows","reportRef":"string-payload-report","sequence":2,"format":"json","mode":"replace","data":[{"spend":12.5}]}`,
				),
			},
			{
				Kind:  "forge-report",
				Index: 3,
				Payload: asString(
					`{"version":1,"scope":"test","id":"string-payload-report","sequence":3,"mode":"append","blocks":[{"id":"spend","kind":"kpiBlock","datasetRef":"rows","valueField":"spend","valueLabel":"Spend","valueFormat":"currency","title":"Spend"}]}`,
				),
			},
			{
				Kind:  "forge-report",
				Index: 4,
				Payload: asString(
					`{"version":1,"scope":"test","id":"string-payload-report","sequence":4,"mode":"commit"}`,
				),
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "string-payload-report", result.ReportID)
	require.NotNil(t, result.ReportExportRequest)
}

func validFencedReportContent() string {
	return "```forge-report\n" +
		`{"version":1,"id":"backend","sequence":1,"mode":"start","grammar":"report-document-v1","title":"Backend report","blocks":[]}` +
		"\n```\n```forge-data\n" +
		`{"version":2,"id":"rows","reportRef":"backend","sequence":2,"format":"json","mode":"replace","data":[{"name":"Display","spend":12.5}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"backend","sequence":3,"mode":"append","blocks":[{"id":"spend","kind":"kpiBlock","datasetRef":"rows","valueField":"spend","valueLabel":"Spend","valueFormat":"currency","title":"Spend"}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"backend","sequence":4,"mode":"commit"}` +
		"\n```"
}
