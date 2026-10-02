package executor_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	execconfig "github.com/viant/agently-core/app/executor/config"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	auth "github.com/viant/agently-core/service/auth"
	reporting "github.com/viant/agently-core/service/reporting"
)

type lifetimeExporter struct{ started, canceled, release chan struct{} }

func (e *lifetimeExporter) Export(ctx context.Context, _ *reporting.RenderRequest) (*reporting.RenderResult, error) {
	close(e.started)
	<-ctx.Done()
	close(e.canceled)
	<-e.release
	return nil, ctx.Err()
}
func TestRuntimeCloseWaitsForOwnedReportWorkerAndRetainsBorrowedService(t *testing.T) {
	isolateActiveReportRunTestState(t)
	exporter := &lifetimeExporter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	service := reporting.New(reporting.Options{Store: reporting.NewStoreAdapter(reportmemory.New()), Exporter: exporter})
	rt, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithDefaults(&execconfig.Defaults{Reporting: execconfig.ReportingDefaults{Enabled: true, QueueIntervalMs: 1}}).WithReportingService(service).Build(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rt.Close(context.Background())) })
	t.Cleanup(func() {
		select {
		case <-exporter.release:
		default:
			close(exporter.release)
		}
	})
	owner := auth.InjectUser(context.Background(), "report-worker-owner")
	input := &reporting.SubmitExportRequest{ArtifactRef: "report://lifetime", Format: reporting.ExportFormatPDF, Scope: reporting.ExportScopeDraft, ReportPrint: json.RawMessage(`{"version":1,"kind":"reportPrint","specVersion":1,"specHash":"spec-1","fillVersion":1,"fillHash":"fill-1","source":{"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},"title":"Demo Report","pageGeometry":{"width":612,"height":792,"marginTop":48,"marginRight":48,"marginBottom":48,"marginLeft":48,"headerHeight":24,"footerHeight":24},"pages":[{"number":1,"elements":[{"id":"body-1","kind":"text","box":{"x":48,"y":96,"width":200,"height":18}}],"headerElements":[],"footerElements":[]}],"bookmarks":[{"id":"section-1","title":"Section 1","pageNumber":1}],"diagnostics":[]}`)}
	_, err = service.SubmitExport(owner, input)
	require.NoError(t, err)
	select {
	case <-exporter.started:
	case <-time.After(2 * time.Second):
		t.Fatal("owned worker did not enter exporter")
	}
	closed := make(chan error, 1)
	go func() { closed <- rt.Close(context.Background()) }()
	select {
	case <-exporter.canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime close did not cancel worker")
	}
	select {
	case err := <-closed:
		t.Fatalf("runtime closed before in-flight worker returned: %v", err)
	default:
	}
	close(exporter.release)
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not join canceled worker")
	}
	require.Same(t, service, rt.Reporting)
	queued, err := service.SubmitExport(owner, input)
	require.NoError(t, err)
	status, err := service.GetExportStatus(owner, queued.JobID)
	require.NoError(t, err)
	require.Equal(t, reporting.JobStatusQueued, status.Status)
}
