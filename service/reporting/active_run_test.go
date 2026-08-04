package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	reportcontext "github.com/viant/agently-core/pkg/agently/reportcontext"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
	mcpmanager "github.com/viant/agently-core/protocol/mcp/manager"
	toolregistry "github.com/viant/agently-core/protocol/tool"
	mcpadapter "github.com/viant/agently-core/protocol/tool/adapter/mcp"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	authsvc "github.com/viant/agently-core/service/auth"
	reportingrunsvc "github.com/viant/agently-core/service/reportingrun"
)

func TestServiceGetActiveReportRunSanitizesAndScopesExactPromptRun(t *testing.T) {
	client := reportmemory.New()
	runClient, ok := client.(reportstore.RunClient)
	require.True(t, ok)
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	runs := reportingrunsvc.New(reportingrunsvc.Options{
		Store: runClient,
		Now: func() time.Time {
			now = now.Add(time.Second)
			return now
		},
		NewID: func() string { return "run-active-1" },
	})
	service := New(Options{
		Store:             NewStoreAdapter(client),
		ActiveRunResolver: runs,
	})
	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	begun, err := runs.Begin(ownerCtx, &reportingrunsvc.BeginInput{
		ConversationID:  "conv-1",
		Origin:          "prompt",
		BuilderRef:      "metricReportBuilder",
		PresetID:        "performance_inventory_brief",
		SourceKind:      "preset",
		SourceID:        "performance_inventory_brief",
		RequestedParams: json.RawMessage(`{"orderId":2676946}`),
		EffectiveParams: json.RawMessage(`{"orderId":2676946,"timezone":"UTC"}`),
		UIRunRequestID:  "ui-request-1",
	})
	require.NoError(t, err)
	completed, err := runs.Complete(ownerCtx, &reportingrunsvc.CompleteInput{
		ReportRunID:      begun.Run.ReportRunID,
		ConversationID:   "conv-1",
		ExpectedRevision: begun.Run.Revision,
		ReportSpec:       json.RawMessage(`{"kind":"reportSpec","secret":"must-not-leak"}`),
		ReportFill:       json.RawMessage(`{"kind":"reportFill","rows":[{"private":"data"}]}`),
		ReportPrint:      json.RawMessage(`{"kind":"reportPrint","pages":[]}`),
	})
	require.NoError(t, err)
	_, err = runs.Activate(ownerCtx, &reportingrunsvc.ActivateInput{
		ReportRunID:             completed.ReportRunID,
		ConversationID:          "conv-1",
		ExpectedRunRevision:     completed.Revision,
		ExpectedContextRevision: 0,
		Source:                  "prompt",
	})
	require.NoError(t, err)

	trustedCtx := runtimerequestctx.WithConversationID(ownerCtx, "conv-1")
	method, err := service.Method("get_active_report_run")
	require.NoError(t, err)
	var output ActiveReportRun
	require.NoError(t, method(trustedCtx, &GetActiveReportRunInput{}, &output))
	require.Equal(t, "run-active-1", output.ReportRunID)
	require.Equal(t, int64(2), output.Revision)
	require.Equal(t, "completed", output.Status)
	require.Equal(t, "report-run://run-active-1", output.ArtifactRef)
	require.Equal(t, "prompt", output.Origin)
	require.Equal(t, "metricReportBuilder", output.BuilderRef)
	require.Equal(t, "performance_inventory_brief", output.PresetID)
	require.JSONEq(t, `2676946`, string(output.RequestedParams["orderId"]))
	require.JSONEq(t, `"UTC"`, string(output.EffectiveParams["timezone"]))
	require.NotEmpty(t, output.CompletedAt)

	payload, err := json.Marshal(output)
	require.NoError(t, err)
	for _, forbidden := range []string{"owner-1", "ownerId", "reportSpec", "reportFill", "reportPrint", "must-not-leak", "private"} {
		require.NotContains(t, string(payload), forbidden)
	}

	_, err = service.GetActiveReportRun(runtimerequestctx.WithConversationID(authsvc.InjectUser(context.Background(), "owner-2"), "conv-1"))
	require.ErrorIs(t, err, ErrNotFound)
	_, err = service.GetActiveReportRun(runtimerequestctx.WithConversationID(ownerCtx, "conv-2"))
	require.ErrorIs(t, err, ErrNotFound)
	_, err = service.GetActiveReportRun(ownerCtx)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = service.GetActiveReportRun(runtimerequestctx.WithConversationID(context.Background(), "conv-1"))
	require.ErrorIs(t, err, ErrNotFound)
}

func TestServiceGetActiveReportRunFailsClosedForUntrustedRunState(t *testing.T) {
	completedAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	validContext := &reportcontext.Record{OwnerID: "owner-1", ConversationID: "conv-1", ActiveReportRunID: "run-1"}
	validRun := &reportrun.Record{
		ReportRunID:     "run-1",
		OwnerID:         "owner-1",
		ConversationID:  "conv-1",
		Origin:          "prompt",
		Status:          reportrun.StatusCompleted,
		CompletedAt:     &completedAt,
		Revision:        2,
		RequestedParams: json.RawMessage(`{}`),
		EffectiveParams: json.RawMessage(`{}`),
	}
	tests := []struct {
		name       string
		mutate     func(*reportcontext.Record, *reportrun.Record)
		contextErr error
		runErr     error
	}{
		{name: "missing pointer", mutate: func(ctx *reportcontext.Record, _ *reportrun.Record) { ctx.ActiveReportRunID = "" }},
		{name: "context owner mismatch", mutate: func(ctx *reportcontext.Record, _ *reportrun.Record) { ctx.OwnerID = "owner-2" }},
		{name: "run owner mismatch", mutate: func(_ *reportcontext.Record, run *reportrun.Record) { run.OwnerID = "owner-2" }},
		{name: "mismatched run", mutate: func(_ *reportcontext.Record, run *reportrun.Record) { run.ReportRunID = "other" }},
		{name: "running", mutate: func(_ *reportcontext.Record, run *reportrun.Record) { run.Status = reportrun.StatusRunning }},
		{name: "manual", mutate: func(_ *reportcontext.Record, run *reportrun.Record) { run.Origin = "manual" }},
		{name: "adopted", mutate: func(_ *reportcontext.Record, run *reportrun.Record) { run.AdoptionSource = "adopt" }},
		{name: "invalid params", mutate: func(_ *reportcontext.Record, run *reportrun.Record) { run.RequestedParams = json.RawMessage(`[]`) }},
		{name: "context error", contextErr: errors.New("backend unavailable")},
		{name: "run error", runErr: errors.New("backend unavailable")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reportCtx := *validContext
			run := *validRun
			if test.mutate != nil {
				test.mutate(&reportCtx, &run)
			}
			resolver := &activeRunResolverStub{
				context:    &reportCtx,
				run:        &run,
				contextErr: test.contextErr,
				runErr:     test.runErr,
			}
			service := New(Options{Store: NewStoreAdapter(reportmemory.New()), ActiveRunResolver: resolver})
			ctx := runtimerequestctx.WithConversationID(authsvc.InjectUser(context.Background(), "owner-1"), "conv-1")
			_, err := service.GetActiveReportRun(ctx)
			require.ErrorIs(t, err, ErrNotFound)
		})
	}
}

func TestServiceGetActiveReportRunConversationAdoptionFlag(t *testing.T) {
	completedAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	validContext := &reportcontext.Record{
		OwnerID:           "owner-1",
		ConversationID:    "conv-1",
		ActiveReportRunID: "run-1",
	}
	validManualRun := &reportrun.Record{
		ReportRunID:     "run-1",
		OwnerID:         "owner-1",
		ConversationID:  "conv-1",
		Origin:          "manual",
		Status:          reportrun.StatusCompleted,
		CompletedAt:     &completedAt,
		Revision:        3,
		RequestedParams: json.RawMessage(`{}`),
		EffectiveParams: json.RawMessage(`{}`),
		ReportSpec:      json.RawMessage(`{"kind":"reportSpec"}`),
		ReportFill:      json.RawMessage(`{"kind":"reportFill"}`),
		ReportPrint:     json.RawMessage(`{"kind":"reportPrint"}`),
	}
	trustedCtx := runtimerequestctx.WithConversationID(authsvc.InjectUser(context.Background(), "owner-1"), "conv-1")

	t.Run("disabled remains prompt only", func(t *testing.T) {
		service := New(Options{
			Store: NewStoreAdapter(reportmemory.New()),
			ActiveRunResolver: &activeRunResolverStub{
				context: validContext,
				run:     validManualRun,
			},
		})
		_, err := service.GetActiveReportRun(trustedCtx)
		require.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("enabled returns adopted manual origin", func(t *testing.T) {
		service := New(Options{
			Store:                       NewStoreAdapter(reportmemory.New()),
			ActiveRunResolver:           &activeRunResolverStub{context: validContext, run: validManualRun},
			ConversationAdoptionEnabled: true,
		})
		active, err := service.GetActiveReportRun(trustedCtx)
		require.NoError(t, err)
		require.Equal(t, "manual", active.Origin)
	})

	t.Run("enabled does not add snapshot gate to prompt", func(t *testing.T) {
		promptRun := *validManualRun
		promptRun.Origin = "prompt"
		promptRun.ReportSpec = nil
		promptRun.ReportFill = nil
		promptRun.ReportPrint = nil
		service := New(Options{
			Store:                       NewStoreAdapter(reportmemory.New()),
			ActiveRunResolver:           &activeRunResolverStub{context: validContext, run: &promptRun},
			ConversationAdoptionEnabled: true,
		})
		active, err := service.GetActiveReportRun(trustedCtx)
		require.NoError(t, err)
		require.Equal(t, "prompt", active.Origin)
	})
	t.Run("disabled does not add snapshot gate to prompt", func(t *testing.T) {
		promptRun := *validManualRun
		promptRun.Origin = "prompt"
		promptRun.ReportSpec = nil
		promptRun.ReportFill = nil
		promptRun.ReportPrint = nil
		service := New(Options{
			Store:             NewStoreAdapter(reportmemory.New()),
			ActiveRunResolver: &activeRunResolverStub{context: validContext, run: &promptRun},
		})
		active, err := service.GetActiveReportRun(trustedCtx)
		require.NoError(t, err)
		require.Equal(t, "prompt", active.Origin)
	})

	for _, test := range []struct {
		name   string
		mutate func(*reportcontext.Record, *reportrun.Record)
	}{
		{name: "missing spec", mutate: func(_ *reportcontext.Record, run *reportrun.Record) { run.ReportSpec = nil }},
		{name: "null fill", mutate: func(_ *reportcontext.Record, run *reportrun.Record) { run.ReportFill = json.RawMessage(`null`) }},
		{name: "invalid print", mutate: func(_ *reportcontext.Record, run *reportrun.Record) { run.ReportPrint = json.RawMessage(`{`) }},
		{name: "run owner mismatch", mutate: func(_ *reportcontext.Record, run *reportrun.Record) { run.OwnerID = "owner-2" }},
		{name: "run conversation mismatch", mutate: func(_ *reportcontext.Record, run *reportrun.Record) { run.ConversationID = "conv-2" }},
		{name: "context owner mismatch", mutate: func(reportCtx *reportcontext.Record, _ *reportrun.Record) { reportCtx.OwnerID = "owner-2" }},
		{name: "context conversation mismatch", mutate: func(reportCtx *reportcontext.Record, _ *reportrun.Record) { reportCtx.ConversationID = "conv-2" }},
		{name: "active pointer mismatch", mutate: func(reportCtx *reportcontext.Record, _ *reportrun.Record) { reportCtx.ActiveReportRunID = "run-2" }},
	} {
		t.Run("enabled rejects manual "+test.name, func(t *testing.T) {
			reportCtx := *validContext
			run := *validManualRun
			test.mutate(&reportCtx, &run)
			service := New(Options{
				Store:                       NewStoreAdapter(reportmemory.New()),
				ActiveRunResolver:           &activeRunResolverStub{context: &reportCtx, run: &run},
				ConversationAdoptionEnabled: true,
			})
			_, err := service.GetActiveReportRun(trustedCtx)
			require.ErrorIs(t, err, ErrNotFound)
		})
	}
}

func TestServiceGetActiveReportRunReadsContextBeforeExactRun(t *testing.T) {
	completedAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	resolver := &activeRunResolverStub{
		context: &reportcontext.Record{OwnerID: "owner-1", ConversationID: "conv-1", ActiveReportRunID: "run-1"},
		run: &reportrun.Record{
			ReportRunID:     "run-1",
			OwnerID:         "owner-1",
			ConversationID:  "conv-1",
			Origin:          "prompt",
			Status:          reportrun.StatusCompleted,
			CompletedAt:     &completedAt,
			Revision:        2,
			RequestedParams: json.RawMessage(`{}`),
			EffectiveParams: json.RawMessage(`{}`),
		},
	}
	service := New(Options{Store: NewStoreAdapter(reportmemory.New()), ActiveRunResolver: resolver})
	ctx := runtimerequestctx.WithConversationID(authsvc.InjectUser(context.Background(), "owner-1"), "conv-1")
	active, err := service.GetActiveReportRun(ctx)
	require.NoError(t, err)
	require.Equal(t, "run-1", active.ReportRunID)
	require.Equal(t, []string{"context:conv-1", "run:run-1:conv-1"}, resolver.calls)
}

func TestGetActiveReportRunInputAcceptsOnlyEmptyJSONObject(t *testing.T) {
	var input GetActiveReportRunInput
	require.NoError(t, json.Unmarshal([]byte(`{}`), &input))
	require.NoError(t, json.Unmarshal([]byte("  { }\n"), &input))

	for _, payload := range []string{
		`{"ownerId":"owner-2"}`,
		`{"conversationId":"conv-2"}`,
		`{"windowId":"window-1"}`,
		`{"reportRunId":"run-2"}`,
		`{"arbitrary":true}`,
		`null`,
		`[]`,
		`""`,
		`{"ownerId":`,
		`{} {}`,
	} {
		t.Run(payload, func(t *testing.T) {
			var decoded GetActiveReportRunInput
			require.Error(t, json.Unmarshal([]byte(payload), &decoded))
		})
	}
}

func TestServiceActiveRunRegistryRejectsNonEmptyInputBeforeResolver(t *testing.T) {
	resolver := validActiveRunResolverStub()
	service := New(Options{Store: NewStoreAdapter(reportmemory.New()), ActiveRunResolver: resolver})
	mgr, err := mcpmanager.New(nil)
	require.NoError(t, err)
	registry, err := toolregistry.NewDefaultRegistry(mgr)
	require.NoError(t, err)
	require.NoError(t, toolregistry.AddInternalService(registry, service))
	ctx := runtimerequestctx.WithConversationID(authsvc.InjectUser(context.Background(), "owner-1"), "conv-1")

	tests := map[string]map[string]interface{}{
		"owner":        {"ownerId": "owner-2"},
		"conversation": {"conversationId": "conv-2"},
		"window":       {"windowId": "window-1"},
		"run":          {"reportRunId": "run-2"},
		"arbitrary":    {"unexpected": true},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			resolver.calls = nil
			_, err := registry.Execute(ctx, "reporting:get_active_report_run", args)
			require.Error(t, err)
			require.Empty(t, resolver.calls, "input rejection must happen before resolver execution")
		})
	}

	resolver.calls = nil
	raw, err := registry.Execute(ctx, "reporting:get_active_report_run", map[string]interface{}{})
	require.NoError(t, err)
	require.Contains(t, raw, `"reportRunId":"run-1"`)
	require.Equal(t, []string{"context:conv-1", "run:run-1:conv-1"}, resolver.calls)
}

func TestServiceGetActiveReportRunMissingAuthDoesNotReachResolver(t *testing.T) {
	resolver := validActiveRunResolverStub()
	service := New(Options{Store: NewStoreAdapter(reportmemory.New()), ActiveRunResolver: resolver})
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	_, err := service.GetActiveReportRun(ctx)
	require.ErrorIs(t, err, ErrNotFound)
	require.Empty(t, resolver.calls)
}

func TestServiceAdvertisesActiveRunOnlyWithResolverAndEmptyInput(t *testing.T) {
	withoutResolver := New(Options{Store: NewStoreAdapter(reportmemory.New())})
	_, err := withoutResolver.Method("get_active_report_run")
	require.Error(t, err)
	for _, signature := range withoutResolver.Methods() {
		require.NotEqual(t, "get_active_report_run", signature.Name)
	}

	withResolver := New(Options{
		Store:             NewStoreAdapter(reportmemory.New()),
		ActiveRunResolver: &activeRunResolverStub{},
	})
	tools := mcpadapter.FromService(withResolver)
	found := false
	for _, tool := range tools {
		if tool.Name != "get_active_report_run" {
			continue
		}
		found = true
		require.Empty(t, tool.InputSchema.Properties)
		require.Empty(t, tool.InputSchema.Required)
		require.NotContains(t, tool.OutputSchema.Properties, "ownerId")
		require.NotContains(t, tool.OutputSchema.Properties, "reportSpec")
		require.Equal(t, "object", tool.OutputSchema.Properties["requestedParams"]["type"])
	}
	require.True(t, found)
}

func TestServiceNormalizesTypedNilActiveRunResolver(t *testing.T) {
	var typedNil *activeRunResolverStub
	service := New(Options{
		Store:             NewStoreAdapter(reportmemory.New()),
		ActiveRunResolver: typedNil,
	})
	require.Nil(t, service.activeRunResolver)
	for _, signature := range service.Methods() {
		require.NotEqual(t, "get_active_report_run", signature.Name)
	}
	_, err := service.Method("get_active_report_run")
	require.Error(t, err)
	ctx := runtimerequestctx.WithConversationID(authsvc.InjectUser(context.Background(), "owner-1"), "conv-1")
	_, err = service.GetActiveReportRun(ctx)
	require.ErrorIs(t, err, ErrNotFound)
}

func validActiveRunResolverStub() *activeRunResolverStub {
	completedAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	return &activeRunResolverStub{
		context: &reportcontext.Record{OwnerID: "owner-1", ConversationID: "conv-1", ActiveReportRunID: "run-1"},
		run: &reportrun.Record{
			ReportRunID:     "run-1",
			OwnerID:         "owner-1",
			ConversationID:  "conv-1",
			Origin:          "prompt",
			Status:          reportrun.StatusCompleted,
			CompletedAt:     &completedAt,
			Revision:        2,
			RequestedParams: json.RawMessage(`{}`),
			EffectiveParams: json.RawMessage(`{}`),
		},
	}
}

type activeRunResolverStub struct {
	context    *reportcontext.Record
	run        *reportrun.Record
	contextErr error
	runErr     error
	calls      []string
}

func (s *activeRunResolverStub) GetContext(_ context.Context, conversationID string) (*reportcontext.Record, error) {
	s.calls = append(s.calls, "context:"+conversationID)
	return s.context, s.contextErr
}

func (s *activeRunResolverStub) GetRun(_ context.Context, reportRunID, conversationID string) (*reportrun.Record, error) {
	s.calls = append(s.calls, "run:"+reportRunID+":"+conversationID)
	return s.run, s.runErr
}
