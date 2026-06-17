package executor_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
	require.Contains(t, names, "reporting/submit_export")
	require.Contains(t, names, "reporting/get_export_status")
	require.Contains(t, names, "reporting/get_artifact")
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
	require.Contains(t, names, "reporting/submit_export")
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
	require.Contains(t, names, "reporting/submit_export")
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
					"requestHash": "request-1",
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
