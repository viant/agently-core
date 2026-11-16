package sdk

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	reportingsvc "github.com/viant/agently-core/service/reporting"
	"github.com/viant/agently-core/service/reporting/catalog"
)

type reportingExecutorStub struct {
	name string
	args map[string]interface{}
	out  interface{}
}

func (s *reportingExecutorStub) ExecuteTool(_ context.Context, name string, args map[string]interface{}) (string, error) {
	s.name = name
	s.args = args
	encoded, err := json.Marshal(s.out)
	return string(encoded), err
}

func TestReportingClientSavedReportLifecycle(t *testing.T) {
	executor := &reportingExecutorStub{out: &reportingsvc.ListReportsResult{
		Reports: []*reportingsvc.ReportSummary{{ReportID: "activity", Title: "Activity"}},
	}}
	client := NewReportingClient(executor)
	listed, err := client.ListReports(context.Background(), &reportingsvc.ListReportsInput{Namespace: "example", Scope: &catalog.ScopeFilter{Key: "collection", Value: json.RawMessage(`"western"`)}})
	require.NoError(t, err)
	require.Equal(t, "reporting:list_reports", executor.name)
	require.Equal(t, "example", executor.args["namespace"])
	require.Equal(t, map[string]any{"key": "collection", "value": "western"}, executor.args["scope"])
	require.Len(t, listed.Reports, 1)

	executor.out = &reportingsvc.DeleteReportResult{ReportID: "activity", Deleted: true}
	deleted, err := client.DeleteReport(context.Background(), &reportingsvc.DeleteReportRequest{ReportID: "activity"})
	require.NoError(t, err)
	require.Equal(t, "reporting:delete_report", executor.name)
	require.True(t, deleted.Deleted)

	executor.out = &reportingsvc.ExportJobStatus{JobID: "job-1", Status: reportingsvc.JobStatusQueued}
	job, err := client.SubmitSavedReportExport(context.Background(), "activity", reportingsvc.ExportFormatPDF)
	require.NoError(t, err)
	require.Equal(t, "reporting:submit_export", executor.name)
	require.Equal(t, "job-1", job.JobID)
	require.Equal(t, "report", executor.args["source"].(map[string]interface{})["kind"])

	executor.out = &reportingsvc.ExportJobStatus{
		JobID:      "job-1",
		Status:     reportingsvc.JobStatusSucceeded,
		ArtifactID: "artifact-1",
	}
	status, err := client.GetExportStatus(context.Background(), "job-1")
	require.NoError(t, err)
	require.Equal(t, "reporting:get_export_status", executor.name)
	require.Equal(t, "job-1", executor.args["jobId"])
	require.Equal(t, reportingsvc.JobStatusSucceeded, status.Status)
	require.Equal(t, "artifact-1", status.ArtifactID)
}
