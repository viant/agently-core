package reporting

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	svc "github.com/viant/agently-core/protocol/tool/service"
	authsvc "github.com/viant/agently-core/service/auth"
)

func TestServiceMethodsExposeReportingSurface(t *testing.T) {
	service := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Now:   func() time.Time { return time.Unix(0, 0).UTC() },
		NewID: func() string { return "job-1" },
	})

	signatures := service.Methods()
	require.Len(t, signatures, 7)
	require.Equal(t, Name, service.Name())
	require.Equal(t, "compile", signatures[0].Name)
	require.Equal(t, "submit_export", signatures[1].Name)
	require.Equal(t, "get_export_status", signatures[2].Name)
	require.Equal(t, "get_artifact", signatures[3].Name)
	require.True(t, signatures[4].Internal)
	require.True(t, signatures[5].Internal)
	require.True(t, signatures[6].Internal)
}

func TestServiceToolMethodDispatchesLifecycle(t *testing.T) {
	now := time.Date(2026, 6, 13, 13, 0, 0, 0, time.UTC)
	compiler := &compileRecorder{
		result: &CompileResult{
			ArtifactRef: "report://draft/performance",
			ReportSpec:  json.RawMessage(`{"kind":"reportSpec","version":1}`),
		},
	}
	service := New(Options{
		Compiler: compiler,
		Store:    NewStoreAdapter(reportmemory.New()),
		Now:      func() time.Time { return now },
		NewID: func() string {
			if compiler.request == nil {
				return "job-1"
			}
			return "artifact-1"
		},
	})
	ctx := authsvc.InjectUser(context.Background(), "user-1")

	compileMethod, err := service.Method("compile")
	require.NoError(t, err)
	compileOut := &CompileResult{}
	require.NoError(t, compileMethod(ctx, &CompileRequest{
		ArtifactRef: "report://draft/performance",
		SourceKind:  "draft",
		Document:    json.RawMessage(`{"kind":"reportDocument"}`),
	}, compileOut))
	require.Equal(t, "report://draft/performance", compileOut.ArtifactRef)

	submitMethod, err := service.Method("submit_export")
	require.NoError(t, err)
	jobOut := &ExportJob{}
	require.NoError(t, submitMethod(ctx, &SubmitExportRequest{
		ReportExportRequest: validTestReportExportRequestEnvelope(),
	}, jobOut))
	require.Equal(t, JobStatusQueued, jobOut.Status)
	require.Equal(t, ExportScopeSavedPayload, jobOut.Scope)

	startMethod, err := service.Method("start_export")
	require.NoError(t, err)
	require.NoError(t, startMethod(context.Background(), &StartExportInput{JobID: jobOut.JobID}, jobOut))
	require.Equal(t, JobStatusRunning, jobOut.Status)

	completeMethod, err := service.Method("complete_export")
	require.NoError(t, err)
	require.NoError(t, completeMethod(context.Background(), &CompleteExportRequest{
		JobID:       jobOut.JobID,
		ContentType: "application/pdf",
		Data:        []byte("%PDF"),
	}, jobOut))
	require.Equal(t, JobStatusSucceeded, jobOut.Status)
	require.NotEmpty(t, jobOut.ArtifactID)

	statusMethod, err := service.Method("get_export_status")
	require.NoError(t, err)
	statusOut := &ExportJob{}
	require.NoError(t, statusMethod(ctx, &GetExportStatusInput{JobID: jobOut.JobID}, statusOut))
	require.Equal(t, JobStatusSucceeded, statusOut.Status)

	artifactMethod, err := service.Method("get_artifact")
	require.NoError(t, err)
	artifactOut := &Artifact{}
	require.NoError(t, artifactMethod(ctx, &GetArtifactInput{ArtifactID: jobOut.ArtifactID}, artifactOut))
	require.Equal(t, []byte("%PDF"), artifactOut.Data)
}

func TestServiceMethodRejectsUnknownName(t *testing.T) {
	service := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
	})
	_, err := service.Method("missing")
	require.Error(t, err)
	require.ErrorContains(t, err, "method not found")
	var _ svc.Service = service
}
