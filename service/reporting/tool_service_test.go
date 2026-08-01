package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
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
	require.Len(t, signatures, 26)
	require.Equal(t, Name, service.Name())
	require.Equal(t, "compile", signatures[0].Name)
	require.Equal(t, "compile_fenced_report", signatures[1].Name)
	require.Equal(t, "compile_and_export_fenced_report", signatures[2].Name)
	require.Equal(t, "record_audit_event", signatures[3].Name)
	require.Equal(t, "share_artifact", signatures[4].Name)
	require.Equal(t, "transition_artifact", signatures[5].Name)
	require.Equal(t, "export_report", signatures[6].Name)
	require.Equal(t, "submit_export", signatures[7].Name)
	require.Equal(t, "get_export_status", signatures[8].Name)
	require.Contains(t, signatures[8].Description, "compact")
	require.Equal(t, reflect.TypeOf(&ExportJobStatus{}), signatures[8].Output)
	require.Equal(t, "list_export_jobs", signatures[9].Name)
	require.Equal(t, "list_export_artifacts", signatures[10].Name)
	require.Equal(t, "get_artifact", signatures[11].Name)
	require.Equal(t, "get_shared_artifact", signatures[12].Name)
	require.Equal(t, "list_shared_artifacts", signatures[13].Name)
	require.Equal(t, "save_report", signatures[14].Name)
	require.Equal(t, "get_report", signatures[15].Name)
	require.Equal(t, "list_reports", signatures[16].Name)
	require.Equal(t, "update_report", signatures[17].Name)
	require.Equal(t, "duplicate_report", signatures[18].Name)
	require.Equal(t, "delete_report", signatures[19].Name)
	require.Equal(t, "record_report_run", signatures[20].Name)
	require.True(t, signatures[21].Internal)
	require.True(t, signatures[22].Internal)
	require.True(t, signatures[23].Internal)
	require.True(t, signatures[24].Internal)
	require.True(t, signatures[25].Internal)
}

func TestServiceToolMethodDispatchesLifecycle(t *testing.T) {
	now := time.Date(2026, 6, 13, 13, 0, 0, 0, time.UTC)
	compiler := &compileRecorder{
		result: &CompileResult{
			ArtifactRef: "report://draft/performance",
			ReportSpec:  json.RawMessage(`{"kind":"reportSpec","version":1}`),
		},
	}
	exporter := &exportRecorder{
		result: &RenderResult{
			ContentType: "application/pdf",
			Data:        []byte("%PDF-run"),
		},
	}
	service := New(Options{
		Compiler: compiler,
		Exporter: exporter,
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

	runMethod, err := service.Method("run_export")
	require.NoError(t, err)
	require.NoError(t, runMethod(context.Background(), &RunExportInput{JobID: jobOut.JobID}, jobOut))
	require.Equal(t, JobStatusSucceeded, jobOut.Status)
	require.NotEmpty(t, jobOut.ArtifactID)
	require.NotNil(t, exporter.request)
	require.Equal(t, jobOut.JobID, exporter.request.JobID)
	require.Equal(t, ExportFormatPDF, exporter.request.Format)

	statusMethod, err := service.Method("get_export_status")
	require.NoError(t, err)
	statusOut := &ExportJobStatus{}
	require.NoError(t, statusMethod(ctx, &GetExportStatusInput{JobID: jobOut.JobID}, statusOut))
	require.Equal(t, JobStatusSucceeded, statusOut.Status)

	statusJSON, err := json.Marshal(statusOut)
	require.NoError(t, err)
	var statusFields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(statusJSON, &statusFields))
	for _, field := range []string{
		"reportSpec",
		"reportFill",
		"reportPrint",
		"metadata",
		"authContextRef",
		"exportRequestId",
		"reportRunRevision",
	} {
		require.NotContains(t, statusFields, field)
	}

	listMethod, err := service.Method("list_export_jobs")
	require.NoError(t, err)
	listOut := &ListExportJobsResult{}
	require.NoError(t, listMethod(ctx, &ListExportJobsInput{Limit: 1}, listOut))
	require.Equal(t, 1, listOut.TotalCount)
	require.Len(t, listOut.Jobs, 1)
	require.Equal(t, jobOut.JobID, listOut.Jobs[0].JobID)

	listArtifactsMethod, err := service.Method("list_export_artifacts")
	require.NoError(t, err)
	artifactsOut := &ListExportArtifactsResult{}
	require.NoError(t, listArtifactsMethod(ctx, &ListExportArtifactsInput{Limit: 1}, artifactsOut))
	require.Equal(t, 1, artifactsOut.TotalCount)
	require.Len(t, artifactsOut.Artifacts, 1)
	require.Equal(t, jobOut.ArtifactID, artifactsOut.Artifacts[0].ArtifactID)

	artifactMethod, err := service.Method("get_artifact")
	require.NoError(t, err)
	artifactOut := &Artifact{}
	require.NoError(t, artifactMethod(ctx, &GetArtifactInput{ArtifactID: jobOut.ArtifactID}, artifactOut))
	require.Empty(t, artifactOut.Data)

	artifactWithData := &Artifact{}
	require.NoError(t, artifactMethod(ctx, &GetArtifactInput{
		ArtifactID:  jobOut.ArtifactID,
		IncludeData: true,
	}, artifactWithData))
	require.Equal(t, []byte("%PDF-run"), artifactWithData.Data)
}

func TestServiceToolMethodRecordsAuditEvents(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	audit := &auditRecorder{}
	service := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Audit: audit,
		Now:   func() time.Time { return now },
	})
	ctx := authsvc.InjectUser(context.Background(), "owner-1")

	recordMethod, err := service.Method("record_audit_event")
	require.NoError(t, err)
	output := &AuditEvent{}
	require.NoError(t, recordMethod(ctx, &RecordAuditEventInput{
		Event: &AuditEvent{
			EventType:   "report.publish",
			ArtifactRef: "reportBuilder.savedView://saved_view_capacity_q3",
			Version:     8,
			ActorRef:    "user://awitas",
			Metadata: map[string]interface{}{
				"source": "reportBuilder",
			},
		},
	}, output))
	require.Equal(t, "report.publish", output.EventType)
	require.Equal(t, 8, output.Version)
	require.Equal(t, "user://awitas", output.ActorRef)
	require.Equal(t, "user://awitas", output.ActorID)
	require.Equal(t, now, output.OccurredAt)
	require.Len(t, audit.events, 1)
	require.Equal(t, "report.publish", audit.events[0].EventType)
	require.Equal(t, "user://awitas", audit.events[0].ActorRef)
	require.Equal(t, "user://awitas", audit.events[0].ActorID)
}

func TestServiceToolMethodCreatesAndTransitionsSharedArtifacts(t *testing.T) {
	now := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	idCount := 0
	service := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Now:   func() time.Time { return now },
		NewID: func() string {
			idCount++
			return "shared-" + string(rune('0'+idCount))
		},
	})
	ctx := authsvc.InjectUser(context.Background(), "owner-1")

	shareMethod, err := service.Method("share_artifact")
	require.NoError(t, err)
	shared := &SharedArtifact{}
	require.NoError(t, shareMethod(ctx, &ShareArtifactRequest{
		ArtifactRef:    "reportBuilder.savedReportPayload://rbreport_forecasting_q3",
		Version:        4,
		Lifecycle:      "draft",
		ReportDocument: json.RawMessage(`{"kind":"reportDocument","id":"forecastingQ3","title":"Forecasting Q3"}`),
		ReportExportRequest: &ReportExportRequest{
			Version: 1,
			Kind:    "reportExportRequest",
			Target:  ReportExportTarget{Format: ExportFormatPDF},
			Source: ReportExportSource{
				From:             "savedPayload",
				ArtifactKind:     "reportBuilder.savedReportPayload",
				ArtifactRef:      "reportBuilder.savedReportPayload://rbreport_forecasting_q3",
				Title:            "Forecasting Q3",
				ReportID:         "forecastingQ3",
				PayloadID:        "rbreport_forecasting_q3",
				SourceArtifactID: "forecasting_q3",
				DocumentVersion:  4,
			},
			ReportSpec:  json.RawMessage(validTestReportSpecJSON()),
			ReportFill:  json.RawMessage(validTestReportFillJSON()),
			ReportPrint: json.RawMessage(validTestReportPrintJSON()),
		},
	}, shared))
	require.Equal(t, "reportBuilder.savedView", shared.Kind)
	require.Equal(t, "draft", shared.Lifecycle)
	require.Equal(t, "reportBuilder.savedView://saved_view_forecastingQ3", shared.ArtifactRef)
	require.Equal(t, "saved_view_forecastingQ3", shared.SourceArtifactID)

	transitionMethod, err := service.Method("transition_artifact")
	require.NoError(t, err)
	published := &SharedArtifact{}
	require.NoError(t, transitionMethod(ctx, &TransitionArtifactRequest{
		ArtifactRef:    "reportBuilder.savedReportPayload://rbreport_forecasting_q3",
		To:             "published",
		Version:        4,
		ReportDocument: json.RawMessage(`{"kind":"reportDocument","id":"forecastingQ3","title":"Forecasting Q3"}`),
		ReportExportRequest: &ReportExportRequest{
			Version: 1,
			Kind:    "reportExportRequest",
			Target:  ReportExportTarget{Format: ExportFormatPDF},
			Source: ReportExportSource{
				From:             "savedPayload",
				ArtifactKind:     "reportBuilder.savedReportPayload",
				ArtifactRef:      "reportBuilder.savedReportPayload://rbreport_forecasting_q3",
				Title:            "Forecasting Q3",
				ReportID:         "forecastingQ3",
				PayloadID:        "rbreport_forecasting_q3",
				SourceArtifactID: "forecasting_q3",
				DocumentVersion:  4,
			},
			ReportSpec:  json.RawMessage(validTestReportSpecJSON()),
			ReportFill:  json.RawMessage(validTestReportFillJSON()),
			ReportPrint: json.RawMessage(validTestReportPrintJSON()),
		},
	}, published))
	require.Equal(t, "reportBuilder.publishedSnapshot", published.Kind)
	require.Equal(t, "published", published.Lifecycle)
	require.Equal(t, "reportBuilder.publishedSnapshot://published_snapshot_forecastingQ3", published.ArtifactRef)
	require.Equal(t, "published_snapshot_forecastingQ3", published.SourceArtifactID)

	getSharedMethod, err := service.Method("get_shared_artifact")
	require.NoError(t, err)
	gotShared := &SharedArtifact{}
	require.NoError(t, getSharedMethod(ctx, &GetSharedArtifactInput{ArtifactID: shared.ArtifactID}, gotShared))
	require.Equal(t, shared.ArtifactRef, gotShared.ArtifactRef)

	listSharedMethod, err := service.Method("list_shared_artifacts")
	require.NoError(t, err)
	listResult := &ListSharedArtifactsResult{}
	require.NoError(t, listSharedMethod(ctx, &ListSharedArtifactsInput{Limit: 10}, listResult))
	require.Len(t, listResult.Artifacts, 2)
	require.Equal(t, published.ArtifactID, listResult.Artifacts[0].ArtifactID)
	require.Equal(t, shared.ArtifactID, listResult.Artifacts[1].ArtifactID)
}

func TestServiceToolMethodSavesGetsListsAndUpdatesReports(t *testing.T) {
	now := time.Date(2026, 6, 24, 14, 0, 0, 0, time.UTC)
	idCount := 0
	service := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Now:   func() time.Time { return now },
		NewID: func() string {
			idCount++
			return "report-" + string(rune('0'+idCount))
		},
	})
	ctx := authsvc.InjectUser(context.Background(), "owner-1")

	saveMethod, err := service.Method("save_report")
	require.NoError(t, err)
	saved := &SharedArtifact{}
	require.NoError(t, saveMethod(ctx, &SaveReportRequest{
		ReportID:        "forecastingQ3",
		Title:           "Forecasting Q3",
		Version:         1,
		DocumentVersion: 4,
		ReportDocument:  json.RawMessage(`{"kind":"reportDocument","id":"forecastingQ3","title":"Forecasting Q3"}`),
		ReportSpec:      json.RawMessage(validTestReportSpecJSON()),
		CompileState:    json.RawMessage(`{"status":"clean"}`),
		Metadata:        json.RawMessage(`{"workspaceId":"steward"}`),
	}, saved))
	require.Equal(t, savedReportArtifactKind, saved.Kind)
	require.Equal(t, "forecastingQ3", saved.ReportID)
	require.JSONEq(t, `{"status":"clean"}`, string(saved.CompileState))

	getMethod, err := service.Method("get_report")
	require.NoError(t, err)
	got := &SharedArtifact{}
	require.NoError(t, getMethod(ctx, &GetReportInput{ArtifactID: saved.ArtifactID}, got))
	require.Equal(t, saved.ArtifactID, got.ArtifactID)

	listMethod, err := service.Method("list_reports")
	require.NoError(t, err)
	listed := &ListReportsResult{}
	require.NoError(t, listMethod(ctx, &ListReportsInput{Limit: 10}, listed))
	require.Len(t, listed.Reports, 1)
	require.Equal(t, saved.ArtifactID, listed.Reports[0].ArtifactID)
	encodedList, err := json.Marshal(listed)
	require.NoError(t, err)
	require.NotContains(t, string(encodedList), "reportDocument")
	require.NotContains(t, string(encodedList), "compileState")

	updateMethod, err := service.Method("update_report")
	require.NoError(t, err)
	updated := &SharedArtifact{}
	require.NoError(t, updateMethod(ctx, &UpdateReportRequest{
		ArtifactID:   saved.ArtifactID,
		Title:        "Forecasting Q3 Updated",
		Version:      2,
		CompileState: json.RawMessage(`{"status":"stale"}`),
	}, updated))
	require.Equal(t, "Forecasting Q3 Updated", updated.Title)
	require.Equal(t, 2, updated.Version)
	require.JSONEq(t, `{"status":"stale"}`, string(updated.CompileState))
}

func TestServiceToolMethodSupportsManualLifecycleWhenNeeded(t *testing.T) {
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

	submitMethod, err := service.Method("submit_export")
	require.NoError(t, err)
	jobOut := &ExportJob{}
	require.NoError(t, submitMethod(ctx, &SubmitExportRequest{
		ReportExportRequest: validTestReportExportRequestEnvelope(),
	}, jobOut))
	require.Equal(t, JobStatusQueued, jobOut.Status)

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
}

func TestServiceToolMethodDispatchesQueuedLifecycle(t *testing.T) {
	now := time.Date(2026, 6, 13, 13, 30, 0, 0, time.UTC)
	exporter := &exportRecorder{
		result: &RenderResult{
			ContentType: "application/pdf",
			Data:        []byte("%PDF-queued"),
		},
	}
	idCounter := 0
	service := New(Options{
		Exporter: exporter,
		Store:    NewStoreAdapter(reportmemory.New()),
		Now:      func() time.Time { return now },
		NewID: func() string {
			idCounter++
			if idCounter <= 2 {
				return "job-" + string(rune('0'+idCounter))
			}
			return "artifact-" + string(rune('0'+idCounter-2))
		},
	})
	ctx := authsvc.InjectUser(context.Background(), "user-1")

	submitMethod, err := service.Method("submit_export")
	require.NoError(t, err)
	firstJob := &ExportJob{}
	secondJob := &ExportJob{}
	require.NoError(t, submitMethod(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/one",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	}, firstJob))
	require.NoError(t, submitMethod(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/two",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	}, secondJob))

	runQueuedMethod, err := service.Method("run_queued_exports")
	require.NoError(t, err)
	result := &RunQueuedExportsResult{}
	require.NoError(t, runQueuedMethod(context.Background(), &RunQueuedExportsInput{Limit: 1}, result))
	require.Equal(t, 1, result.ProcessedCount)
	require.Equal(t, 1, result.SucceededCount)
	require.Equal(t, 0, result.FailedCount)
	require.Len(t, result.Jobs, 1)
	require.Equal(t, firstJob.JobID, result.Jobs[0].JobID)
}

type failingStartStore struct {
	base      Store
	failJobID string
}

func (s *failingStartStore) CreateJob(ctx context.Context, job *ExportJob) error {
	return s.base.CreateJob(ctx, job)
}

func (s *failingStartStore) GetJob(ctx context.Context, jobID string) (*ExportJob, error) {
	return s.base.GetJob(ctx, jobID)
}

func (s *failingStartStore) ListJobs(ctx context.Context) ([]*ExportJob, error) {
	return s.base.ListJobs(ctx)
}

func (s *failingStartStore) UpdateJob(ctx context.Context, job *ExportJob) error {
	if s.failJobID != "" && job != nil && job.JobID == s.failJobID && job.Status == JobStatusRunning {
		return errors.New("store update failed")
	}
	return s.base.UpdateJob(ctx, job)
}

func (s *failingStartStore) PutArtifact(ctx context.Context, artifact *Artifact) error {
	return s.base.PutArtifact(ctx, artifact)
}

func (s *failingStartStore) GetArtifact(ctx context.Context, artifactID string) (*Artifact, error) {
	return s.base.GetArtifact(ctx, artifactID)
}

func (s *failingStartStore) ListArtifacts(ctx context.Context) ([]*Artifact, error) {
	return s.base.ListArtifacts(ctx)
}

func (s *failingStartStore) CreateSharedArtifact(ctx context.Context, artifact *SharedArtifact) error {
	return s.base.CreateSharedArtifact(ctx, artifact)
}

func (s *failingStartStore) GetSharedArtifact(ctx context.Context, artifactID string) (*SharedArtifact, error) {
	return s.base.GetSharedArtifact(ctx, artifactID)
}

func (s *failingStartStore) ListSharedArtifacts(ctx context.Context) ([]*SharedArtifact, error) {
	return s.base.ListSharedArtifacts(ctx)
}

func (s *failingStartStore) UpdateSharedArtifact(ctx context.Context, artifact *SharedArtifact) error {
	return s.base.UpdateSharedArtifact(ctx, artifact)
}

func (s *failingStartStore) DeleteSharedArtifact(ctx context.Context, artifactID string) error {
	return s.base.DeleteSharedArtifact(ctx, artifactID)
}

func TestServiceToolMethodRunQueuedExportsPreservesPartialResultOnError(t *testing.T) {
	now := time.Date(2026, 6, 13, 13, 35, 0, 0, time.UTC)
	exporter := &exportRecorder{
		result: &RenderResult{
			ContentType: "application/pdf",
			Data:        []byte("%PDF-queued"),
		},
	}
	idCounter := 0
	baseStore := NewMemoryStore()
	service := New(Options{
		Exporter: exporter,
		Store: &failingStartStore{
			base:      baseStore,
			failJobID: "job-2",
		},
		Now: func() time.Time { return now },
		NewID: func() string {
			idCounter++
			switch idCounter {
			case 1:
				return "job-1"
			case 2:
				return "job-2"
			case 3:
				return "artifact-1"
			default:
				return "artifact-extra"
			}
		},
	})
	ctx := authsvc.InjectUser(context.Background(), "user-1")

	submitMethod, err := service.Method("submit_export")
	require.NoError(t, err)
	firstJob := &ExportJob{}
	secondJob := &ExportJob{}
	require.NoError(t, submitMethod(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/one",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	}, firstJob))
	require.NoError(t, submitMethod(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/two",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	}, secondJob))

	runQueuedMethod, err := service.Method("run_queued_exports")
	require.NoError(t, err)
	result := &RunQueuedExportsResult{}
	err = runQueuedMethod(context.Background(), &RunQueuedExportsInput{Limit: 2}, result)
	require.EqualError(t, err, "store update failed")
	require.Equal(t, 1, result.ProcessedCount)
	require.Equal(t, 1, result.SucceededCount)
	require.Equal(t, 0, result.FailedCount)
	require.Len(t, result.Jobs, 1)
	require.Equal(t, firstJob.JobID, result.Jobs[0].JobID)
	require.Equal(t, JobStatusSucceeded, result.Jobs[0].Status)
}

func TestServiceToolMethodRunQueuedExportsPreservesMixedBatchResults(t *testing.T) {
	now := time.Date(2026, 6, 13, 13, 40, 0, 0, time.UTC)
	exporter := &queuedExportRecorder{}
	idCounter := 0
	baseStore := NewMemoryStore()
	service := New(Options{
		Exporter: exporter,
		Store: &claimRaceStore{
			base:       baseStore,
			claimJobID: "job-1",
			claimTime:  now.Add(5 * time.Second),
		},
		Now: func() time.Time {
			return now.Add(time.Duration(idCounter) * time.Minute)
		},
		NewID: func() string {
			idCounter++
			switch idCounter {
			case 1:
				return "job-1"
			case 2:
				return "job-2"
			case 3:
				return "job-3"
			case 4:
				return "artifact-1"
			case 5:
				return "artifact-2"
			default:
				return "artifact-extra"
			}
		},
	})
	ctx := authsvc.InjectUser(context.Background(), "user-1")

	require.NoError(t, baseStore.PutArtifact(context.Background(), &Artifact{
		ArtifactID:  "artifact-1",
		JobID:       "other-job",
		ArtifactRef: "report://draft/existing",
		OwnerID:     "user-1",
		Format:      ExportFormatPDF,
		ContentType: "application/pdf",
		Data:        []byte("%PDF-existing"),
		CreatedAt:   now,
	}))

	submitMethod, err := service.Method("submit_export")
	require.NoError(t, err)
	firstJob := &ExportJob{}
	secondJob := &ExportJob{}
	thirdJob := &ExportJob{}
	require.NoError(t, submitMethod(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/claimed",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	}, firstJob))
	require.NoError(t, submitMethod(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/collision",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	}, secondJob))
	require.NoError(t, submitMethod(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/success",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	}, thirdJob))

	runQueuedMethod, err := service.Method("run_queued_exports")
	require.NoError(t, err)
	result := &RunQueuedExportsResult{}
	require.NoError(t, runQueuedMethod(context.Background(), &RunQueuedExportsInput{Limit: 2}, result))
	require.Equal(t, 2, result.ProcessedCount)
	require.Equal(t, 1, result.SucceededCount)
	require.Equal(t, 1, result.FailedCount)
	require.Len(t, result.Jobs, 2)
	require.Equal(t, secondJob.JobID, result.Jobs[0].JobID)
	require.Equal(t, JobStatusFailed, result.Jobs[0].Status)
	require.Contains(t, result.Jobs[0].Error, "artifact artifact-1 already exists")
	require.Equal(t, thirdJob.JobID, result.Jobs[1].JobID)
	require.Equal(t, JobStatusSucceeded, result.Jobs[1].Status)
}

func TestServiceToolMethodRunExportPreservesFailedJobOnArtifactCollision(t *testing.T) {
	now := time.Date(2026, 6, 13, 13, 45, 0, 0, time.UTC)
	exporter := &exportRecorder{
		result: &RenderResult{
			ContentType: "application/pdf",
			Data:        []byte("%PDF-collision"),
		},
	}
	store := NewMemoryStore()
	idCounter := 0
	service := New(Options{
		Exporter: exporter,
		Store:    store,
		Now:      func() time.Time { return now },
		NewID: func() string {
			idCounter++
			if idCounter == 1 {
				return "job-1"
			}
			return "artifact-1"
		},
	})
	ctx := authsvc.InjectUser(context.Background(), "user-1")

	require.NoError(t, store.PutArtifact(context.Background(), &Artifact{
		ArtifactID:  "artifact-1",
		JobID:       "other-job",
		ArtifactRef: "report://draft/existing",
		OwnerID:     "user-1",
		Format:      ExportFormatPDF,
		ContentType: "application/pdf",
		Data:        []byte("%PDF-existing"),
		CreatedAt:   now,
	}))

	submitMethod, err := service.Method("submit_export")
	require.NoError(t, err)
	jobOut := &ExportJob{}
	require.NoError(t, submitMethod(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/collision",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	}, jobOut))

	runMethod, err := service.Method("run_export")
	require.NoError(t, err)
	err = runMethod(context.Background(), &RunExportInput{JobID: jobOut.JobID}, jobOut)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAlreadyExists)
	require.Equal(t, JobStatusFailed, jobOut.Status)
	require.Contains(t, jobOut.Error, "artifact artifact-1 already exists")
}

func TestServiceToolMethodCompleteExportPreservesFailedJobOnArtifactCollision(t *testing.T) {
	now := time.Date(2026, 6, 13, 13, 50, 0, 0, time.UTC)
	store := NewMemoryStore()
	idCounter := 0
	service := New(Options{
		Store: store,
		Now:   func() time.Time { return now },
		NewID: func() string {
			idCounter++
			if idCounter == 1 {
				return "job-1"
			}
			return "artifact-1"
		},
	})
	ctx := authsvc.InjectUser(context.Background(), "user-1")

	require.NoError(t, store.PutArtifact(context.Background(), &Artifact{
		ArtifactID:  "artifact-1",
		JobID:       "other-job",
		ArtifactRef: "report://draft/existing",
		OwnerID:     "user-1",
		Format:      ExportFormatPDF,
		ContentType: "application/pdf",
		Data:        []byte("%PDF-existing"),
		CreatedAt:   now,
	}))

	submitMethod, err := service.Method("submit_export")
	require.NoError(t, err)
	jobOut := &ExportJob{}
	require.NoError(t, submitMethod(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/collision",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	}, jobOut))

	startMethod, err := service.Method("start_export")
	require.NoError(t, err)
	require.NoError(t, startMethod(context.Background(), &StartExportInput{JobID: jobOut.JobID}, jobOut))
	require.Equal(t, JobStatusRunning, jobOut.Status)

	completeMethod, err := service.Method("complete_export")
	require.NoError(t, err)
	err = completeMethod(context.Background(), &CompleteExportRequest{
		JobID:       jobOut.JobID,
		ContentType: "application/pdf",
		Data:        []byte("%PDF-collision"),
	}, jobOut)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAlreadyExists)
	require.Equal(t, JobStatusFailed, jobOut.Status)
	require.Contains(t, jobOut.Error, "artifact artifact-1 already exists")
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
