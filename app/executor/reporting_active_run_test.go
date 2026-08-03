package executor_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	execconfig "github.com/viant/agently-core/app/executor/config"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	authsvc "github.com/viant/agently-core/service/auth"
	reportingsvc "github.com/viant/agently-core/service/reporting"
	reportingrunsvc "github.com/viant/agently-core/service/reportingrun"
)

func TestBuilderBuild_ActiveReportRunRegistryPersistsAcrossRuntimeRebuild(t *testing.T) {
	isolateActiveReportRunTestState(t)

	defaults := &execconfig.Defaults{
		Reporting: execconfig.ReportingDefaults{
			Enabled:         true,
			QueueIntervalMs: int(time.Hour / time.Millisecond),
			TransitionalWithUI: execconfig.ReportingTransitionalWithUIDefaults{
				Admission:     "open",
				Persistence:   "enabled",
				ExportFromRun: "enabled",
				Orchestration: "enabled",
			},
		},
	}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	first, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithDefaults(defaults).
		Build(firstCtx)
	require.NoError(t, err)
	require.NotNil(t, first.Reporting)
	require.NotNil(t, first.ReportRuns)
	requireRegistryTool(t, first, "reporting/get_active_report_run", true)

	ownerCtx := authsvc.InjectUser(context.Background(), "restart-owner")
	begun, err := first.ReportRuns.Begin(ownerCtx, &reportingrunsvc.BeginInput{
		ConversationID:  "restart-conversation",
		Origin:          "prompt",
		BuilderRef:      "metricReportBuilder",
		PresetID:        "performance_inventory_brief",
		SourceKind:      "preset",
		SourceID:        "performance_inventory_brief",
		RequestedParams: json.RawMessage(`{"orderId":2676946}`),
		EffectiveParams: json.RawMessage(`{"orderId":2676946}`),
		UIRunRequestID:  "restart-request",
	})
	require.NoError(t, err)
	completed, err := first.ReportRuns.Complete(ownerCtx, &reportingrunsvc.CompleteInput{
		ReportRunID:      begun.Run.ReportRunID,
		ConversationID:   "restart-conversation",
		ExpectedRevision: begun.Run.Revision,
		ReportSpec:       json.RawMessage(`{"kind":"reportSpec","version":1}`),
		ReportFill:       json.RawMessage(`{"kind":"reportFill","version":1}`),
		ReportPrint:      json.RawMessage(`{"kind":"reportPrint","version":1}`),
	})
	require.NoError(t, err)
	_, err = first.ReportRuns.Activate(ownerCtx, &reportingrunsvc.ActivateInput{
		ReportRunID:             completed.ReportRunID,
		ConversationID:          "restart-conversation",
		ExpectedRunRevision:     completed.Revision,
		ExpectedContextRevision: 0,
		Source:                  "prompt",
	})
	require.NoError(t, err)
	cancelFirst()

	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	second, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithDefaults(defaults).
		Build(secondCtx)
	require.NoError(t, err)
	require.NotNil(t, second.ReportRuns)
	requireRegistryTool(t, second, "reporting/get_active_report_run", true)

	trustedCtx := runtimerequestctx.WithConversationID(ownerCtx, "restart-conversation")
	raw, err := second.Registry.Execute(trustedCtx, "reporting:get_active_report_run", map[string]interface{}{})
	require.NoError(t, err)
	var active reportingsvc.ActiveReportRun
	require.NoError(t, json.Unmarshal([]byte(raw), &active))
	require.Equal(t, completed.ReportRunID, active.ReportRunID)
	require.Equal(t, completed.Revision, active.Revision)
	require.Equal(t, "report-run://"+completed.ReportRunID, active.ArtifactRef)
	require.NotContains(t, raw, "restart-owner")
	require.NotContains(t, raw, "reportSpec")
}

func TestBuilderBuild_ActiveReportRunRegistryRequiresPersistenceAndOrchestration(t *testing.T) {
	isolateActiveReportRunTestState(t)
	tests := []struct {
		name          string
		persistence   string
		orchestration string
	}{
		{name: "persistence only", persistence: "enabled", orchestration: "disabled"},
		{name: "orchestration without persistence", persistence: "disabled", orchestration: "enabled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runtime, err := executor.NewBuilder().
				WithAgentFinder(stubAgentFinder{}).
				WithModelFinder(stubModelFinder{}).
				WithDefaults(&execconfig.Defaults{
					Reporting: execconfig.ReportingDefaults{
						Enabled: true,
						TransitionalWithUI: execconfig.ReportingTransitionalWithUIDefaults{
							Admission:     "open",
							Persistence:   test.persistence,
							ExportFromRun: "enabled",
							Orchestration: test.orchestration,
						},
					},
				}).
				Build(ctx)
			require.NoError(t, err)
			requireRegistryTool(t, runtime, "reporting/get_active_report_run", false)
		})
	}
}

func isolateActiveReportRunTestState(t *testing.T) {
	t.Helper()
	isolationRoot := t.TempDir()
	previousWorkingDirectory, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(isolationRoot))
	t.Cleanup(func() {
		if err := os.Chdir(previousWorkingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	t.Setenv("AGENTLY_WORKSPACE", filepath.Join(isolationRoot, "workspace"))
	t.Setenv("AGENTLY_RUNTIME_ROOT", filepath.Join(isolationRoot, "runtime"))
	t.Setenv("AGENTLY_STATE_PATH", filepath.Join(isolationRoot, "state"))
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	t.Setenv("AGENTLY_DB_PATH", filepath.Join(isolationRoot, "agently.db"))
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
}

func requireRegistryTool(t *testing.T, runtime *executor.Runtime, name string, want bool) {
	t.Helper()
	definitions := runtime.Registry.MatchDefinition("reporting:*")
	found := false
	for _, definition := range definitions {
		if definition != nil && definition.Name == name {
			found = true
			break
		}
	}
	require.Equal(t, want, found)
}
