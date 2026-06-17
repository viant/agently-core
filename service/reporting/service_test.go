package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	authsvc "github.com/viant/agently-core/service/auth"
)

type compileRecorder struct {
	request *CompileRequest
	result  *CompileResult
	err     error
}

func (c *compileRecorder) Compile(_ context.Context, request *CompileRequest) (*CompileResult, error) {
	c.request = cloneCompileRequest(request)
	if c.err != nil {
		return nil, c.err
	}
	return cloneCompileResult(c.result), nil
}

type exportRecorder struct {
	request *RenderRequest
	result  *RenderResult
	err     error
}

func (r *exportRecorder) Export(_ context.Context, request *RenderRequest) (*RenderResult, error) {
	if request != nil {
		r.request = &RenderRequest{
			JobID:       request.JobID,
			ArtifactRef: request.ArtifactRef,
			OwnerID:     request.OwnerID,
			Format:      request.Format,
			Scope:       request.Scope,
			ReportSpec:  cloneJSON(request.ReportSpec),
			ReportFill:  cloneJSON(request.ReportFill),
			ReportPrint: cloneJSON(request.ReportPrint),
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.result == nil {
		return nil, nil
	}
	return &RenderResult{
		ContentType:  r.result.ContentType,
		Data:         append([]byte{}, r.result.Data...),
		Diagnostics:  cloneDiagnostics(r.result.Diagnostics),
		RetentionTTL: r.result.RetentionTTL,
	}, nil
}

type auditRecorder struct {
	events []*AuditEvent
}

func (r *auditRecorder) Record(_ context.Context, event *AuditEvent) error {
	r.events = append(r.events, cloneAuditEvent(event))
	return nil
}

func TestServiceCompileDelegatesAndAudits(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	compiler := &compileRecorder{
		result: &CompileResult{
			ArtifactRef: "report://draft/performance",
			ReportSpec:  json.RawMessage(`{"kind":"reportSpec","version":1}`),
			Diagnostics: []Diagnostic{{Code: "previewOnly", Severity: "warning", Message: "preview compile"}},
		},
	}
	audit := &auditRecorder{}
	svc := New(Options{
		Compiler: compiler,
		Store:    NewStoreAdapter(reportmemory.New()),
		Audit:    audit,
		Now:      func() time.Time { return now },
		NewID:    func() string { return "job-fixed" },
	})
	ctx := authsvc.InjectUser(context.Background(), "user-123")

	result, err := svc.Compile(ctx, &CompileRequest{
		ArtifactRef: "report://draft/performance",
		SourceKind:  "draft",
		Document:    json.RawMessage(`{"kind":"reportDocument","id":"performance"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "report://draft/performance", result.ArtifactRef)
	require.JSONEq(t, `{"kind":"reportDocument","id":"performance"}`, string(compiler.request.Document))
	require.Equal(t, now, result.CompiledAt)
	require.Len(t, audit.events, 1)
	require.Equal(t, "report.compile", audit.events[0].EventType)
	require.Equal(t, "user-123", audit.events[0].ActorID)
}

func TestServiceExportLifecycleScopesArtifactsToOwner(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 30, 0, 0, time.UTC)
	audit := &auditRecorder{}
	svc := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Audit: audit,
		Now:   func() time.Time { return now },
		NewID: func() string {
			if len(audit.events) == 0 {
				return "job-1"
			}
			return "artifact-1"
		},
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	job, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validTestReportPrintJSON()),
	})
	require.NoError(t, err)
	require.Equal(t, JobStatusQueued, job.Status)
	require.Equal(t, "owner-1", job.OwnerID)

	running, err := svc.StartExport(context.Background(), job.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusRunning, running.Status)
	require.NotNil(t, running.StartedAt)

	completed, err := svc.CompleteExport(context.Background(), &CompleteExportRequest{
		JobID:       job.JobID,
		Data:        []byte("%PDF-1.7"),
		Diagnostics: []Diagnostic{{Code: "rendered", Severity: "info", Message: "pdf rendered"}},
	})
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, completed.Status)
	require.Equal(t, "artifact-1", completed.ArtifactID)

	gotStatus, err := svc.GetExportStatus(ownerCtx, job.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, gotStatus.Status)

	artifact, err := svc.GetArtifact(ownerCtx, completed.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, ExportFormatPDF, artifact.Format)
	require.Equal(t, "application/pdf", artifact.ContentType)
	require.Equal(t, []byte("%PDF-1.7"), artifact.Data)

	otherCtx := authsvc.InjectUser(context.Background(), "owner-2")
	_, err = svc.GetExportStatus(otherCtx, job.JobID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = svc.GetArtifact(otherCtx, completed.ArtifactID)
	require.ErrorIs(t, err, ErrNotFound)

	require.Len(t, audit.events, 2)
	require.Equal(t, "report.export.submit", audit.events[0].EventType)
	require.Equal(t, "report.export.complete", audit.events[1].EventType)
}

func TestServiceSubmitExportAcceptsCanonicalEnvelope(t *testing.T) {
	svc := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Now:   func() time.Time { return time.Unix(0, 0).UTC() },
		NewID: func() string { return "job-envelope" },
	})
	ctx := authsvc.InjectUser(context.Background(), "user-1")

	job, err := svc.SubmitExport(ctx, &SubmitExportRequest{
		ReportExportRequest: validTestReportExportRequestEnvelope(),
	})
	require.NoError(t, err)
	require.Equal(t, "job-envelope", job.JobID)
	require.Equal(t, "reportBuilder.savedReportPayload://rbreport_forecasting_q3", job.ArtifactRef)
	require.Equal(t, ExportFormatPDF, job.Format)
	require.Equal(t, ExportScopeSavedPayload, job.Scope)
	require.JSONEq(t, validTestReportSpecJSON(), string(job.ReportSpec))
	require.JSONEq(t, validTestReportFillJSON(), string(job.ReportFill))
	require.JSONEq(t, validTestReportPrintJSON(), string(job.ReportPrint))
}

func TestServiceValidatesExportArtifactsByFormat(t *testing.T) {
	svc := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Now:   func() time.Time { return time.Unix(0, 0).UTC() },
		NewID: func() string { return "job-1" },
	})
	ctx := authsvc.InjectUser(context.Background(), "user-1")

	_, err := svc.SubmitExport(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
	})
	require.EqualError(t, err, "reporting export: reportPrint is required for pdf export")

	_, err = svc.SubmitExport(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatCSV,
		Scope:       ExportScopeDraft,
	})
	require.EqualError(t, err, "reporting export: reportFill is required for tabular export")

	_, err = svc.SubmitExport(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(`{"version":1,"kind":"reportPrint"}`),
	})
	require.EqualError(t, err, "reporting export: invalid reportPrint: missing specVersion")

	_, err = svc.SubmitExport(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatCSV,
		Scope:       ExportScopeDraft,
		ReportFill:  json.RawMessage(`{"version":1,"kind":"reportFill"}`),
	})
	require.EqualError(t, err, "reporting export: invalid reportFill: missing specVersion")

	_, err = svc.SubmitExport(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatCSV,
		Scope:       ExportScopeDraft,
		ReportSpec:  json.RawMessage(validTestReportSpecJSON()),
		ReportFill:  json.RawMessage(validTestReportFillJSONWithSpecVersion(2)),
	})
	require.EqualError(t, err, "reporting export: reportFill specVersion 2 does not match reportSpec version 1")

	_, err = svc.SubmitExport(ctx, &SubmitExportRequest{
		ReportExportRequest: &ReportExportRequest{
			Version: 1,
			Kind:    "reportExportRequest",
			Target: ReportExportTarget{
				Format: ExportFormatPDF,
			},
			Source: ReportExportSource{
				From:        "savedPayload",
				ArtifactRef: "reportBuilder.savedReportPayload://rbreport_forecasting_q3",
				Title:       "Forecasting Q3",
			},
			ReportSpec:  json.RawMessage(validTestReportSpecJSON()),
			ReportFill:  json.RawMessage(validTestReportFillJSON()),
			ReportPrint: json.RawMessage(validTestReportPrintJSON()),
		},
	})
	require.EqualError(t, err, "reporting export: reportExportRequest source.payloadId is required for savedPayload exports")
}

func TestServiceRunExportUsesConfiguredExporter(t *testing.T) {
	now := time.Date(2026, 6, 13, 15, 0, 0, 0, time.UTC)
	exporter := &exportRecorder{
		result: &RenderResult{
			Data:         []byte("%PDF-runtime"),
			Diagnostics:  []Diagnostic{{Code: "rendered", Severity: "info", Message: "rendered successfully"}},
			RetentionTTL: 2 * time.Hour,
		},
	}
	idCounter := 0
	svc := New(Options{
		Exporter: exporter,
		Store:    NewStoreAdapter(reportmemory.New()),
		Now:      func() time.Time { return now },
		NewID: func() string {
			idCounter++
			if idCounter == 1 {
				return "job-1"
			}
			return "artifact-1"
		},
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	job, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportSpec:  json.RawMessage(validTestReportSpecJSON()),
		ReportFill:  json.RawMessage(validTestReportFillJSON()),
		ReportPrint: json.RawMessage(validTestReportPrintJSON()),
	})
	require.NoError(t, err)

	completed, err := svc.RunExport(context.Background(), job.JobID)
	require.NoError(t, err)
	require.NotNil(t, completed)
	require.Equal(t, JobStatusSucceeded, completed.Status)
	require.Equal(t, "artifact-1", completed.ArtifactID)
	require.NotNil(t, exporter.request)
	require.Equal(t, ExportFormatPDF, exporter.request.Format)
	require.Equal(t, ExportScopeDraft, exporter.request.Scope)
	require.Equal(t, "owner-1", exporter.request.OwnerID)
	require.JSONEq(t, validTestReportPrintJSON(), string(exporter.request.ReportPrint))
	require.JSONEq(t, validTestReportFillJSON(), string(exporter.request.ReportFill))
	require.JSONEq(t, validTestReportSpecJSON(), string(exporter.request.ReportSpec))

	artifact, err := svc.GetArtifact(ownerCtx, completed.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, []byte("%PDF-runtime"), artifact.Data)
	require.Equal(t, "application/pdf", artifact.ContentType)
	require.Equal(t, 2*time.Hour, artifact.RetentionTTL)
}

func TestServiceRunExportMarksJobFailedOnExporterError(t *testing.T) {
	svc := New(Options{
		Exporter: &exportRecorder{err: errors.New("render failed")},
		Store:    NewStoreAdapter(reportmemory.New()),
		Now:      func() time.Time { return time.Date(2026, 6, 13, 15, 30, 0, 0, time.UTC) },
		NewID:    func() string { return "job-1" },
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	job, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validTestReportPrintJSON()),
	})
	require.NoError(t, err)

	failed, err := svc.RunExport(context.Background(), job.JobID)
	require.EqualError(t, err, "render failed")
	require.NotNil(t, failed)
	require.Equal(t, JobStatusFailed, failed.Status)
	require.Equal(t, "render failed", failed.Error)

	status, statusErr := svc.GetExportStatus(ownerCtx, job.JobID)
	require.NoError(t, statusErr)
	require.Equal(t, JobStatusFailed, status.Status)
	require.Equal(t, "render failed", status.Error)
}

func TestReportSpecCompiler_CompileCanonicalSpec(t *testing.T) {
	now := time.Date(2026, 6, 13, 14, 0, 0, 0, time.UTC)
	compiler := NewReportSpecCompiler(func() time.Time { return now })
	result, err := compiler.Compile(context.Background(), &CompileRequest{
		ArtifactRef: "report://draft/performance",
		SourceKind:  SourceKindReportSpec,
		Document: json.RawMessage(`{
			"version": 1,
			"kind": "reportSpec",
			"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
			"title": "Demo Report",
			"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
			"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
			"refinements": [],
			"calculatedFields": [],
			"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
			"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
		}`),
	})
	require.NoError(t, err)
	require.Equal(t, "report://draft/performance", result.ArtifactRef)
	require.JSONEq(t, `{
			"version": 1,
			"kind": "reportSpec",
			"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
			"title": "Demo Report",
			"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
			"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
			"refinements": [],
			"calculatedFields": [],
			"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
			"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
		}`, string(result.ReportSpec))
	require.Equal(t, now, result.CompiledAt)
}

func TestReportSpecCompiler_RejectsInvalidInputs(t *testing.T) {
	compiler := NewReportSpecCompiler(nil)
	_, err := compiler.Compile(context.Background(), &CompileRequest{
		SourceKind: "reportDocument",
		Document:   json.RawMessage(`{"kind":"reportDocument"}`),
	})
	require.EqualError(t, err, `invalid reporting compile source kind "reportDocument": only reportSpec is supported`)

	_, err = compiler.Compile(context.Background(), &CompileRequest{
		SourceKind: SourceKindReportSpec,
		Document:   json.RawMessage(`{"version":1,"kind":"reportSpec"}`),
	})
	require.ErrorContains(t, err, "invalid reportSpec: missing source")

	for _, testCase := range []struct {
		name    string
		doc     string
		wantErr string
	}{
		{
			name: "null source",
			doc: `{
				"version": 1,
				"kind": "reportSpec",
				"source": null,
				"title": "Demo Report",
				"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
				"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
				"refinements": [],
				"calculatedFields": [],
				"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
				"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
			}`,
			wantErr: "invalid reportSpec: source must be an object",
		},
		{
			name: "numeric title",
			doc: `{
				"version": 1,
				"kind": "reportSpec",
				"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
				"title": 42,
				"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
				"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
				"refinements": [],
				"calculatedFields": [],
				"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
				"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
			}`,
			wantErr: "invalid reportSpec: title must be a string",
		},
		{
			name: "null title",
			doc: `{
				"version": 1,
				"kind": "reportSpec",
				"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
				"title": null,
				"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
				"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
				"refinements": [],
				"calculatedFields": [],
				"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
				"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
			}`,
			wantErr: "invalid reportSpec: title must be a string",
		},
		{
			name: "empty title",
			doc: `{
				"version": 1,
				"kind": "reportSpec",
				"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
				"title": "",
				"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
				"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
				"refinements": [],
				"calculatedFields": [],
				"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
				"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
			}`,
			wantErr: "invalid reportSpec: title must be a non-empty string",
		},
		{
			name: "null refinements",
			doc: `{
				"version": 1,
				"kind": "reportSpec",
				"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
				"title": "Demo Report",
				"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
				"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
				"refinements": null,
				"calculatedFields": [],
				"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
				"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
			}`,
			wantErr: "invalid reportSpec: refinements must be an array",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := compiler.Compile(context.Background(), &CompileRequest{
				SourceKind: SourceKindReportSpec,
				Document:   json.RawMessage(testCase.doc),
			})
			require.EqualError(t, err, testCase.wantErr)
		})
	}
}

func validTestReportSpecJSON() string {
	return `{
		"version": 1,
		"kind": "reportSpec",
		"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
		"title": "Demo Report",
		"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
		"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
		"refinements": [],
		"calculatedFields": [],
		"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
		"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
	}`
}

func validTestReportFillJSON() string {
	return validTestReportFillJSONWithSpecVersion(1)
}

func validTestReportFillJSONWithSpecVersion(specVersion int) string {
	return `{
		"version": 1,
		"kind": "reportFill",
		"specVersion": ` + jsonNumber(specVersion) + `,
		"specHash": "spec-1",
		"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
		"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
		"refinements": [],
		"calculatedFields": [],
		"datasets": [{
			"id": "primary",
			"dataSourceRef": "demo",
			"request": {"limit": 25, "offset": 0},
			"provenance": {"requestHash":"request-1","rowCount":1,"truncated":false,"hasMore":false,"diagnostics":[]},
			"rows": [{"channel":"Display"}]
		}],
		"blocks": [{"id":"primaryTable","kind":"tableBlock"}],
		"diagnostics": []
	}`
}

func validTestReportPrintJSON() string {
	return `{
		"version": 1,
		"kind": "reportPrint",
		"specVersion": 1,
		"specHash": "spec-1",
		"fillVersion": 1,
		"fillHash": "fill-1",
		"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
		"title": "Demo Report",
		"pageGeometry": {
			"width": 612,
			"height": 792,
			"marginTop": 48,
			"marginRight": 48,
			"marginBottom": 48,
			"marginLeft": 48,
			"headerHeight": 24,
			"footerHeight": 24
		},
		"pages": [{
			"number": 1,
			"elements": [{
				"id": "body-1",
				"kind": "text",
				"box": {"x": 48, "y": 96, "width": 200, "height": 18}
			}],
			"headerElements": [],
			"footerElements": []
		}],
		"bookmarks": [{"id":"section-1","title":"Section 1","pageNumber":1}],
		"diagnostics": []
	}`
}

func validTestReportExportRequestEnvelope() *ReportExportRequest {
	return &ReportExportRequest{
		Version: 1,
		Kind:    "reportExportRequest",
		Target: ReportExportTarget{
			Format: ExportFormatPDF,
		},
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
	}
}

func jsonNumber(value int) string {
	return strconv.Itoa(value)
}
