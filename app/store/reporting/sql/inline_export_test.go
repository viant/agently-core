package sql

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	authctx "github.com/viant/agently-core/internal/auth"
	reporting "github.com/viant/agently-core/service/reporting"
	"github.com/viant/datly/bootstrap/connector"
	dexec "github.com/viant/datly/exec"
)

func TestNativeInlinePDFExportPersistsCompletedArtifact(t *testing.T) {
	ctx, store, db, server := reportingRuntime(t)
	verifyNativeInlinePDFExport(t, ctx, store, db, server, "inline-export-owner")
}

func TestNativeInlinePDFExportMySQL(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	ctx := context.Background()
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	defer db.Close()
	_, file, _, _ := runtime.Caller(0)
	server, err := native.New(ctx, native.Options{SourceRoot: filepath.Join(filepath.Dir(file), "../../../.."), Connectors: []connector.Config{{Name: "agently", Driver: "mysql", DSN: dsn}}})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	client, err := New(ctx, server, nil, nil)
	require.NoError(t, err)
	ownerID := fmt.Sprintf("inline-export-%d", time.Now().UnixNano())
	defer func() {
		_, err := db.Exec("DELETE FROM report_export_artifact WHERE owner_id=?", ownerID)
		require.NoError(t, err)
		_, err = db.Exec("DELETE FROM report_export_job WHERE owner_id=?", ownerID)
		require.NoError(t, err)
	}()
	verifyNativeInlinePDFExport(t, ctx, client.(*Store), db, server, ownerID)
}

func verifyNativeInlinePDFExport(t *testing.T, ctx context.Context, store *Store, db *sql.DB, server dexec.ComponentInvoker, ownerID string) {
	t.Helper()
	owner := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: ownerID})
	service := reporting.New(reporting.Options{Store: reporting.NewStoreAdapter(store), Exporter: reporting.NewForgeExporter(nil)})
	content := "```forge-report\n" +
		`{"version":1,"id":"inline","sequence":1,"mode":"start","grammar":"report-document-v1","title":"Inline report","blocks":[]}` +
		"\n```\n```forge-data\n" +
		`{"version":2,"id":"rows","reportRef":"inline","sequence":2,"format":"json","mode":"replace","data":[{"name":"Display","spend":12.5}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"inline","sequence":3,"mode":"append","blocks":[{"id":"spend","kind":"kpiBlock","datasetRef":"rows","valueField":"spend","valueFormat":"currency","title":"Spend"}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"inline","sequence":4,"mode":"commit"}` + "\n```"
	compiled, err := service.CompileFencedReport(owner, &reporting.CompileFencedReportRequest{Content: content, Format: reporting.ExportFormatPDF})
	require.NoError(t, err)
	submitted, err := service.SubmitExport(owner, &reporting.SubmitExportRequest{ReportExportRequest: compiled.ReportExportRequest})
	require.NoError(t, err)
	require.Equal(t, reporting.JobStatusQueued, submitted.Status)

	worker := reporting.NewWorker(service, reporting.WorkerOptions{BatchLimit: 1})
	result, err := worker.RunOnce(WithInternalAccess(context.Background()))
	require.NoError(t, err)
	require.Equal(t, 1, result.SucceededCount)

	// Reload through a new store/service to prove completion is durable, not just
	// the mutation's in-memory response or an uncommitted transaction snapshot.
	reloaded, err := New(ctx, server, nil, nil)
	require.NoError(t, err)
	restored := reporting.New(reporting.Options{Store: reporting.NewStoreAdapter(reloaded)})
	job, err := restored.GetExportStatus(owner, submitted.JobID)
	require.NoError(t, err)
	require.Equal(t, reporting.JobStatusSucceeded, job.Status)
	require.NotEmpty(t, job.ArtifactID)
	artifact, err := restored.GetArtifact(owner, job.ArtifactID)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(artifact.Data, []byte("%PDF-")))
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM report_export_artifact WHERE job_id=?", job.JobID).Scan(&count))
	require.Equal(t, 1, count)
}
