package reporting

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	authsvc "github.com/viant/agently-core/service/auth"
)

func TestWorkerRunOnceProcessesQueuedExports(t *testing.T) {
	exporter := &exportRecorder{
		result: &RenderResult{
			ContentType: "application/pdf",
			Data:        []byte("%PDF-worker"),
		},
	}
	idCounter := 0
	svc := New(Options{
		Exporter: exporter,
		Store:    NewStoreAdapter(reportmemory.New()),
		Now:      func() time.Time { return time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC) },
		NewID: func() string {
			idCounter++
			switch idCounter {
			case 1:
				return "job-1"
			default:
				return "artifact-1"
			}
		},
	})
	ctx := authsvc.InjectUser(context.Background(), "owner-1")
	_, err := svc.SubmitExport(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/worker",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)

	worker := NewWorker(svc, WorkerOptions{
		Interval:   time.Millisecond,
		BatchLimit: 1,
		Logger:     func(string, ...interface{}) {},
	})
	result, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.ProcessedCount)
	require.Equal(t, 1, result.SucceededCount)
	require.Len(t, result.Jobs, 1)
	require.Equal(t, JobStatusSucceeded, result.Jobs[0].Status)
}

func TestWorkerStartProcessesQueuedExportsOnInterval(t *testing.T) {
	exporter := &exportRecorder{
		result: &RenderResult{
			ContentType: "application/pdf",
			Data:        []byte("%PDF-worker-loop"),
		},
	}
	idCounter := 0
	svc := New(Options{
		Exporter: exporter,
		Store:    NewStoreAdapter(reportmemory.New()),
		Now:      func() time.Time { return time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC) },
		NewID: func() string {
			idCounter++
			switch idCounter {
			case 1:
				return "job-1"
			default:
				return "artifact-1"
			}
		},
	})
	ctx := authsvc.InjectUser(context.Background(), "owner-1")
	job, err := svc.SubmitExport(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/worker-loop",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := NewWorker(svc, WorkerOptions{
		Interval:   5 * time.Millisecond,
		BatchLimit: 1,
		Logger:     func(string, ...interface{}) {},
	})
	require.NoError(t, worker.Start(runCtx))

	require.Eventually(t, func() bool {
		status, err := svc.GetExportStatus(ctx, job.JobID)
		return err == nil && status != nil && status.Status == JobStatusSucceeded
	}, time.Second, 10*time.Millisecond)
}
