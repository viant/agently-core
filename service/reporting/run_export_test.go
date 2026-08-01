package reporting

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	exportrequest "github.com/viant/agently-core/pkg/agently/exportrequest"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	authsvc "github.com/viant/agently-core/service/auth"
)

func TestRunReferenceExportAuthoritativeCopyAuthorizationAndIdempotency(t *testing.T) {
	client := reportmemory.New()
	runClient := client.(reportstore.RunClient)
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	service := New(Options{
		Store:                NewStoreAdapter(client),
		ExportFromRunEnabled: true,
		Now:                  func() time.Time { return now },
		NewID:                sequentialRunExportID("job"),
	})
	ownerOneConversationOne := runExportContext("owner-1", "conversation-1", "request-1")
	runOne := completedRun("run-1", "owner-1", "conversation-1", 7, now)
	runTwo := completedRun("run-2", "owner-1", "conversation-1", 11, now)
	require.NoError(t, runClient.CreateReportRun(ownerOneConversationOne, runOne))
	require.NoError(t, runClient.CreateReportRun(ownerOneConversationOne, runTwo))

	first, err := service.SubmitExport(ownerOneConversationOne, &SubmitExportRequest{
		ReportRunID: runOne.ReportRunID,
		Format:      ExportFormatPDF,
	})
	require.NoError(t, err)
	require.Equal(t, runOne.ReportRunID, first.ReportRunID)
	require.Equal(t, runOne.Revision, first.ReportRunRevision)
	require.Equal(t, "conversation-1", first.ConversationID)
	require.Empty(t, first.WorkspaceID)
	require.JSONEq(t, string(runOne.ReportSpec), string(first.ReportSpec))
	require.JSONEq(t, string(runOne.ReportFill), string(first.ReportFill))
	require.JSONEq(t, string(runOne.ReportPrint), string(first.ReportPrint))

	replay, err := service.SubmitExport(ownerOneConversationOne, &SubmitExportRequest{
		ReportRunID: runOne.ReportRunID,
		Format:      ExportFormatPDF,
	})
	require.NoError(t, err)
	require.Equal(t, first.JobID, replay.JobID)

	_, err = service.SubmitExport(ownerOneConversationOne, &SubmitExportRequest{
		ReportRunID: runTwo.ReportRunID,
		Format:      ExportFormatPDF,
	})
	require.ErrorIs(t, err, ErrConflict)

	second, err := service.SubmitExport(
		runExportContext("owner-1", "conversation-1", "request-2"),
		&SubmitExportRequest{ReportRunID: runOne.ReportRunID, Format: ExportFormatPDF},
	)
	require.NoError(t, err)
	require.NotEqual(t, first.JobID, second.JobID)

	conversationTwoCtx := runExportContext("owner-1", "conversation-2", "request-1")
	runThree := completedRun("run-3", "owner-1", "conversation-2", 5, now)
	require.NoError(t, runClient.CreateReportRun(conversationTwoCtx, runThree))
	scoped, err := service.SubmitExport(conversationTwoCtx, &SubmitExportRequest{
		ReportRunID: runThree.ReportRunID,
		Format:      ExportFormatPDF,
	})
	require.NoError(t, err)
	require.NotEqual(t, first.JobID, scoped.JobID)

	_, err = service.GetExportStatus(runExportContext("owner-1", "conversation-2", "status"), first.JobID)
	require.ErrorIs(t, err, ErrNotFound)
	visible, err := service.GetExportStatus(runExportContext("owner-1", "conversation-1", "status"), first.JobID)
	require.NoError(t, err)
	require.Equal(t, first.JobID, visible.JobID)

	_, err = service.SubmitExport(
		runExportContext("owner-2", "conversation-1", "request-owner-2"),
		&SubmitExportRequest{ReportRunID: runOne.ReportRunID, Format: ExportFormatPDF},
	)
	require.Error(t, err)
}

func TestRunReferenceExportRejectsUntrustedAndMixedInputs(t *testing.T) {
	client := reportmemory.New()
	runClient := client.(reportstore.RunClient)
	now := time.Date(2026, 7, 29, 12, 30, 0, 0, time.UTC)
	service := New(Options{
		Store:                NewStoreAdapter(client),
		ExportFromRunEnabled: true,
		Now:                  func() time.Time { return now },
		NewID:                sequentialRunExportID("reject-job"),
	})
	ctx := runExportContext("owner-1", "conversation-1", "request-reject")
	require.NoError(t, runClient.CreateReportRun(ctx, completedRun("run-complete", "owner-1", "conversation-1", 3, now)))

	rejected := []struct {
		name    string
		mutate  func(*SubmitExportRequest)
		message string
	}{
		{name: "conversation", mutate: func(input *SubmitExportRequest) { input.ConversationID = "conversation-1" }, message: "does not accept"},
		{name: "workspace", mutate: func(input *SubmitExportRequest) { input.WorkspaceID = "workspace-1" }, message: "does not accept"},
		{name: "artifact", mutate: func(input *SubmitExportRequest) { input.ArtifactRef = "browser://payload" }, message: "does not accept"},
		{name: "scope", mutate: func(input *SubmitExportRequest) { input.Scope = ExportScopeDraft }, message: "does not accept"},
		{name: "metadata", mutate: func(input *SubmitExportRequest) { input.Metadata = json.RawMessage(`{"browser":true}`) }, message: "does not accept"},
		{name: "spec", mutate: func(input *SubmitExportRequest) { input.ReportSpec = json.RawMessage(validTestReportSpecJSON()) }, message: "does not accept"},
		{name: "fill", mutate: func(input *SubmitExportRequest) { input.ReportFill = json.RawMessage(validTestReportFillJSON()) }, message: "does not accept"},
		{name: "print", mutate: func(input *SubmitExportRequest) { input.ReportPrint = json.RawMessage(validTestReportPrintJSON()) }, message: "does not accept"},
		{name: "source", mutate: func(input *SubmitExportRequest) { input.Source = &ExportSource{Kind: "inline"} }, message: "does not accept"},
		{name: "request", mutate: func(input *SubmitExportRequest) { input.ReportExportRequest = &ReportExportRequest{} }, message: "does not accept"},
	}
	for index, testCase := range rejected {
		t.Run(testCase.name, func(t *testing.T) {
			input := &SubmitExportRequest{ReportRunID: "run-complete", Format: ExportFormatPDF}
			testCase.mutate(input)
			_, err := service.SubmitExport(
				runExportContext("owner-1", "conversation-1", fmt.Sprintf("reject-%d", index)),
				input,
			)
			require.ErrorContains(t, err, testCase.message)
		})
	}

	alternateJSONFields := []struct {
		name  string
		value string
	}{
		{name: "ConversationID", value: `"conversation-1"`},
		{name: "WorkspaceID", value: `"workspace-1"`},
		{name: "scope", value: `"draft"`},
		{name: "artifact", value: `"browser://payload"`},
		{name: "source", value: `{"kind":"inline"}`},
		{name: "spec", value: `{"kind":"reportSpec"}`},
		{name: "fill", value: `{"kind":"reportFill"}`},
		{name: "print", value: `{"kind":"reportPrint"}`},
		{name: "metadata", value: `{"browser":true}`},
		{name: "request", value: `{"target":{"format":"pdf"}}`},
		{name: "reportRunRevision", value: `3`},
		{name: "exportRequestId", value: `"model-controlled"`},
	}
	for index, alternate := range alternateJSONFields {
		t.Run("json_"+alternate.name, func(t *testing.T) {
			var input SubmitExportRequest
			require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(
				`{"reportRunId":"run-complete","format":"pdf",%q:%s}`,
				alternate.name,
				alternate.value,
			)), &input))
			_, err := service.SubmitExport(
				runExportContext("owner-1", "conversation-1", fmt.Sprintf("json-reject-%d", index)),
				&input,
			)
			require.ErrorContains(t, err, "does not accept")
		})
	}

	_, err := service.SubmitExport(ctx, &SubmitExportRequest{ReportRunID: "run-complete", Format: ExportFormatXLSX})
	require.ErrorContains(t, err, "pdf only")
	_, err = service.SubmitExport(
		exportrequest.WithID(authsvc.InjectUser(context.Background(), "owner-1"), "missing-conversation"),
		&SubmitExportRequest{ReportRunID: "run-complete", Format: ExportFormatPDF},
	)
	require.ErrorContains(t, err, "trusted current conversation is required")
	_, err = service.SubmitExport(
		runtimerequestctx.WithConversationID(authsvc.InjectUser(context.Background(), "owner-1"), "conversation-1"),
		&SubmitExportRequest{ReportRunID: "run-complete", Format: ExportFormatPDF},
	)
	require.ErrorContains(t, err, "trusted export request identity is required")
	_, err = service.SubmitExport(
		runExportContext("owner-1", "conversation-1", strings.Repeat("x", 129)),
		&SubmitExportRequest{ReportRunID: "run-complete", Format: ExportFormatPDF},
	)
	require.ErrorIs(t, err, ErrConflict)

	unbound := completedRun("run-unbound", "owner-1", "", 2, now)
	require.NoError(t, runClient.CreateReportRun(authsvc.InjectUser(context.Background(), "owner-1"), unbound))
	_, err = service.SubmitExport(
		runExportContext("owner-1", "conversation-1", "unbound"),
		&SubmitExportRequest{ReportRunID: unbound.ReportRunID, Format: ExportFormatPDF},
	)
	require.Error(t, err)

	running := completedRun("run-running", "owner-1", "conversation-1", 2, now)
	running.Status = reportrun.StatusRunning
	running.CompletedAt = nil
	require.NoError(t, runClient.CreateReportRun(ctx, running))
	_, err = service.SubmitExport(
		runExportContext("owner-1", "conversation-1", "running"),
		&SubmitExportRequest{ReportRunID: running.ReportRunID, Format: ExportFormatPDF},
	)
	require.ErrorIs(t, err, reportstore.ErrInvalidTransition)

	missingSnapshot := completedRun("run-missing-snapshot", "owner-1", "conversation-1", 2, now)
	missingSnapshot.ReportPrint = nil
	require.NoError(t, runClient.CreateReportRun(ctx, missingSnapshot))
	_, err = service.SubmitExport(
		runExportContext("owner-1", "conversation-1", "missing-snapshot"),
		&SubmitExportRequest{ReportRunID: missingSnapshot.ReportRunID, Format: ExportFormatPDF},
	)
	require.ErrorIs(t, err, reportstore.ErrInvalidTransition)

	zeroRevision := completedRun("run-zero-revision", "owner-1", "conversation-1", 0, now)
	require.NoError(t, runClient.CreateReportRun(ctx, zeroRevision))
	_, err = service.SubmitExport(
		runExportContext("owner-1", "conversation-1", "zero-revision"),
		&SubmitExportRequest{ReportRunID: zeroRevision.ReportRunID, Format: ExportFormatPDF},
	)
	require.ErrorIs(t, err, reportstore.ErrInvalidTransition)
}

func TestRunReferenceExportLifecycleRecoveryAndLegacyCompatibility(t *testing.T) {
	client := reportmemory.New()
	runClient := client.(reportstore.RunClient)
	now := time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)
	exporter := &exportRecorder{result: &RenderResult{
		ContentType: "application/pdf",
		Data:        []byte("%PDF-run-reference"),
	}}
	service := New(Options{
		Store:                NewStoreAdapter(client),
		Exporter:             exporter,
		ExportFromRunEnabled: true,
		Now:                  func() time.Time { return now },
		NewID:                sequentialRunExportID("lifecycle"),
	})
	ctx := runExportContext("owner-1", "conversation-1", "lifecycle-request")
	run := completedRun("run-lifecycle", "owner-1", "conversation-1", 9, now)
	require.NoError(t, runClient.CreateReportRun(ctx, run))
	job, err := service.SubmitExport(ctx, &SubmitExportRequest{ReportRunID: run.ReportRunID, Format: ExportFormatPDF})
	require.NoError(t, err)
	started, err := service.StartExport(ctx, job.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusRunning, started.Status)
	completed, err := service.CompleteExport(ctx, &CompleteExportRequest{
		JobID:       job.JobID,
		ContentType: "application/pdf",
		Data:        []byte("%PDF-run-reference"),
	})
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, completed.Status)
	require.NotEmpty(t, completed.ArtifactID)
	duplicate, err := service.CompleteExport(ctx, &CompleteExportRequest{
		JobID:       job.JobID,
		ContentType: "application/pdf",
		Data:        []byte("%PDF-different-retry-body"),
	})
	require.NoError(t, err)
	require.Equal(t, completed.ArtifactID, duplicate.ArtifactID)
	artifacts, err := client.ListArtifacts(ctx)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)

	recoveryCtx := runExportContext("owner-1", "conversation-1", "queued-recovery")
	queued, err := service.SubmitExport(recoveryCtx, &SubmitExportRequest{ReportRunID: run.ReportRunID, Format: ExportFormatPDF})
	require.NoError(t, err)
	restarted := New(Options{
		Store:                NewStoreAdapter(client),
		Exporter:             exporter,
		ExportFromRunEnabled: true,
		Now:                  func() time.Time { return now },
		NewID:                sequentialRunExportID("restarted"),
	})
	result, err := restarted.RunQueuedExports(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, result.SucceededCount)
	require.NotNil(t, exporter.request)
	require.Equal(t, run.ReportRunID, exporter.request.ReportRunID)
	require.Equal(t, run.Revision, exporter.request.ReportRunRevision)
	require.JSONEq(t, string(run.ReportPrint), string(exporter.request.ReportPrint))
	recovered, err := restarted.GetExportStatus(ctx, queued.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, recovered.Status)
	require.NotEqual(t, completed.ArtifactID, recovered.ArtifactID)
	artifacts, err = client.ListArtifacts(ctx)
	require.NoError(t, err)
	require.Len(t, artifacts, 2)

	staleCtx := runExportContext("owner-1", "conversation-1", "stale-running")
	stale, err := service.SubmitExport(staleCtx, &SubmitExportRequest{ReportRunID: run.ReportRunID, Format: ExportFormatPDF})
	require.NoError(t, err)
	_, err = service.StartExport(staleCtx, stale.JobID)
	require.NoError(t, err)
	now = now.Add(20 * time.Minute)
	reconciled, err := service.ReconcileStaleRunningExports(context.Background(), 15*time.Minute)
	require.NoError(t, err)
	require.Len(t, reconciled, 1)
	require.Equal(t, JobStatusFailed, reconciled[0].Status)
	retry, err := service.SubmitExport(
		runExportContext("owner-1", "conversation-1", "stale-running-retry"),
		&SubmitExportRequest{ReportRunID: run.ReportRunID, Format: ExportFormatPDF},
	)
	require.NoError(t, err)
	require.NotEqual(t, stale.JobID, retry.JobID)
	failedAgain, err := service.GetExportStatus(ctx, stale.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusFailed, failedAgain.Status)

	flagOff := New(Options{Store: NewStoreAdapter(reportmemory.New()), NewID: sequentialRunExportID("legacy")})
	legacyCtx := authsvc.InjectUser(context.Background(), "legacy-owner")
	legacy, err := flagOff.SubmitExport(legacyCtx, &SubmitExportRequest{
		ArtifactRef: "legacy://artifact",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportSpec:  json.RawMessage(validTestReportSpecJSON()),
		ReportFill:  json.RawMessage(validTestReportFillJSON()),
		ReportPrint: json.RawMessage(validTestReportPrintJSON()),
	})
	require.NoError(t, err)
	require.Empty(t, legacy.ReportRunID)
	require.Empty(t, legacy.ExportRequestID)
	_, err = flagOff.SubmitExport(legacyCtx, &SubmitExportRequest{ReportRunID: "run-disabled", Format: ExportFormatPDF})
	require.ErrorContains(t, err, "disabled")
}

func TestRunReferenceExportArtifactFailureAndOrphanRecovery(t *testing.T) {
	now := time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC)
	t.Run("artifact persistence failure never reports success", func(t *testing.T) {
		client := reportmemory.New()
		runClient := client.(reportstore.RunClient)
		ids := []string{"job-collision", "artifact-collision"}
		index := 0
		service := New(Options{
			Store:                NewStoreAdapter(client),
			ExportFromRunEnabled: true,
			Now:                  func() time.Time { return now },
			NewID: func() string {
				id := ids[index]
				index++
				return id
			},
		})
		ctx := runExportContext("owner-1", "conversation-1", "artifact-failure")
		run := completedRun("run-artifact-failure", "owner-1", "conversation-1", 2, now)
		require.NoError(t, runClient.CreateReportRun(ctx, run))
		job, err := service.SubmitExport(ctx, &SubmitExportRequest{ReportRunID: run.ReportRunID, Format: ExportFormatPDF})
		require.NoError(t, err)
		_, err = service.StartExport(ctx, job.JobID)
		require.NoError(t, err)
		require.NoError(t, client.CreateJob(ctx, &reportjob.Record{
			JobID:       "other-job",
			ArtifactRef: "legacy://other",
			OwnerID:     "owner-1",
			Format:      "pdf",
			Scope:       "draft",
			Status:      "succeeded",
			SubmittedAt: now,
		}))
		require.NoError(t, client.PutArtifact(ctx, &reportartifact.Record{
			ArtifactID:  "artifact-collision",
			JobID:       "other-job",
			ArtifactRef: "legacy://other",
			OwnerID:     "owner-1",
			Format:      "pdf",
		}))
		_, err = service.CompleteExport(ctx, &CompleteExportRequest{
			JobID:       job.JobID,
			ContentType: "application/pdf",
			Data:        []byte("%PDF-collision"),
		})
		require.ErrorIs(t, err, ErrAlreadyExists)
		persisted, getErr := service.GetExportStatus(ctx, job.JobID)
		require.NoError(t, getErr)
		require.Equal(t, JobStatusRunning, persisted.Status)
		require.Empty(t, persisted.ArtifactID)
	})

	t.Run("orphan artifact completes stale running job exactly once", func(t *testing.T) {
		client := reportmemory.New()
		runClient := client.(reportstore.RunClient)
		service := New(Options{
			Store:                NewStoreAdapter(client),
			ExportFromRunEnabled: true,
			Now:                  func() time.Time { return now },
			NewID:                sequentialRunExportID("orphan"),
		})
		ctx := runExportContext("owner-1", "conversation-1", "orphan-recovery")
		run := completedRun("run-orphan", "owner-1", "conversation-1", 4, now)
		require.NoError(t, runClient.CreateReportRun(ctx, run))
		job, err := service.SubmitExport(ctx, &SubmitExportRequest{ReportRunID: run.ReportRunID, Format: ExportFormatPDF})
		require.NoError(t, err)
		running, err := service.StartExport(ctx, job.JobID)
		require.NoError(t, err)
		require.NoError(t, client.PutArtifact(ctx, &reportartifact.Record{
			ArtifactID:  "persisted-orphan",
			JobID:       running.JobID,
			ArtifactRef: running.ArtifactRef,
			OwnerID:     running.OwnerID,
			Format:      string(running.Format),
			ContentType: "application/pdf",
			Data:        []byte("%PDF-orphan"),
			CreatedAt:   now,
		}))
		now = now.Add(20 * time.Minute)
		reconciled, err := service.ReconcileStaleRunningExports(context.Background(), 15*time.Minute)
		require.NoError(t, err)
		require.Len(t, reconciled, 1)
		require.Equal(t, JobStatusSucceeded, reconciled[0].Status)
		require.Equal(t, "persisted-orphan", reconciled[0].ArtifactID)
		artifacts, err := client.ListArtifacts(ctx)
		require.NoError(t, err)
		require.Len(t, artifacts, 1)
	})
}

func TestRunReferenceSubmitSchemaHidesTrustedIdentityAndRevision(t *testing.T) {
	inputType := reflect.TypeOf(SubmitExportRequest{})
	_, hasRevision := inputType.FieldByName("ReportRunRevision")
	require.False(t, hasRevision)
	_, hasRequestID := inputType.FieldByName("ExportRequestID")
	require.False(t, hasRequestID)

	payload, err := json.Marshal(&SubmitExportRequest{ReportRunID: "run-1", Format: ExportFormatPDF})
	require.NoError(t, err)
	require.JSONEq(t, `{"format":"pdf","reportRunId":"run-1"}`, string(payload))

	jobPayload, err := json.Marshal(&ExportJob{
		ReportRunID:       "run-1",
		ReportRunRevision: 7,
		ExportRequestID:   "trusted-secret",
	})
	require.NoError(t, err)
	require.NotContains(t, string(jobPayload), "trusted-secret")
	require.NotContains(t, string(jobPayload), "exportRequestId")
	require.NotContains(t, string(jobPayload), "reportRunRevision")

	jobRecordType := reflect.TypeOf(reportjob.Record{})
	_, hasJobRevision := jobRecordType.FieldByName("Revision")
	require.False(t, hasJobRevision, "report_export_job must not grow a fourth revision column")
}

func TestRunReferenceExportAddsNoEmailOrPublicToolSurface(t *testing.T) {
	methods := New(Options{Store: NewStoreAdapter(reportmemory.New())}).Methods()
	submitCount := 0
	for _, method := range methods {
		name := strings.ToLower(strings.TrimSpace(method.Name))
		require.NotContains(t, name, "email")
		require.NotContains(t, name, "send")
		if name == "submit_export" {
			submitCount++
		}
	}
	require.Equal(t, 1, submitCount)
}

func completedRun(id, ownerID, conversationID string, revision int64, now time.Time) *reportrun.Record {
	completedAt := now.UTC()
	return &reportrun.Record{
		ReportRunID:    id,
		OwnerID:        ownerID,
		ConversationID: conversationID,
		Materializer:   reportrun.MaterializerLegacyBrowser,
		Origin:         "manual",
		Status:         reportrun.StatusCompleted,
		StartedAt:      now.Add(-time.Minute).UTC(),
		CompletedAt:    &completedAt,
		Revision:       revision,
		UIRunRequestID: "ui-" + id,
		ReportSpec:     json.RawMessage(validTestReportSpecJSON()),
		ReportFill:     json.RawMessage(validTestReportFillJSON()),
		ReportPrint:    json.RawMessage(validTestReportPrintJSON()),
		CreatedAt:      now.Add(-time.Minute).UTC(),
		UpdatedAt:      now.UTC(),
	}
}

func runExportContext(ownerID, conversationID, requestID string) context.Context {
	ctx := authsvc.InjectUser(context.Background(), ownerID)
	ctx = runtimerequestctx.WithConversationID(ctx, conversationID)
	return exportrequest.WithID(ctx, requestID)
}

func sequentialRunExportID(prefix string) func() string {
	index := 0
	return func() string {
		index++
		return fmt.Sprintf("%s-%d", prefix, index)
	}
}
