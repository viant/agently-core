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
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	authsvc "github.com/viant/agently-core/service/auth"
	reportingsvc "github.com/viant/agently-core/service/reporting"
	"github.com/viant/agently-core/workspace"
	wsconfig "github.com/viant/agently-core/workspace/config"
)

func TestBuilderBuild_RegistersReportingService(t *testing.T) {
	reportingSvc := reportingsvc.New(reportingsvc.Options{
		Store: reportingsvc.NewStoreAdapter(reportmemory.New()),
	})
	rt, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithReportingService(reportingSvc).
		Build(context.Background())
	require.NoError(t, err)
	require.NotNil(t, rt.Reporting)
	require.Same(t, reportingSvc, rt.Reporting)

	defs := rt.Registry.MatchDefinition("reporting:*")
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		if def == nil {
			continue
		}
		names = append(names, def.Name)
	}

	require.Contains(t, names, "reporting/compile")
	require.Contains(t, names, "reporting/export_report")
	require.Contains(t, names, "reporting/submit_export")
	require.Contains(t, names, "reporting/get_export_status")
	require.Contains(t, names, "reporting/list_export_jobs")
	require.Contains(t, names, "reporting/list_export_artifacts")
	require.Contains(t, names, "reporting/get_artifact")
	require.Contains(t, names, "reporting/save_report")
	require.Contains(t, names, "reporting/get_report")
	require.Contains(t, names, "reporting/list_reports")
	require.Contains(t, names, "reporting/update_report")
	require.NotContains(t, names, "reporting/run_export")
	require.NotContains(t, names, "reporting/start_export")
	require.NotContains(t, names, "reporting/complete_export")
	require.NotContains(t, names, "reporting/fail_export")
}

func TestBuilderBuild_DoesNotRegisterReportingServiceByDefault(t *testing.T) {
	rt, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		Build(context.Background())
	require.NoError(t, err)
	require.Nil(t, rt.Reporting)

	defs := rt.Registry.MatchDefinition("reporting:*")
	require.Empty(t, defs)
}

func TestBuilderBuild_RegistersReportingServiceFromDefaults(t *testing.T) {
	rt, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithDefaults(&execconfig.Defaults{
			Reporting: execconfig.ReportingDefaults{Enabled: true},
		}).
		Build(context.Background())
	require.NoError(t, err)
	require.NotNil(t, rt.Reporting)

	defs := rt.Registry.MatchDefinition("reporting:*")
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		if def == nil {
			continue
		}
		names = append(names, def.Name)
	}
	require.Contains(t, names, "reporting/compile")
	require.Contains(t, names, "reporting/export_report")
	require.Contains(t, names, "reporting/submit_export")
	require.Contains(t, names, "reporting/list_export_jobs")
	require.Contains(t, names, "reporting/list_export_artifacts")
	require.Contains(t, names, "reporting/save_report")
	require.Contains(t, names, "reporting/get_report")
	require.Contains(t, names, "reporting/list_reports")
	require.Contains(t, names, "reporting/update_report")
}

func TestBuilderBuild_RegistersReportingServiceFromWorkspaceDefaults(t *testing.T) {
	prevRoot := workspace.Root()
	root := t.TempDir()
	workspace.SetRoot(root)
	t.Cleanup(func() {
		workspace.SetRoot(prevRoot)
	})
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
default:
  reporting:
    enabled: true
`), 0o644))

	cfg, err := wsconfig.Load(root)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	defaults := cfg.DefaultsWithFallback(&execconfig.Defaults{
		Model:    "test-model",
		Embedder: "test-embedder",
		Agent:    "coder",
	})
	require.True(t, defaults.Reporting.Enabled)

	rt, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithDefaults(defaults).
		Build(context.Background())
	require.NoError(t, err)
	require.NotNil(t, rt.Reporting)

	defs := rt.Registry.MatchDefinition("reporting:*")
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		if def == nil {
			continue
		}
		names = append(names, def.Name)
	}
	require.Contains(t, names, "reporting/compile")
	require.Contains(t, names, "reporting/export_report")
	require.Contains(t, names, "reporting/submit_export")
	require.Contains(t, names, "reporting/list_export_jobs")
	require.Contains(t, names, "reporting/list_export_artifacts")
	require.Contains(t, names, "reporting/save_report")
	require.Contains(t, names, "reporting/get_report")
	require.Contains(t, names, "reporting/list_reports")
	require.Contains(t, names, "reporting/update_report")
}

func TestBuilderBuild_ReportingServiceFromDefaultsPersistsAcrossRuntimeRebuild(t *testing.T) {
	prevRoot := workspace.Root()
	root := t.TempDir()
	workspace.SetRoot(root)
	t.Cleanup(func() {
		workspace.SetRoot(prevRoot)
	})
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
default:
  reporting:
    enabled: true
`), 0o644))

	cfg, err := wsconfig.Load(root)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	defaults := cfg.DefaultsWithFallback(&execconfig.Defaults{
		Model:    "test-model",
		Embedder: "test-embedder",
		Agent:    "coder",
	})
	require.True(t, defaults.Reporting.Enabled)

	first, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithDefaults(defaults).
		Build(context.Background())
	require.NoError(t, err)
	require.NotNil(t, first.Reporting)

	ctx := authsvc.InjectUser(context.Background(), "persist-user")
	submitRaw, err := first.Registry.Execute(ctx, "reporting:submit_export", map[string]interface{}{
		"artifactRef": "report://draft/persisted",
		"format":      "pdf",
		"scope":       "draft",
		"reportPrint": validReportingIntegrationPrintPayload(),
	})
	require.NoError(t, err)
	var queued reportingsvc.ExportJob
	require.NoError(t, json.Unmarshal([]byte(submitRaw), &queued))
	require.Equal(t, reportingsvc.JobStatusQueued, queued.Status)

	second, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithDefaults(defaults).
		Build(context.Background())
	require.NoError(t, err)
	require.NotNil(t, second.Reporting)

	statusRaw, err := second.Registry.Execute(ctx, "reporting:get_export_status", map[string]interface{}{
		"jobId": queued.JobID,
	})
	require.NoError(t, err)
	var status reportingsvc.ExportJob
	require.NoError(t, json.Unmarshal([]byte(statusRaw), &status))
	require.Equal(t, queued.JobID, status.JobID)
	require.Equal(t, "persist-user", status.OwnerID)
	require.Equal(t, reportingsvc.JobStatusQueued, status.Status)
}

func TestBuilderBuild_ReportingRegistryRoundTrip(t *testing.T) {
	reportingSvc := reportingsvc.New(reportingsvc.Options{
		Store: reportingsvc.NewStoreAdapter(reportmemory.New()),
	})
	rt, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithReportingService(reportingSvc).
		Build(context.Background())
	require.NoError(t, err)
	require.NotNil(t, rt.Reporting)
	require.Same(t, reportingSvc, rt.Reporting)

	ctx := authsvc.InjectUser(context.Background(), "builder-user")
	submitRaw, err := rt.Registry.Execute(ctx, "reporting:submit_export", map[string]interface{}{
		"artifactRef": "report://draft/performance",
		"format":      "pdf",
		"scope":       "draft",
		"reportPrint": validReportingIntegrationPrintPayload(),
	})
	require.NoError(t, err)

	var job reportingsvc.ExportJob
	require.NoError(t, json.Unmarshal([]byte(submitRaw), &job))
	require.Equal(t, reportingsvc.JobStatusQueued, job.Status)
	require.Equal(t, "builder-user", job.OwnerID)

	statusRaw, err := rt.Registry.Execute(ctx, "reporting:get_export_status", map[string]interface{}{
		"jobId": job.JobID,
	})
	require.NoError(t, err)
	var status reportingsvc.ExportJob
	require.NoError(t, json.Unmarshal([]byte(statusRaw), &status))
	require.Equal(t, job.JobID, status.JobID)
	require.Equal(t, reportingsvc.JobStatusQueued, status.Status)

	_, err = rt.Reporting.StartExport(context.Background(), job.JobID)
	require.NoError(t, err)

	completed, err := rt.Reporting.CompleteExport(context.Background(), &reportingsvc.CompleteExportRequest{
		JobID:       job.JobID,
		ContentType: "application/pdf",
		Data:        []byte("%PDF"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, completed.ArtifactID)

	artifactRaw, err := rt.Registry.Execute(ctx, "reporting:get_artifact", map[string]interface{}{
		"artifactId": completed.ArtifactID,
	})
	require.NoError(t, err)
	var artifact reportingsvc.Artifact
	require.NoError(t, json.Unmarshal([]byte(artifactRaw), &artifact))
	require.Equal(t, []byte("%PDF"), artifact.Data)
}

func TestBuilderBuild_ReportingRegistryAcceptsCanonicalExportEnvelope(t *testing.T) {
	reportingSvc := reportingsvc.New(reportingsvc.Options{
		Store: reportingsvc.NewStoreAdapter(reportmemory.New()),
	})
	rt, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithReportingService(reportingSvc).
		Build(context.Background())
	require.NoError(t, err)

	ctx := authsvc.InjectUser(context.Background(), "builder-user")
	submitRaw, err := rt.Registry.Execute(ctx, "reporting:submit_export", map[string]interface{}{
		"reportExportRequest": validReportingIntegrationExportEnvelopePayload(),
	})
	require.NoError(t, err)

	var job reportingsvc.ExportJob
	require.NoError(t, json.Unmarshal([]byte(submitRaw), &job))
	require.Equal(t, reportingsvc.JobStatusQueued, job.Status)
	require.Equal(t, reportingsvc.ExportScopeSavedPayload, job.Scope)
	require.Equal(t, reportingsvc.ExportFormatPDF, job.Format)
	require.Equal(t, "reportBuilder.savedReportPayload://rbreport_forecasting_q3", job.ArtifactRef)
}

func TestBuilderBuild_DefaultReportingServiceRunsForgePDFExport(t *testing.T) {
	prevRoot := workspace.Root()
	root := t.TempDir()
	workspace.SetRoot(root)
	t.Cleanup(func() {
		workspace.SetRoot(prevRoot)
	})

	rt, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithDefaults(&execconfig.Defaults{
			Reporting: execconfig.ReportingDefaults{Enabled: true},
		}).
		Build(context.Background())
	require.NoError(t, err)
	require.NotNil(t, rt.Reporting)

	ctx := authsvc.InjectUser(context.Background(), "builder-user")
	submitRaw, err := rt.Registry.Execute(ctx, "reporting:submit_export", map[string]interface{}{
		"artifactRef": "report://draft/default-pdf",
		"format":      "pdf",
		"scope":       "draft",
		"reportPrint": validReportingIntegrationPrintPayload(),
	})
	require.NoError(t, err)

	var queued reportingsvc.ExportJob
	require.NoError(t, json.Unmarshal([]byte(submitRaw), &queued))
	require.Equal(t, reportingsvc.JobStatusQueued, queued.Status)

	completed, err := rt.Reporting.RunExport(context.Background(), queued.JobID)
	require.NoError(t, err)
	require.Equal(t, reportingsvc.JobStatusSucceeded, completed.Status)
	require.NotEmpty(t, completed.ArtifactID)

	artifact, err := rt.Reporting.GetArtifact(ctx, completed.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, reportingsvc.ExportFormatPDF, artifact.Format)
	require.Equal(t, "application/pdf", artifact.ContentType)
	require.NotEmpty(t, artifact.Data)
	require.True(t, len(artifact.Data) > 100)
}

func TestBuilderBuild_DefaultReportingServiceRunsForgeCSVExport(t *testing.T) {
	rt, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithDefaults(&execconfig.Defaults{
			Reporting: execconfig.ReportingDefaults{Enabled: true},
		}).
		Build(context.Background())
	require.NoError(t, err)
	require.NotNil(t, rt.Reporting)

	ctx := authsvc.InjectUser(context.Background(), "builder-user")
	submitRaw, err := rt.Registry.Execute(ctx, "reporting:submit_export", map[string]interface{}{
		"artifactRef": "report://draft/default-csv",
		"format":      "csv",
		"scope":       "draft",
		"reportFill":  validReportingIntegrationRenderableFillPayload(),
	})
	require.NoError(t, err)

	var queued reportingsvc.ExportJob
	require.NoError(t, json.Unmarshal([]byte(submitRaw), &queued))
	require.Equal(t, reportingsvc.JobStatusQueued, queued.Status)

	completed, err := rt.Reporting.RunExport(context.Background(), queued.JobID)
	require.NoError(t, err)
	require.Equal(t, reportingsvc.JobStatusSucceeded, completed.Status)
	require.NotEmpty(t, completed.ArtifactID)

	artifact, err := rt.Reporting.GetArtifact(ctx, completed.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, reportingsvc.ExportFormatCSV, artifact.Format)
	require.Equal(t, "text/csv", artifact.ContentType)
	require.Equal(t, "Channel,Spend\nDisplay,$42.50\nCTV,$30.00\n", string(artifact.Data))
}

func TestBuilderBuild_DefaultReportingServiceRunsForgeXLSXExport(t *testing.T) {
	rt, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithDefaults(&execconfig.Defaults{
			Reporting: execconfig.ReportingDefaults{Enabled: true},
		}).
		Build(context.Background())
	require.NoError(t, err)
	require.NotNil(t, rt.Reporting)

	ctx := authsvc.InjectUser(context.Background(), "builder-user")
	submitRaw, err := rt.Registry.Execute(ctx, "reporting:submit_export", map[string]interface{}{
		"artifactRef": "report://draft/default-xlsx",
		"format":      "xlsx",
		"scope":       "draft",
		"reportFill":  validReportingIntegrationRenderableFillPayload(),
	})
	require.NoError(t, err)

	var queued reportingsvc.ExportJob
	require.NoError(t, json.Unmarshal([]byte(submitRaw), &queued))
	require.Equal(t, reportingsvc.JobStatusQueued, queued.Status)

	completed, err := rt.Reporting.RunExport(context.Background(), queued.JobID)
	require.NoError(t, err)
	require.Equal(t, reportingsvc.JobStatusSucceeded, completed.Status)
	require.NotEmpty(t, completed.ArtifactID)

	artifact, err := rt.Reporting.GetArtifact(ctx, completed.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, reportingsvc.ExportFormatXLSX, artifact.Format)
	require.Equal(t, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", artifact.ContentType)
	require.True(t, len(artifact.Data) > 100)
}

func TestBuilderBuild_DefaultReportingServiceWorkerProcessesQueuedExports(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithDefaults(&execconfig.Defaults{
			Reporting: execconfig.ReportingDefaults{
				Enabled:         true,
				QueueIntervalMs: 5,
				QueueBatchLimit: 1,
			},
		}).
		Build(ctx)
	require.NoError(t, err)
	require.NotNil(t, rt.Reporting)
	require.NotNil(t, rt.ReportingWorker)

	ownerCtx := authsvc.InjectUser(context.Background(), "builder-user")
	submitRaw, err := rt.Registry.Execute(ownerCtx, "reporting:submit_export", map[string]interface{}{
		"artifactRef": "report://draft/worker-pdf",
		"format":      "pdf",
		"scope":       "draft",
		"reportPrint": validReportingIntegrationPrintPayload(),
	})
	require.NoError(t, err)

	var queued reportingsvc.ExportJob
	require.NoError(t, json.Unmarshal([]byte(submitRaw), &queued))
	require.Equal(t, reportingsvc.JobStatusQueued, queued.Status)

	require.Eventually(t, func() bool {
		status, err := rt.Reporting.GetExportStatus(ownerCtx, queued.JobID)
		return err == nil && status != nil && status.Status == reportingsvc.JobStatusSucceeded
	}, time.Second, 10*time.Millisecond)
}

func TestBuilderBuild_DefaultReportingServiceWorkerUsesFallbackIntervalWhenEnabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithDefaults(&execconfig.Defaults{
			Reporting: execconfig.ReportingDefaults{
				Enabled: true,
			},
		}).
		Build(ctx)
	require.NoError(t, err)
	require.NotNil(t, rt.Reporting)
	require.NotNil(t, rt.ReportingWorker)

	ownerCtx := authsvc.InjectUser(context.Background(), "builder-user")
	submitRaw, err := rt.Registry.Execute(ownerCtx, "reporting:submit_export", map[string]interface{}{
		"artifactRef": "report://draft/worker-fallback-pdf",
		"format":      "pdf",
		"scope":       "draft",
		"reportPrint": validReportingIntegrationPrintPayload(),
	})
	require.NoError(t, err)

	var queued reportingsvc.ExportJob
	require.NoError(t, json.Unmarshal([]byte(submitRaw), &queued))
	require.Equal(t, reportingsvc.JobStatusQueued, queued.Status)

	require.Eventually(t, func() bool {
		status, err := rt.Reporting.GetExportStatus(ownerCtx, queued.JobID)
		return err == nil && status != nil && status.Status == reportingsvc.JobStatusSucceeded
	}, 2*time.Second, 20*time.Millisecond)
}

func TestBuilderBuild_DefaultReportingServiceWorkerPreservesOwnerVisibility(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithDefaults(&execconfig.Defaults{
			Reporting: execconfig.ReportingDefaults{
				Enabled:         true,
				QueueIntervalMs: 5,
				QueueBatchLimit: 2,
			},
		}).
		Build(ctx)
	require.NoError(t, err)
	require.NotNil(t, rt.ReportingWorker)

	firstCtx := authsvc.InjectUser(context.Background(), "builder-user-1")
	secondCtx := authsvc.InjectUser(context.Background(), "builder-user-2")

	firstRaw, err := rt.Registry.Execute(firstCtx, "reporting:submit_export", map[string]interface{}{
		"artifactRef": "report://draft/owner-one",
		"format":      "pdf",
		"scope":       "draft",
		"reportPrint": validReportingIntegrationPrintPayload(),
	})
	require.NoError(t, err)
	secondRaw, err := rt.Registry.Execute(secondCtx, "reporting:submit_export", map[string]interface{}{
		"artifactRef": "report://draft/owner-two",
		"format":      "pdf",
		"scope":       "draft",
		"reportPrint": validReportingIntegrationPrintPayload(),
	})
	require.NoError(t, err)

	var firstJob reportingsvc.ExportJob
	var secondJob reportingsvc.ExportJob
	require.NoError(t, json.Unmarshal([]byte(firstRaw), &firstJob))
	require.NoError(t, json.Unmarshal([]byte(secondRaw), &secondJob))

	require.Eventually(t, func() bool {
		firstStatus, firstErr := rt.Reporting.GetExportStatus(firstCtx, firstJob.JobID)
		secondStatus, secondErr := rt.Reporting.GetExportStatus(secondCtx, secondJob.JobID)
		return firstErr == nil && secondErr == nil &&
			firstStatus != nil && secondStatus != nil &&
			firstStatus.Status == reportingsvc.JobStatusSucceeded &&
			secondStatus.Status == reportingsvc.JobStatusSucceeded
	}, time.Second, 10*time.Millisecond)

	firstStatus, err := rt.Reporting.GetExportStatus(firstCtx, firstJob.JobID)
	require.NoError(t, err)
	secondStatus, err := rt.Reporting.GetExportStatus(secondCtx, secondJob.JobID)
	require.NoError(t, err)
	require.Equal(t, "builder-user-1", firstStatus.OwnerID)
	require.Equal(t, "builder-user-2", secondStatus.OwnerID)

	firstArtifact, err := rt.Reporting.GetArtifact(firstCtx, firstStatus.ArtifactID)
	require.NoError(t, err)
	secondArtifact, err := rt.Reporting.GetArtifact(secondCtx, secondStatus.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, "builder-user-1", firstArtifact.OwnerID)
	require.Equal(t, "builder-user-2", secondArtifact.OwnerID)

	_, err = rt.Reporting.GetExportStatus(firstCtx, secondJob.JobID)
	require.ErrorIs(t, err, reportingsvc.ErrNotFound)
	_, err = rt.Reporting.GetExportStatus(secondCtx, firstJob.JobID)
	require.ErrorIs(t, err, reportingsvc.ErrNotFound)
	_, err = rt.Reporting.GetArtifact(firstCtx, secondStatus.ArtifactID)
	require.ErrorIs(t, err, reportingsvc.ErrNotFound)
	_, err = rt.Reporting.GetArtifact(secondCtx, firstStatus.ArtifactID)
	require.ErrorIs(t, err, reportingsvc.ErrNotFound)
}

func validReportingIntegrationPrintPayload() map[string]interface{} {
	return map[string]interface{}{
		"version":     1,
		"kind":        "reportPrint",
		"specVersion": 1,
		"specHash":    "spec-1",
		"fillVersion": 1,
		"fillHash":    "fill-1",
		"source": map[string]interface{}{
			"kind":          "dashboard.reportBuilder",
			"containerId":   "demo",
			"stateKey":      "demo",
			"dataSourceRef": "demo",
		},
		"title": "Demo Report",
		"pageGeometry": map[string]interface{}{
			"width":        612,
			"height":       792,
			"marginTop":    48,
			"marginRight":  48,
			"marginBottom": 48,
			"marginLeft":   48,
			"headerHeight": 24,
			"footerHeight": 24,
		},
		"pages": []map[string]interface{}{
			{
				"number": 1,
				"elements": []map[string]interface{}{
					{
						"id":   "body-1",
						"kind": "text",
						"text": "Demo body",
						"box": map[string]interface{}{
							"x":      48,
							"y":      96,
							"width":  200,
							"height": 18,
						},
					},
				},
				"headerElements": []interface{}{},
				"footerElements": []interface{}{},
			},
		},
		"bookmarks": []map[string]interface{}{
			{
				"id":         "section-1",
				"title":      "Section 1",
				"pageNumber": 1,
			},
		},
		"diagnostics": []interface{}{},
	}
}

func validReportingIntegrationSpecPayload() map[string]interface{} {
	return map[string]interface{}{
		"version": 1,
		"kind":    "reportSpec",
		"source": map[string]interface{}{
			"kind":          "dashboard.reportBuilder",
			"containerId":   "demo",
			"stateKey":      "demo",
			"dataSourceRef": "demo",
		},
		"title": "Demo Report",
		"parameters": map[string]interface{}{
			"viewMode":   "table",
			"groupBy":    "",
			"pageSize":   25,
			"orderField": "",
			"orderDir":   "asc",
		},
		"layoutIntent": map[string]interface{}{
			"kind":               "single",
			"resultPanePosition": "left",
			"blockOrder":         []interface{}{"primaryTable"},
		},
		"refinements":      []interface{}{},
		"calculatedFields": []interface{}{},
		"datasets": []map[string]interface{}{
			{"id": "primary", "dataSourceRef": "demo", "request": map[string]interface{}{}},
		},
		"blocks": []map[string]interface{}{
			{"id": "primaryTable", "kind": "tableBlock", "datasetRef": "primary", "columns": []interface{}{}},
		},
	}
}

func validReportingIntegrationFillPayload() map[string]interface{} {
	return map[string]interface{}{
		"version":     1,
		"kind":        "reportFill",
		"specVersion": 1,
		"specHash":    "spec-1",
		"source": map[string]interface{}{
			"kind":          "dashboard.reportBuilder",
			"containerId":   "demo",
			"stateKey":      "demo",
			"dataSourceRef": "demo",
		},
		"parameters": map[string]interface{}{
			"viewMode":   "table",
			"groupBy":    "",
			"pageSize":   25,
			"orderField": "",
			"orderDir":   "asc",
		},
		"refinements":      []interface{}{},
		"calculatedFields": []interface{}{},
		"datasets": []map[string]interface{}{
			{
				"id":            "primary",
				"dataSourceRef": "demo",
				"request":       map[string]interface{}{"limit": 25, "offset": 0},
				"provenance": map[string]interface{}{
					"requestHash": "fnv1a:9702fdec",
					"rowCount":    1,
					"truncated":   false,
					"hasMore":     false,
					"diagnostics": []interface{}{},
				},
				"rows": []map[string]interface{}{{"channel": "Display"}},
			},
		},
		"blocks": []map[string]interface{}{
			{"id": "primaryTable", "kind": "tableBlock", "datasetRef": "primary", "columns": []interface{}{}, "content": map[string]interface{}{"columns": []interface{}{}, "rowCount": 1, "resolvedRows": []interface{}{}}},
		},
		"diagnostics": []interface{}{},
	}
}

func validReportingIntegrationRenderableFillPayload() map[string]interface{} {
	return map[string]interface{}{
		"version":     1,
		"kind":        "reportFill",
		"specVersion": 1,
		"specHash":    "spec-1",
		"source": map[string]interface{}{
			"kind":          "dashboard.reportBuilder",
			"containerId":   "demo",
			"stateKey":      "demo",
			"dataSourceRef": "demo",
		},
		"parameters": map[string]interface{}{
			"viewMode":   "table",
			"groupBy":    "",
			"pageSize":   25,
			"orderField": "",
			"orderDir":   "asc",
		},
		"refinements":      []interface{}{},
		"calculatedFields": []interface{}{},
		"datasets": []map[string]interface{}{
			{
				"id":            "primary",
				"dataSourceRef": "demo",
				"request":       map[string]interface{}{"limit": 25, "offset": 0},
				"provenance": map[string]interface{}{
					"requestHash": "fnv1a:9702fdec",
					"rowCount":    2,
					"truncated":   false,
					"hasMore":     false,
					"diagnostics": []interface{}{},
				},
				"rows": []map[string]interface{}{
					{"channel": "Display", "spend": 42.5},
					{"channel": "CTV", "spend": 30},
				},
			},
		},
		"blocks": []map[string]interface{}{
			{
				"id":         "primaryTable",
				"kind":       "tableBlock",
				"datasetRef": "primary",
				"columns": []map[string]interface{}{
					{"key": "channel", "label": "Channel"},
					{"key": "spend", "label": "Spend", "format": "currency"},
				},
				"content": map[string]interface{}{
					"columns": []map[string]interface{}{
						{"key": "channel", "label": "Channel"},
						{"key": "spend", "label": "Spend", "format": "currency"},
					},
					"rowCount": 2,
					"resolvedRows": []map[string]interface{}{
						{
							"rowIndex": 0,
							"cells": []map[string]interface{}{
								{"key": "channel", "sourceKey": "channel", "displayKey": "channel", "value": "Display", "displayValue": "Display", "visualState": nil},
								{"key": "spend", "sourceKey": "spend", "displayKey": "spend", "value": 42.5, "displayValue": "$42.50", "visualState": nil},
							},
						},
						{
							"rowIndex": 1,
							"cells": []map[string]interface{}{
								{"key": "channel", "sourceKey": "channel", "displayKey": "channel", "value": "CTV", "displayValue": "CTV", "visualState": nil},
								{"key": "spend", "sourceKey": "spend", "displayKey": "spend", "value": 30, "displayValue": "$30.00", "visualState": nil},
							},
						},
					},
				},
			},
		},
		"diagnostics": []interface{}{},
	}
}

func validReportingIntegrationExportEnvelopePayload() map[string]interface{} {
	return map[string]interface{}{
		"version": 1,
		"kind":    "reportExportRequest",
		"target": map[string]interface{}{
			"format": "pdf",
		},
		"source": map[string]interface{}{
			"from":             "savedPayload",
			"artifactKind":     "reportBuilder.savedReportPayload",
			"artifactRef":      "reportBuilder.savedReportPayload://rbreport_forecasting_q3",
			"title":            "Forecasting Q3",
			"reportId":         "forecastingQ3",
			"payloadId":        "rbreport_forecasting_q3",
			"sourceArtifactId": "forecasting_q3",
			"documentVersion":  4,
		},
		"reportSpec":  validReportingIntegrationSpecPayload(),
		"reportFill":  validReportingIntegrationFillPayload(),
		"reportPrint": validReportingIntegrationPrintPayload(),
	}
}
