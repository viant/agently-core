package reportingrun

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	reportcontext "github.com/viant/agently-core/pkg/agently/reportcontext"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
	authsvc "github.com/viant/agently-core/service/auth"
)

var (
	testSpec  = []byte(`{"kind":"reportSpec","version":1}`)
	testFill  = []byte(`{"kind":"reportFill","datasets":[]}`)
	testPrint = []byte(`{"kind":"reportPrint","pages":[]}`)
)

func newTestService(t *testing.T) (*Service, reportstore.RunClient) {
	t.Helper()
	store, ok := reportmemory.New().(reportstore.RunClient)
	if !ok {
		t.Fatal("memory reporting store does not implement RunClient")
	}
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	id := 0
	service := New(Options{
		Store: store,
		Now: func() time.Time {
			now = now.Add(time.Second)
			return now
		},
		NewID: func() string {
			id++
			return fmt.Sprintf("run-%d", id)
		},
	})
	return service, store
}

func begin(t *testing.T, service *Service, ctx context.Context, requestID, conversationID, origin string) *BeginResult {
	t.Helper()
	result, err := service.Begin(ctx, &BeginInput{
		UIRunRequestID:  requestID,
		ConversationID:  conversationID,
		Origin:          origin,
		BuilderRef:      "metricReportBuilder",
		PresetID:        "performance_inventory_brief",
		SourceKind:      "preset",
		SourceID:        "performance_inventory_brief",
		RequestedParams: []byte(`{"orderId":2676946}`),
		EffectiveParams: []byte(`{"orderId":2676946}`),
	})
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	return result
}

func complete(t *testing.T, service *Service, ctx context.Context, runID, conversationID string, revision int64) {
	t.Helper()
	if _, err := service.Complete(ctx, &CompleteInput{
		ReportRunID:      runID,
		ConversationID:   conversationID,
		ExpectedRevision: revision,
		ReportSpec:       testSpec,
		ReportFill:       testFill,
		ReportPrint:      testPrint,
	}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
}

func TestService_RunLifecycleAndIdempotency(t *testing.T) {
	service, _ := newTestService(t)
	owner := authsvc.InjectUser(context.Background(), "owner-1")

	first := begin(t, service, owner, "ui-request-1", "conv-1", "prompt")
	if first.Run.ReportRunID != "run-1" || first.Run.Revision != 1 || first.Run.Status != "running" {
		t.Fatalf("Begin() = %+v", first.Run)
	}
	duplicate := begin(t, service, owner, "ui-request-1", "conv-1", "prompt")
	if duplicate.Run.ReportRunID != first.Run.ReportRunID {
		t.Fatalf("duplicate Begin() ID = %q, want %q", duplicate.Run.ReportRunID, first.Run.ReportRunID)
	}
	if _, err := service.Begin(owner, &BeginInput{
		UIRunRequestID: "ui-request-1",
		ConversationID: "conv-1",
		Origin:         "prompt",
		BuilderRef:     "different",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting duplicate Begin() error = %v, want conflict", err)
	}

	completed, err := service.Complete(owner, &CompleteInput{
		ReportRunID:      first.Run.ReportRunID,
		ConversationID:   "conv-1",
		ExpectedRevision: 1,
		ReportSpec:       testSpec,
		ReportFill:       testFill,
		ReportPrint:      testPrint,
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if completed.Status != "completed" || completed.Revision != 2 || completed.CompletedAt == nil {
		t.Fatalf("Complete() = %+v", completed)
	}
	duplicateAfterComplete := begin(t, service, owner, "ui-request-1", "conv-1", "prompt")
	if duplicateAfterComplete.Run.ReportRunID != first.Run.ReportRunID ||
		duplicateAfterComplete.Run.Status != "completed" {
		t.Fatalf("duplicate Begin(after complete) = %+v", duplicateAfterComplete.Run)
	}
	identical, err := service.Complete(owner, &CompleteInput{
		ReportRunID:      first.Run.ReportRunID,
		ConversationID:   "conv-1",
		ExpectedRevision: 1,
		ReportSpec:       []byte("{ \"kind\": \"reportSpec\", \"version\": 1 }"),
		ReportFill:       testFill,
		ReportPrint:      testPrint,
	})
	if err != nil || identical.Revision != 2 {
		t.Fatalf("identical duplicate Complete() = %+v, %v", identical, err)
	}
	if _, err := service.Complete(owner, &CompleteInput{
		ReportRunID:      first.Run.ReportRunID,
		ConversationID:   "conv-1",
		ExpectedRevision: 2,
		ReportSpec:       []byte(`{"kind":"reportSpec","changed":true}`),
		ReportFill:       testFill,
		ReportPrint:      testPrint,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("mutating completed snapshot error = %v, want conflict", err)
	}
	if _, err := service.Fail(owner, &FailInput{
		ReportRunID:      first.Run.ReportRunID,
		ConversationID:   "conv-1",
		ExpectedRevision: 2,
		FailureCode:      "late",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Fail(completed) error = %v, want conflict", err)
	}
}

func TestService_FailureMissingSnapshotsAndCAS(t *testing.T) {
	service, _ := newTestService(t)
	owner := authsvc.InjectUser(context.Background(), "owner-1")

	missing := begin(t, service, owner, "missing-snapshot", "conv-1", "prompt")
	if _, err := service.Complete(owner, &CompleteInput{
		ReportRunID:      missing.Run.ReportRunID,
		ConversationID:   "conv-1",
		ExpectedRevision: 1,
		ReportSpec:       testSpec,
		ReportFill:       testFill,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Complete(missing print) error = %v, want invalid", err)
	}
	if _, err := service.Complete(owner, &CompleteInput{
		ReportRunID:      missing.Run.ReportRunID,
		ConversationID:   "conv-1",
		ExpectedRevision: 9,
		ReportSpec:       testSpec,
		ReportFill:       testFill,
		ReportPrint:      testPrint,
	}); !errors.Is(err, ErrCAS) {
		t.Fatalf("Complete(stale) error = %v, want CAS", err)
	}

	failed := begin(t, service, owner, "failure", "conv-1", "prompt")
	result, err := service.Fail(owner, &FailInput{
		ReportRunID:      failed.Run.ReportRunID,
		ConversationID:   "conv-1",
		ExpectedRevision: 1,
		FailureCode:      "fetch_failed",
		FailureText:      "datasource unavailable",
	})
	if err != nil || result.Status != "failed" || result.Revision != 2 {
		t.Fatalf("Fail() = %+v, %v", result, err)
	}
	if _, err := service.Complete(owner, &CompleteInput{
		ReportRunID:      failed.Run.ReportRunID,
		ConversationID:   "conv-1",
		ExpectedRevision: 2,
		ReportSpec:       testSpec,
		ReportFill:       testFill,
		ReportPrint:      testPrint,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Complete(failed) error = %v, want conflict", err)
	}
}

func TestService_OwnerConversationActivationAndAdoption(t *testing.T) {
	service, _ := newTestService(t)
	owner := authsvc.InjectUser(context.Background(), "owner-1")
	otherOwner := authsvc.InjectUser(context.Background(), "owner-2")

	bound := begin(t, service, owner, "bound", "conv-1", "prompt")
	complete(t, service, owner, bound.Run.ReportRunID, "conv-1", 1)
	if _, err := service.GetRun(otherOwner, bound.Run.ReportRunID, "conv-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner GetRun() error = %v, want not found", err)
	}
	if _, err := service.GetRun(owner, bound.Run.ReportRunID, "conv-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-conversation GetRun() error = %v, want not found", err)
	}
	active, err := service.Activate(owner, &ActivateInput{
		ReportRunID:             bound.Run.ReportRunID,
		ConversationID:          "conv-1",
		ExpectedRunRevision:     2,
		ExpectedContextRevision: 0,
		Source:                  "prompt",
	})
	if err != nil || active.ActiveReportRunID != bound.Run.ReportRunID || active.Revision != 1 {
		t.Fatalf("Activate() = %+v, %v", active, err)
	}
	revalidated, err := service.GetContext(owner, "conv-1")
	if err != nil || revalidated.ActiveReportRunID != bound.Run.ReportRunID {
		t.Fatalf("GetContext() = %+v, %v", revalidated, err)
	}

	second := begin(t, service, owner, "bound-2", "conv-1", "prompt")
	complete(t, service, owner, second.Run.ReportRunID, "conv-1", 1)
	if _, err := service.Activate(owner, &ActivateInput{
		ReportRunID:             second.Run.ReportRunID,
		ConversationID:          "conv-1",
		ExpectedRunRevision:     2,
		ExpectedContextRevision: 0,
	}); !errors.Is(err, ErrCAS) {
		t.Fatalf("Activate(stale pointer) error = %v, want CAS", err)
	}

	manual := begin(t, service, owner, "manual", "", "manual")
	if manual.Run.ConversationID != "" {
		t.Fatalf("manual run conversation = %q, want NULL/empty", manual.Run.ConversationID)
	}
	complete(t, service, owner, manual.Run.ReportRunID, "", 1)
	if _, err := service.Adopt(owner, &AdoptInput{
		ReportRunID:             manual.Run.ReportRunID,
		ConversationID:          "conv-2",
		ExpectedRunRevision:     1,
		ExpectedContextRevision: 0,
	}); !errors.Is(err, ErrCAS) {
		t.Fatalf("Adopt(stale run) error = %v, want CAS", err)
	}
	adopted, err := service.Adopt(owner, &AdoptInput{
		ReportRunID:             manual.Run.ReportRunID,
		ConversationID:          "conv-2",
		ExpectedRunRevision:     2,
		ExpectedContextRevision: 0,
		Source:                  "adopt",
	})
	if err != nil {
		t.Fatalf("Adopt() error = %v", err)
	}
	if adopted.Run.ConversationID != "conv-2" || adopted.Context.ActiveReportRunID != manual.Run.ReportRunID {
		t.Fatalf("Adopt() = %+v", adopted)
	}
	retried, err := service.Adopt(owner, &AdoptInput{
		ReportRunID:             manual.Run.ReportRunID,
		ConversationID:          "conv-2",
		ExpectedRunRevision:     adopted.Run.Revision,
		ExpectedContextRevision: 0,
		Source:                  "adopt",
	})
	if err != nil || retried.Run.ReportRunID != manual.Run.ReportRunID ||
		retried.Context.ActiveReportRunID != manual.Run.ReportRunID {
		t.Fatalf("duplicate Adopt() = %+v, %v", retried, err)
	}
	if _, err := service.Adopt(owner, &AdoptInput{
		ConversationID: "conv-2",
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Adopt(without exact ID) error = %v, want invalid", err)
	}
	otherManual := begin(t, service, owner, "manual-2", "", "manual")
	complete(t, service, owner, otherManual.Run.ReportRunID, "", 1)
	if _, err := service.GetRun(owner, otherManual.Run.ReportRunID, ""); err != nil {
		t.Fatalf("exact adoption unexpectedly selected another manual run: %v", err)
	}
	if _, err := service.Adopt(otherOwner, &AdoptInput{
		ReportRunID:             manual.Run.ReportRunID,
		ConversationID:          "conv-2",
		ExpectedRunRevision:     adopted.Run.Revision,
		ExpectedContextRevision: adopted.Context.Revision,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner Adopt() error = %v, want not found", err)
	}
}

func TestService_AdoptRequiresOwnedCompletedManualRunWithValidSnapshot(t *testing.T) {
	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	completedAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	validRun := &reportrun.Record{
		ReportRunID: "run-1",
		OwnerID:     "owner-1",
		Origin:      "manual",
		Status:      reportrun.StatusCompleted,
		CompletedAt: &completedAt,
		Revision:    2,
		ReportSpec:  testSpec,
		ReportFill:  testFill,
		ReportPrint: testPrint,
	}
	tests := []struct {
		name   string
		mutate func(*reportrun.Record)
		want   error
	}{
		{name: "foreign owner", mutate: func(run *reportrun.Record) { run.OwnerID = "owner-2" }, want: ErrNotFound},
		{name: "running", mutate: func(run *reportrun.Record) { run.Status = reportrun.StatusRunning }, want: ErrConflict},
		{name: "missing completion", mutate: func(run *reportrun.Record) { run.CompletedAt = nil }, want: ErrConflict},
		{name: "zero completion", mutate: func(run *reportrun.Record) {
			zero := time.Time{}
			run.CompletedAt = &zero
		}, want: ErrConflict},
		{name: "prompt", mutate: func(run *reportrun.Record) { run.Origin = "prompt" }, want: ErrConflict},
		{name: "missing spec", mutate: func(run *reportrun.Record) { run.ReportSpec = nil }, want: ErrConflict},
		{name: "null fill", mutate: func(run *reportrun.Record) { run.ReportFill = []byte(`null`) }, want: ErrConflict},
		{name: "invalid print", mutate: func(run *reportrun.Record) { run.ReportPrint = []byte(`{`) }, want: ErrConflict},
		{name: "different conversation", mutate: func(run *reportrun.Record) { run.ConversationID = "conv-other" }, want: ErrNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run := cloneRun(validRun)
			test.mutate(run)
			adoptCalled := false
			store := &adoptionRunClient{
				getRun: func(context.Context, string) (*reportrun.Record, error) { return run, nil },
				getContext: func(context.Context, string) (*reportcontext.Record, error) {
					return nil, reportstore.ErrNotFound
				},
				adopt: func(context.Context, *reportrun.Record, int64, *reportcontext.Record, int64) error {
					adoptCalled = true
					return nil
				},
			}
			service := New(Options{Store: store})
			_, err := service.Adopt(ownerCtx, &AdoptInput{
				ReportRunID:             "run-1",
				ConversationID:          "conv-1",
				ExpectedRunRevision:     2,
				ExpectedContextRevision: 0,
			})
			require.ErrorIs(t, err, test.want)
			require.False(t, adoptCalled)
		})
	}
}

func TestService_AdoptIdempotentSuccessRevalidatesFullState(t *testing.T) {
	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	validRun, validContext := validAdoptionState()
	tests := []struct {
		name   string
		mutate func(*reportrun.Record, *reportcontext.Record)
		want   error
	}{
		{name: "run owner", mutate: func(run *reportrun.Record, _ *reportcontext.Record) { run.OwnerID = "owner-2" }, want: ErrNotFound},
		{name: "run status", mutate: func(run *reportrun.Record, _ *reportcontext.Record) { run.Status = reportrun.StatusRunning }, want: ErrNotFound},
		{name: "missing completion", mutate: func(run *reportrun.Record, _ *reportcontext.Record) { run.CompletedAt = nil }, want: ErrNotFound},
		{name: "zero completion", mutate: func(run *reportrun.Record, _ *reportcontext.Record) {
			zero := time.Time{}
			run.CompletedAt = &zero
		}, want: ErrNotFound},
		{name: "run origin", mutate: func(run *reportrun.Record, _ *reportcontext.Record) { run.Origin = "prompt" }, want: ErrNotFound},
		{name: "run snapshot", mutate: func(run *reportrun.Record, _ *reportcontext.Record) { run.ReportSpec = []byte(`null`) }, want: ErrNotFound},
		{name: "run conversation", mutate: func(run *reportrun.Record, _ *reportcontext.Record) { run.ConversationID = "conv-other" }, want: ErrNotFound},
		{name: "active pointer", mutate: func(_ *reportrun.Record, reportCtx *reportcontext.Record) { reportCtx.ActiveReportRunID = "run-other" }, want: ErrNotFound},
		{name: "context owner", mutate: func(_ *reportrun.Record, reportCtx *reportcontext.Record) { reportCtx.OwnerID = "owner-2" }, want: ErrNotFound},
		{name: "context conversation", mutate: func(_ *reportrun.Record, reportCtx *reportcontext.Record) { reportCtx.ConversationID = "conv-other" }, want: ErrNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run := cloneRun(validRun)
			reportCtx := cloneContext(validContext)
			test.mutate(run, reportCtx)
			service := New(Options{Store: &adoptionRunClient{
				getRun:     func(context.Context, string) (*reportrun.Record, error) { return run, nil },
				getContext: func(context.Context, string) (*reportcontext.Record, error) { return reportCtx, nil },
			}})
			_, err := service.Adopt(ownerCtx, &AdoptInput{ReportRunID: "run-1", ConversationID: "conv-1"})
			require.ErrorIs(t, err, test.want)
		})
	}
}

func TestService_AdoptIdempotentContextReadErrors(t *testing.T) {
	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	validRun, _ := validAdoptionState()
	backendErr := errors.New("context backend unavailable")
	tests := []struct {
		name       string
		contextErr error
		want       error
	}{
		{name: "nil context", want: ErrNotFound},
		{name: "missing context", contextErr: reportstore.ErrNotFound, want: ErrNotFound},
		{name: "backend error", contextErr: backendErr, want: backendErr},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := New(Options{Store: &adoptionRunClient{
				getRun: func(context.Context, string) (*reportrun.Record, error) {
					return cloneRun(validRun), nil
				},
				getContext: func(context.Context, string) (*reportcontext.Record, error) {
					return nil, test.contextErr
				},
			}})
			result, err := service.Adopt(ownerCtx, &AdoptInput{ReportRunID: "run-1", ConversationID: "conv-1"})
			require.ErrorIs(t, err, test.want)
			require.Nil(t, result)
		})
	}
}

func TestService_AdoptCASReloadSuccessRevalidatesFullState(t *testing.T) {
	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	initial, _ := validAdoptionState()
	initial.ConversationID = ""
	validReloadedRun, validReloadedContext := validAdoptionState()
	tests := []struct {
		name   string
		mutate func(*reportrun.Record, *reportcontext.Record)
	}{
		{name: "valid", mutate: func(*reportrun.Record, *reportcontext.Record) {}},
		{name: "run owner", mutate: func(run *reportrun.Record, _ *reportcontext.Record) { run.OwnerID = "owner-2" }},
		{name: "run status", mutate: func(run *reportrun.Record, _ *reportcontext.Record) { run.Status = reportrun.StatusRunning }},
		{name: "missing completion", mutate: func(run *reportrun.Record, _ *reportcontext.Record) { run.CompletedAt = nil }},
		{name: "zero completion", mutate: func(run *reportrun.Record, _ *reportcontext.Record) {
			zero := time.Time{}
			run.CompletedAt = &zero
		}},
		{name: "run origin", mutate: func(run *reportrun.Record, _ *reportcontext.Record) { run.Origin = "prompt" }},
		{name: "run snapshot", mutate: func(run *reportrun.Record, _ *reportcontext.Record) { run.ReportPrint = []byte(`null`) }},
		{name: "run conversation", mutate: func(run *reportrun.Record, _ *reportcontext.Record) { run.ConversationID = "conv-other" }},
		{name: "active pointer", mutate: func(_ *reportrun.Record, reportCtx *reportcontext.Record) { reportCtx.ActiveReportRunID = "run-other" }},
		{name: "context owner", mutate: func(_ *reportrun.Record, reportCtx *reportcontext.Record) { reportCtx.OwnerID = "owner-2" }},
		{name: "context conversation", mutate: func(_ *reportrun.Record, reportCtx *reportcontext.Record) { reportCtx.ConversationID = "conv-other" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reloadedRun := cloneRun(validReloadedRun)
			reloadedContext := cloneContext(validReloadedContext)
			test.mutate(reloadedRun, reloadedContext)
			runReads := 0
			contextReads := 0
			service := New(Options{Store: &adoptionRunClient{
				getRun: func(context.Context, string) (*reportrun.Record, error) {
					runReads++
					if runReads == 1 {
						return cloneRun(initial), nil
					}
					return reloadedRun, nil
				},
				getContext: func(context.Context, string) (*reportcontext.Record, error) {
					contextReads++
					if contextReads == 1 {
						return nil, reportstore.ErrNotFound
					}
					return reloadedContext, nil
				},
				adopt: func(context.Context, *reportrun.Record, int64, *reportcontext.Record, int64) error {
					return reportstore.ErrCASMismatch
				},
			}})
			result, err := service.Adopt(ownerCtx, &AdoptInput{
				ReportRunID:             "run-1",
				ConversationID:          "conv-1",
				ExpectedRunRevision:     initial.Revision,
				ExpectedContextRevision: 0,
			})
			if test.name == "valid" {
				require.NoError(t, err)
				require.Equal(t, "run-1", result.Run.ReportRunID)
				return
			}
			require.ErrorIs(t, err, ErrNotFound)
			require.Nil(t, result)
		})
	}
}

func TestService_AdoptCASReloadErrors(t *testing.T) {
	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	initial, _ := validAdoptionState()
	initial.ConversationID = ""
	validReloadedRun, validReloadedContext := validAdoptionState()
	backendErr := errors.New("reload backend unavailable")
	tests := []struct {
		name            string
		reloadedRun     *reportrun.Record
		runErr          error
		reloadedContext *reportcontext.Record
		contextErr      error
		want            error
	}{
		{name: "run not found", runErr: reportstore.ErrNotFound, reloadedContext: validReloadedContext, want: ErrNotFound},
		{name: "run backend error", runErr: backendErr, reloadedContext: validReloadedContext, want: backendErr},
		{name: "nil run", reloadedContext: validReloadedContext, want: ErrNotFound},
		{name: "context not found", reloadedRun: validReloadedRun, contextErr: reportstore.ErrNotFound, want: ErrNotFound},
		{name: "context backend error", reloadedRun: validReloadedRun, contextErr: backendErr, want: backendErr},
		{name: "nil context", reloadedRun: validReloadedRun, want: ErrNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runReads := 0
			contextReads := 0
			service := New(Options{Store: &adoptionRunClient{
				getRun: func(context.Context, string) (*reportrun.Record, error) {
					runReads++
					if runReads == 1 {
						return cloneRun(initial), nil
					}
					return cloneRun(test.reloadedRun), test.runErr
				},
				getContext: func(context.Context, string) (*reportcontext.Record, error) {
					contextReads++
					if contextReads == 1 {
						return nil, reportstore.ErrNotFound
					}
					return cloneContext(test.reloadedContext), test.contextErr
				},
				adopt: func(context.Context, *reportrun.Record, int64, *reportcontext.Record, int64) error {
					return reportstore.ErrCASMismatch
				},
			}})
			result, err := service.Adopt(ownerCtx, &AdoptInput{
				ReportRunID:             "run-1",
				ConversationID:          "conv-1",
				ExpectedRunRevision:     initial.Revision,
				ExpectedContextRevision: 0,
			})
			require.ErrorIs(t, err, test.want)
			require.Nil(t, result)
		})
	}
}

func validAdoptionState() (*reportrun.Record, *reportcontext.Record) {
	completedAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	return &reportrun.Record{
			ReportRunID:    "run-1",
			OwnerID:        "owner-1",
			ConversationID: "conv-1",
			Origin:         "manual",
			Status:         reportrun.StatusCompleted,
			CompletedAt:    &completedAt,
			Revision:       3,
			ReportSpec:     testSpec,
			ReportFill:     testFill,
			ReportPrint:    testPrint,
		}, &reportcontext.Record{
			OwnerID:           "owner-1",
			ConversationID:    "conv-1",
			ActiveReportRunID: "run-1",
			Revision:          1,
		}
}

type adoptionRunClient struct {
	reportstore.RunClient
	getRun     func(context.Context, string) (*reportrun.Record, error)
	getContext func(context.Context, string) (*reportcontext.Record, error)
	adopt      func(context.Context, *reportrun.Record, int64, *reportcontext.Record, int64) error
}

func (s *adoptionRunClient) GetReportRun(ctx context.Context, reportRunID string) (*reportrun.Record, error) {
	return s.getRun(ctx, reportRunID)
}

func (s *adoptionRunClient) GetConversationReportContext(ctx context.Context, conversationID string) (*reportcontext.Record, error) {
	return s.getContext(ctx, conversationID)
}

func (s *adoptionRunClient) AdoptReportRunAndContextCAS(ctx context.Context, run *reportrun.Record, expectedRunRevision int64, reportCtx *reportcontext.Record, expectedContextRevision int64) error {
	return s.adopt(ctx, run, expectedRunRevision, reportCtx, expectedContextRevision)
}

func TestService_PromptRequiresConversation(t *testing.T) {
	service, _ := newTestService(t)
	owner := authsvc.InjectUser(context.Background(), "owner-1")
	if _, err := service.Begin(owner, &BeginInput{
		UIRunRequestID: "prompt-without-conversation",
		Origin:         "prompt",
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Begin(prompt without conversation) error = %v, want invalid", err)
	}
}

func TestService_WaitTerminalCompletedAndFailed(t *testing.T) {
	service, _ := newTestService(t)
	owner := authsvc.InjectUser(context.Background(), "owner-1")

	completedRun := begin(t, service, owner, "wait-completed", "conv-1", "prompt")
	completedResult := make(chan *reportrun.Record, 1)
	completedErr := make(chan error, 1)
	go func() {
		result, err := service.WaitTerminal(owner, completedRun.Run.ReportRunID, "conv-1")
		completedResult <- result
		completedErr <- err
	}()
	complete(t, service, owner, completedRun.Run.ReportRunID, "conv-1", 1)
	require.NoError(t, <-completedErr)
	completed := <-completedResult
	require.Equal(t, completedRun.Run.ReportRunID, completed.ReportRunID)
	require.Equal(t, reportrun.StatusCompleted, completed.Status)
	require.Equal(t, int64(2), completed.Revision)

	failedRun := begin(t, service, owner, "wait-failed", "conv-1", "prompt")
	failedResult := make(chan *reportrun.Record, 1)
	failedErr := make(chan error, 1)
	go func() {
		result, err := service.WaitTerminal(owner, failedRun.Run.ReportRunID, "conv-1")
		failedResult <- result
		failedErr <- err
	}()
	_, err := service.Fail(owner, &FailInput{
		ReportRunID:      failedRun.Run.ReportRunID,
		ConversationID:   "conv-1",
		ExpectedRevision: 1,
		FailureCode:      "render_failed",
		FailureText:      "render failed",
	})
	require.NoError(t, err)
	require.NoError(t, <-failedErr)
	failed := <-failedResult
	require.Equal(t, failedRun.Run.ReportRunID, failed.ReportRunID)
	require.Equal(t, reportrun.StatusFailed, failed.Status)
	require.Equal(t, int64(2), failed.Revision)
}

func TestService_WaitTerminalCancellationTimeoutAndScope(t *testing.T) {
	service, _ := newTestService(t)
	owner := authsvc.InjectUser(context.Background(), "owner-1")
	otherOwner := authsvc.InjectUser(context.Background(), "owner-2")
	run := begin(t, service, owner, "wait-scope", "conv-1", "prompt")

	cancelled, cancel := context.WithCancel(owner)
	cancel()
	started := time.Now()
	_, err := service.WaitTerminal(cancelled, run.Run.ReportRunID, "conv-1")
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, time.Since(started), 100*time.Millisecond)

	timed, cancelTimed := context.WithTimeout(owner, 20*time.Millisecond)
	defer cancelTimed()
	_, err = service.WaitTerminal(timed, run.Run.ReportRunID, "conv-1")
	require.ErrorIs(t, err, context.DeadlineExceeded)

	_, err = service.WaitTerminal(otherOwner, run.Run.ReportRunID, "conv-1")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = service.WaitTerminal(owner, run.Run.ReportRunID, "conv-2")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = service.WaitTerminal(owner, "", "conv-1")
	require.ErrorIs(t, err, ErrInvalid)
}

func TestService_WaitTerminalUsesExactIDAndProductionBackoff(t *testing.T) {
	service, _ := newTestService(t)
	owner := authsvc.InjectUser(context.Background(), "owner-1")
	target := begin(t, service, owner, "wait-target", "conv-1", "prompt")
	other := begin(t, service, owner, "wait-other", "conv-1", "prompt")
	complete(t, service, owner, other.Run.ReportRunID, "conv-1", 1)

	ctx, cancel := context.WithTimeout(owner, 20*time.Millisecond)
	defer cancel()
	_, err := service.WaitTerminal(ctx, target.Run.ReportRunID, "conv-1")
	require.ErrorIs(t, err, context.DeadlineExceeded)

	require.Equal(t, 300*time.Second, waitTerminalBudget)
	require.Equal(t, 250*time.Millisecond, waitTerminalPollDelay(0))
	require.Equal(t, 500*time.Millisecond, waitTerminalPollDelay(1))
	require.Equal(t, time.Second, waitTerminalPollDelay(2))
	require.Equal(t, time.Second, waitTerminalPollDelay(3))
	require.Equal(t, time.Second, waitTerminalPollDelay(100))
}

func TestService_ValidatesOnlyDurableT1RunEventsFromPersistedLifecycle(t *testing.T) {
	service, _ := newTestService(t)
	owner := authsvc.InjectUser(context.Background(), "owner-1")
	otherOwner := authsvc.InjectUser(context.Background(), "owner-2")
	started := begin(t, service, owner, "event-run", "conv-1", "prompt")

	if err := service.ValidateDurableUIEvent(owner, "conv-1", "report.export_complete", nil); err != nil {
		t.Fatalf("non-T1 export event validation error = %v", err)
	}
	if err := service.ValidateDurableUIEvent(owner, "conv-1", "report.run_start", map[string]interface{}{
		"reportRunId": started.Run.ReportRunID,
		"revision":    int64(1),
		"status":      "running",
	}); err != nil {
		t.Fatalf("valid report.run_start error = %v", err)
	}
	for name, testCase := range map[string]struct {
		ctx            context.Context
		conversationID string
		kind           string
		detail         map[string]interface{}
		want           error
	}{
		"bogus run": {
			ctx: owner, conversationID: "conv-1", kind: "report.run_start",
			detail: map[string]interface{}{"reportRunId": "bogus", "revision": 1, "status": "running"},
			want:   ErrNotFound,
		},
		"cross owner": {
			ctx: otherOwner, conversationID: "conv-1", kind: "report.run_start",
			detail: map[string]interface{}{"reportRunId": started.Run.ReportRunID, "revision": 1, "status": "running"},
			want:   ErrNotFound,
		},
		"cross conversation": {
			ctx: owner, conversationID: "conv-2", kind: "report.run_start",
			detail: map[string]interface{}{"reportRunId": started.Run.ReportRunID, "revision": 1, "status": "running"},
			want:   ErrNotFound,
		},
		"terminal event before persistence": {
			ctx: owner, conversationID: "conv-1", kind: "report.run",
			detail: map[string]interface{}{"reportRunId": started.Run.ReportRunID, "revision": 1, "status": "completed"},
			want:   ErrConflict,
		},
		"missing exact revision": {
			ctx: owner, conversationID: "conv-1", kind: "report.run_start",
			detail: map[string]interface{}{"reportRunId": started.Run.ReportRunID, "status": "running"},
			want:   ErrInvalid,
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := service.ValidateDurableUIEvent(testCase.ctx, testCase.conversationID, testCase.kind, testCase.detail)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("ValidateDurableUIEvent() error = %v, want %v", err, testCase.want)
			}
		})
	}

	complete(t, service, owner, started.Run.ReportRunID, "conv-1", 1)
	if err := service.ValidateDurableUIEvent(owner, "conv-1", "report.run_start", map[string]interface{}{
		"reportRunId": started.Run.ReportRunID,
		"revision":    1,
		"status":      "running",
	}); !errors.Is(err, ErrCAS) {
		t.Fatalf("stale report.run_start error = %v, want CAS", err)
	}
	if err := service.ValidateDurableUIEvent(owner, "conv-1", "report.run", map[string]interface{}{
		"reportRunId": started.Run.ReportRunID,
		"revision":    2,
		"status":      "failed",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong terminal status error = %v, want conflict", err)
	}
	if err := service.ValidateDurableUIEvent(owner, "conv-1", "report.run", map[string]interface{}{
		"reportRunId": started.Run.ReportRunID,
		"revision":    float64(2),
		"status":      "completed",
	}); err != nil {
		t.Fatalf("valid report.run error = %v", err)
	}
}
