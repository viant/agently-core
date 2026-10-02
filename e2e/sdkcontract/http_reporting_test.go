package sdkcontract_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/e2e/sdkcontract"
	auditread "github.com/viant/agently-core/internal/datly/reporting/audit/read"
	exportrequestmodel "github.com/viant/agently-core/model/exportrequest"
	reportrunmodel "github.com/viant/agently-core/model/reportrun"
	auth "github.com/viant/agently-core/service/auth"
	reporting "github.com/viant/agently-core/service/reporting"
	reportingrun "github.com/viant/agently-core/service/reportingrun"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

func TestActualApplicationReportingAdoptionExportAndAudit(t *testing.T) {
	prior := workspace.Root()
	t.Cleanup(func() { workspace.SetRoot(prior) })
	for _, key := range []string{"AGENTLY_WORKSPACE", "AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	fixture, err := sdkcontract.NewReporting(ctx, root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fixture.Runtime.Close(context.Background())) })
	require.NotNil(t, fixture.Runtime.ReportRuns)
	server := httptest.NewServer(fixture.Handler)
	defer server.Close()
	request := func(method, path, token string, body any, expected int, target any, headers map[string]string) {
		t.Helper()
		var payload []byte
		if body != nil {
			payload, err = json.Marshal(body)
			require.NoError(t, err)
		}
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(payload))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		response, err := server.Client().Do(req)
		require.NoError(t, err)
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, expected, response.StatusCode, "%s %s: %s", method, path, data)
		if target != nil {
			require.NoError(t, json.Unmarshal(data, target), "%s", data)
		}
	}
	tool := func(name, token string, args any, result any, headers map[string]string, expected int) {
		t.Helper()
		var envelope struct {
			Result string `json:"result"`
		}
		var target any
		if expected == 200 {
			target = &envelope
		}
		request("POST", "/v1/tools/"+name+"/execute?conversationId="+sdkcontract.ConversationID, token, args, expected, target, headers)
		if result != nil && expected == 200 {
			require.NoError(t, json.Unmarshal([]byte(envelope.Result), result), envelope.Result)
		}
	}
	begin := reportingrun.BeginInput{Origin: "manual", UIRunRequestID: "http-manual-report"}
	request("POST", "/v1/api/report-runs/begin", "", begin, 401, nil, nil)
	var begun, retried reportingrun.BeginResult
	request("POST", "/v1/api/report-runs/begin", fixture.Token, begin, 200, &begun, nil)
	require.Equal(t, int64(1), begun.Run.Revision)
	request("POST", "/v1/api/report-runs/begin", fixture.Token, begin, 200, &retried, nil)
	require.Equal(t, begun.Run.ReportRunID, retried.Run.ReportRunID)
	path := "/v1/api/report-runs/" + begun.Run.ReportRunID
	complete := reportingrun.CompleteInput{ExpectedRevision: 0, ReportSpec: json.RawMessage(routeReportSpec), ReportFill: json.RawMessage(routeReportFill), ReportPrint: json.RawMessage(routeReportPrint)}
	request("POST", path+"/complete", fixture.Token, complete, 409, nil, nil)
	complete.ExpectedRevision = 1
	var completed reportrunmodel.Record
	request("POST", path+"/complete", fixture.Token, complete, 200, &completed, nil)
	require.Equal(t, int64(2), completed.Revision)
	adoption := reportingrun.AdoptInput{ConversationID: sdkcontract.ConversationID, ExpectedRunRevision: 1, ExpectedContextRevision: 0, Source: "adopt"}
	request("POST", path+"/adopt", fixture.OtherToken, adoption, 404, nil, nil)
	request("POST", path+"/adopt", fixture.Token, adoption, 409, nil, nil)
	adoption.ExpectedRunRevision = 2
	var adopted, duplicate reportingrun.AdoptionResult
	request("POST", path+"/adopt", fixture.Token, adoption, 200, &adopted, nil)
	require.Equal(t, sdkcontract.ConversationID, adopted.Run.ConversationID)
	require.Equal(t, begun.Run.ReportRunID, adopted.Context.ActiveReportRunID)
	require.Equal(t, int64(1), adopted.Context.Revision)
	adoption.ExpectedRunRevision = adopted.Run.Revision
	request("POST", path+"/adopt", fixture.Token, adoption, 200, &duplicate, nil)
	require.Equal(t, adopted.Run.Revision, duplicate.Run.Revision)
	require.Equal(t, adopted.Context.Revision, duplicate.Context.Revision)
	var job, retry reporting.ExportJob
	headers := map[string]string{exportrequestmodel.Header: "http-export-operation"}
	args := map[string]any{"reportRunId": begun.Run.ReportRunID, "format": "pdf"}
	tool("reporting:submit_export", fixture.Token, args, &job, headers, 200)
	tool("reporting:submit_export", fixture.Token, args, &retry, headers, 200)
	require.Equal(t, job.JobID, retry.JobID)
	require.Equal(t, begun.Run.ReportRunID, job.ReportRunID)
	require.Equal(t, sdkcontract.Owner, job.OwnerID)
	var audit reporting.AuditEvent
	tool("reporting:record_audit_event", fixture.Token, map[string]any{"event": map[string]any{"eventType": "report.download", "artifactRef": job.ArtifactRef, "jobId": job.JobID, "metadata": map[string]any{"via": "actual-http"}}}, &audit, nil, 200)
	require.Equal(t, sdkcontract.Owner, audit.ActorID)
	ownerContext := auth.InjectUser(ctx, sdkcontract.Owner)
	_, err = fixture.Runtime.Reporting.StartExport(ownerContext, job.JobID)
	require.NoError(t, err)
	finished, err := fixture.Runtime.Reporting.CompleteExport(ownerContext, &reporting.CompleteExportRequest{JobID: job.JobID, ContentType: "application/pdf", Data: []byte("%PDF isolated application route fixture")})
	require.NoError(t, err)
	var status reporting.ExportJob
	tool("reporting:get_export_status", fixture.Token, map[string]any{"jobId": job.JobID}, &status, nil, 200)
	require.Equal(t, reporting.JobStatusSucceeded, status.Status)
	require.Equal(t, finished.ArtifactID, status.ArtifactID)
	var artifact reporting.Artifact
	tool("reporting:get_artifact", fixture.Token, map[string]any{"artifactId": finished.ArtifactID}, &artifact, nil, 200)
	require.Equal(t, finished.ArtifactID, artifact.ArtifactID)
	require.Equal(t, "application/pdf", artifact.ContentType)
	require.Empty(t, artifact.Data)
	tool("reporting:get_artifact", fixture.OtherToken, map[string]any{"artifactId": finished.ArtifactID}, nil, nil, 404)
	auditInput := &auditread.Input{}
	auditInput.SetJobID(job.JobID)
	result, err := fixture.Runtime.Native.InvokeComponent(ctx, dexec.ComponentRequest{
		Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[auditread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/audit"}},
		Input:  auditInput,
		Providers: []locator.Provider{provider.Named("reportauditaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "internal" {
				return true, true, nil
			}
			return nil, false, nil
		})},
	})
	require.NoError(t, err)
	persisted, ok := result.(*auditread.Output)
	require.True(t, ok)
	count := 0
	for _, event := range persisted.Data {
		if event.EventType == "report.download" {
			require.Equal(t, sdkcontract.Owner, event.ActorId)
			require.Contains(t, string(event.MetadataJson), "actual-http")
			count++
		}
	}
	require.Equal(t, 1, count)
}

const routeReportSpec = `{"version":1,"kind":"reportSpec","source":{"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},"title":"Demo Report","parameters":{"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},"layoutIntent":{"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},"refinements":[],"calculatedFields":[],"datasets":[{"id":"primary","dataSourceRef":"demo","request":{}}],"blocks":[{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]}`

const routeReportFill = `{"version":1,"kind":"reportFill","specVersion":1,"specHash":"spec-1","source":{"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},"parameters":{"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},"refinements":[],"calculatedFields":[],"datasets":[{"id":"primary","dataSourceRef":"demo","request":{"limit":25,"offset":0},"provenance":{"requestHash":"request-1","rowCount":1,"truncated":false,"hasMore":false,"diagnostics":[]},"rows":[{"channel":"Display"}]}],"blocks":[{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[],"content":{"columns":[],"rowCount":1,"resolvedRows":[]}}],"diagnostics":[]}`

const routeReportPrint = `{"version":1,"kind":"reportPrint","specVersion":1,"specHash":"spec-1","fillVersion":1,"fillHash":"fill-1","source":{"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},"title":"Demo Report","pageGeometry":{"width":612,"height":792,"marginTop":48,"marginRight":48,"marginBottom":48,"marginLeft":48,"headerHeight":24,"footerHeight":24},"pages":[{"number":1,"elements":[{"id":"body-1","kind":"text","box":{"x":48,"y":96,"width":200,"height":18}}],"headerElements":[],"footerElements":[]}],"bookmarks":[{"id":"section-1","title":"Section 1","pageNumber":1}],"diagnostics":[]}`
