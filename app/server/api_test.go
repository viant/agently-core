package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/viant/agently-core/app/executor"
	execconfig "github.com/viant/agently-core/app/executor/config"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	"github.com/viant/agently-core/genai/llm"
	agentproto "github.com/viant/agently-core/protocol/agent"
	uiresource "github.com/viant/agently-core/protocol/ui/resource"
	"github.com/viant/agently-core/sdk"
	svcauth "github.com/viant/agently-core/service/auth"
	reportingsvc "github.com/viant/agently-core/service/reporting"
	"github.com/viant/agently-core/workspace"
	wsconfig "github.com/viant/agently-core/workspace/config"
)

func TestRuntimeExecutorAdapter_ListResources_PublishesWorkspaceWindows(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "order.yaml"), `
id: order
title: Order Summary
windowKey: order
namespace: Order Summary
view:
  content:
    id: orderRoot
    containers:
      - id: summary
        title: Summary
`)
		adapter := &runtimeExecutorAdapter{}
		resources, err := adapter.ListResources(context.Background())
		if err != nil {
			t.Fatalf("ListResources failed: %v", err)
		}
		if len(resources) != 1 {
			t.Fatalf("expected exactly one workspace-backed resource, got %d (%#v)", len(resources), resources)
		}
		if resources[0].Uri != uiresource.WorkspaceViewURI("order") {
			t.Fatalf("expected workspace order resource, got %#v", resources[0])
		}
	})
}

func TestRuntimeExecutorAdapter_ListResources_EmptyWhenNoWorkspaceWindows(t *testing.T) {
	withWorkspaceRoot(t, func(_ string) {
		adapter := &runtimeExecutorAdapter{}
		resources, err := adapter.ListResources(context.Background())
		if err != nil {
			t.Fatalf("ListResources failed: %v", err)
		}
		if len(resources) != 0 {
			t.Fatalf("expected no resources when workspace declares no forge windows, got %#v", resources)
		}
	})
}

func TestRuntimeExecutorAdapter_ReadResource_ResolvesWorkspaceOrder(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "order.yaml"), `
id: order
title: Order Summary
windowKey: order
namespace: Order Summary
view:
  content:
    id: orderRoot
    containers:
      - id: summary
        title: Summary
`)
		adapter := &runtimeExecutorAdapter{}
		result, err := adapter.ReadResource(context.Background(), uiresource.WorkspaceViewURI("order"))
		if err != nil {
			t.Fatalf("ReadResource failed: %v", err)
		}
		if result == nil || len(result.Contents) != 1 {
			t.Fatalf("expected one contents entry, got %#v", result)
		}
		if !strings.Contains(result.Contents[0].Text, "Order Summary") {
			t.Fatalf("expected workspace title in HTML payload")
		}
	})
}

func TestRuntimeExecutorAdapter_ReadResource_UnknownURIWhenNoWorkspaceMatch(t *testing.T) {
	withWorkspaceRoot(t, func(_ string) {
		adapter := &runtimeExecutorAdapter{}
		if _, err := adapter.ReadResource(context.Background(), "ui://agently.wk_default/view/missing"); err == nil {
			t.Fatal("expected error for unknown workspace window")
		}
	})
}

type apiTestAgentFinder struct{}

func (apiTestAgentFinder) Find(context.Context, string) (*agentproto.Agent, error) {
	return &agentproto.Agent{}, nil
}

type apiTestModelFinder struct{}

func (apiTestModelFinder) Find(context.Context, string) (llm.Model, error) {
	return apiTestModel{}, nil
}

type apiTestModel struct{}

func (apiTestModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return &llm.GenerateResponse{}, nil
}

func (apiTestModel) Implements(string) bool { return false }

func TestNewAPIHandler_MetadataReportingCapabilityFollowsRuntimeRegistration(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "config.yaml"), `
default:
  reporting:
    enabled: true
`)
		cfg, err := wsconfig.Load(root)
		if err != nil {
			t.Fatalf("load workspace config: %v", err)
		}
		if cfg == nil {
			t.Fatal("expected workspace config")
		}
		defaults := cfg.DefaultsWithFallback(&execconfig.Defaults{
			Model:    "test-model",
			Embedder: "test-embedder",
			Agent:    "coder",
		})

		rt, err := executor.NewBuilder().
			WithAgentFinder(apiTestAgentFinder{}).
			WithModelFinder(apiTestModelFinder{}).
			WithDefaults(defaults).
			Build(context.Background())
		if err != nil {
			t.Fatalf("build runtime: %v", err)
		}
		if rt.Reporting == nil {
			t.Fatal("expected reporting service to be registered from workspace defaults")
		}

		client, closeClient, err := sdk.NewLocalHTTPFromRuntime(context.Background(), rt)
		if err != nil {
			t.Fatalf("new local http from runtime: %v", err)
		}
		t.Cleanup(closeClient)

		handler, err := NewAPIHandler(context.Background(), APIOptions{
			Version:     "test-version",
			Runtime:     rt,
			Client:      client,
			AgentFinder: apiTestAgentFinder{},
		})
		if err != nil {
			t.Fatalf("new api handler: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/v1/workspace/metadata", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}

		var payload struct {
			Capabilities struct {
				Reporting bool `json:"reporting"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !payload.Capabilities.Reporting {
			t.Fatalf("expected reporting capability to be true")
		}
	})
}

func TestNewAPIHandler_MetadataReportingCapabilityUsesRuntimeOverrideWhenDefaultsDisabled(t *testing.T) {
	withWorkspaceRoot(t, func(_ string) {
		reportingSvc := reportingsvc.New(reportingsvc.Options{
			Store: reportingsvc.NewStoreAdapter(reportmemory.New()),
		})
		rt, err := executor.NewBuilder().
			WithAgentFinder(apiTestAgentFinder{}).
			WithModelFinder(apiTestModelFinder{}).
			WithDefaults(&execconfig.Defaults{
				Model:    "test-model",
				Embedder: "test-embedder",
				Agent:    "coder",
				Reporting: execconfig.ReportingDefaults{
					Enabled: false,
				},
			}).
			WithReportingService(reportingSvc).
			Build(context.Background())
		if err != nil {
			t.Fatalf("build runtime: %v", err)
		}
		if rt.Reporting == nil {
			t.Fatal("expected explicitly injected reporting service")
		}

		client, closeClient, err := sdk.NewLocalHTTPFromRuntime(context.Background(), rt)
		if err != nil {
			t.Fatalf("new local http from runtime: %v", err)
		}
		t.Cleanup(closeClient)

		handler, err := NewAPIHandler(context.Background(), APIOptions{
			Version:     "test-version",
			Runtime:     rt,
			Client:      client,
			AgentFinder: apiTestAgentFinder{},
		})
		if err != nil {
			t.Fatalf("new api handler: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/v1/workspace/metadata", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}

		var payload struct {
			Capabilities struct {
				Reporting bool `json:"reporting"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !payload.Capabilities.Reporting {
			t.Fatalf("expected reporting capability override to be true when runtime reporting is registered")
		}
	})
}

func TestNewAPIHandler_ReportingToolRoutes_WorkspaceEnabled(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "config.yaml"), `
default:
  reporting:
    enabled: true
`)
		rt, client, finder, err := BuildWorkspaceRuntime(context.Background(), RuntimeOptions{
			WorkspaceRoot: root,
			Defaults: &execconfig.Defaults{
				Model:    "test-model",
				Embedder: "test-embedder",
				Agent:    "coder",
			},
		})
		if err != nil {
			t.Fatalf("BuildWorkspaceRuntime failed: %v", err)
		}
		if rt.Reporting == nil {
			t.Fatal("expected reporting runtime to be enabled from workspace defaults")
		}

		handler, err := NewAPIHandler(context.Background(), APIOptions{
			Version:     "test-version",
			Runtime:     rt,
			Client:      client,
			AgentFinder: finder,
		})
		if err != nil {
			t.Fatalf("NewAPIHandler failed: %v", err)
		}

		submitBody := []byte(`{"name":"reporting:submit_export","args":{"artifactRef":"report://draft/performance","format":"pdf","scope":"draft","reportPrint":` + validReportingAPIPrintJSON() + `}}`)
		submitReq := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(submitBody))
		submitReq = submitReq.WithContext(svcauth.InjectUser(submitReq.Context(), "api-user"))
		submitRec := httptest.NewRecorder()
		handler.ServeHTTP(submitRec, submitReq)
		if submitRec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", submitRec.Code, submitRec.Body.String())
		}

		var submitEnvelope struct {
			Result string `json:"result"`
		}
		if err := json.Unmarshal(submitRec.Body.Bytes(), &submitEnvelope); err != nil {
			t.Fatalf("decode submit envelope: %v", err)
		}
		var job struct {
			JobID    string `json:"jobId"`
			OwnerID  string `json:"ownerId"`
			Status   string `json:"status"`
			Artifact string `json:"artifactRef"`
		}
		if err := json.Unmarshal([]byte(submitEnvelope.Result), &job); err != nil {
			t.Fatalf("decode submit result: %v", err)
		}
		if job.JobID == "" {
			t.Fatalf("expected job id in submit result: %s", submitEnvelope.Result)
		}
		if job.OwnerID != "api-user" {
			t.Fatalf("expected owner api-user, got %q", job.OwnerID)
		}
		if job.Status != "queued" {
			t.Fatalf("expected queued status, got %q", job.Status)
		}

		statusBody := []byte(`{"name":"reporting:get_export_status","args":{"jobId":"` + job.JobID + `"}}`)
		statusReq := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(statusBody))
		statusReq = statusReq.WithContext(svcauth.InjectUser(statusReq.Context(), "api-user"))
		statusRec := httptest.NewRecorder()
		handler.ServeHTTP(statusRec, statusReq)
		if statusRec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", statusRec.Code, statusRec.Body.String())
		}

		var statusEnvelope struct {
			Result string `json:"result"`
		}
		if err := json.Unmarshal(statusRec.Body.Bytes(), &statusEnvelope); err != nil {
			t.Fatalf("decode status envelope: %v", err)
		}
		var status struct {
			JobID   string `json:"jobId"`
			OwnerID string `json:"ownerId"`
			Status  string `json:"status"`
		}
		if err := json.Unmarshal([]byte(statusEnvelope.Result), &status); err != nil {
			t.Fatalf("decode status result: %v", err)
		}
		if status.JobID != job.JobID {
			t.Fatalf("expected status job %q, got %q", job.JobID, status.JobID)
		}
		if status.OwnerID != "api-user" {
			t.Fatalf("expected owner api-user, got %q", status.OwnerID)
		}
		if status.Status != "queued" {
			t.Fatalf("expected queued status, got %q", status.Status)
		}
	})
}

func TestNewAPIHandler_ReportingToolRoutes_AcceptsCanonicalExportEnvelope(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "config.yaml"), `
default:
  reporting:
    enabled: true
`)
		rt, client, finder, err := BuildWorkspaceRuntime(context.Background(), RuntimeOptions{
			WorkspaceRoot: root,
			Defaults: &execconfig.Defaults{
				Model:    "test-model",
				Embedder: "test-embedder",
				Agent:    "coder",
			},
		})
		if err != nil {
			t.Fatalf("BuildWorkspaceRuntime failed: %v", err)
		}

		handler, err := NewAPIHandler(context.Background(), APIOptions{
			Version:     "test-version",
			Runtime:     rt,
			Client:      client,
			AgentFinder: finder,
		})
		if err != nil {
			t.Fatalf("NewAPIHandler failed: %v", err)
		}

		submitBody := []byte(`{"name":"reporting:submit_export","args":{"reportExportRequest":` + validReportingAPIExportRequestJSON() + `}}`)
		submitReq := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(submitBody))
		submitReq = submitReq.WithContext(svcauth.InjectUser(submitReq.Context(), "api-user"))
		submitRec := httptest.NewRecorder()
		handler.ServeHTTP(submitRec, submitReq)
		if submitRec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", submitRec.Code, submitRec.Body.String())
		}

		var submitEnvelope struct {
			Result string `json:"result"`
		}
		if err := json.Unmarshal(submitRec.Body.Bytes(), &submitEnvelope); err != nil {
			t.Fatalf("decode submit envelope: %v", err)
		}
		var job struct {
			ArtifactRef string `json:"artifactRef"`
			Format      string `json:"format"`
			Scope       string `json:"scope"`
			Status      string `json:"status"`
		}
		if err := json.Unmarshal([]byte(submitEnvelope.Result), &job); err != nil {
			t.Fatalf("decode submit result: %v", err)
		}
		if job.ArtifactRef != "reportBuilder.savedReportPayload://rbreport_forecasting_q3" {
			t.Fatalf("expected canonical artifactRef, got %q", job.ArtifactRef)
		}
		if job.Format != "pdf" {
			t.Fatalf("expected pdf format, got %q", job.Format)
		}
		if job.Scope != "saved_payload" {
			t.Fatalf("expected saved_payload scope, got %q", job.Scope)
		}
		if job.Status != "queued" {
			t.Fatalf("expected queued status, got %q", job.Status)
		}
	})
}

func TestNewAPIHandler_ReportingCompileRoute_WorkspaceEnabled(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "config.yaml"), `
default:
  reporting:
    enabled: true
`)

		rt, client, finder, err := BuildWorkspaceRuntime(context.Background(), RuntimeOptions{
			WorkspaceRoot: root,
			Defaults: &execconfig.Defaults{
				Model:    "test-model",
				Embedder: "test-embedder",
				Agent:    "coder",
			},
		})
		if err != nil {
			t.Fatalf("BuildWorkspaceRuntime failed: %v", err)
		}
		if rt.Reporting == nil {
			t.Fatal("expected reporting runtime to be enabled from workspace defaults")
		}

		handler, err := NewAPIHandler(context.Background(), APIOptions{
			Version:     "test-version",
			Runtime:     rt,
			Client:      client,
			AgentFinder: finder,
		})
		if err != nil {
			t.Fatalf("NewAPIHandler failed: %v", err)
		}

		compileBody := []byte(`{"name":"reporting:compile","args":{"artifactRef":"report://draft/compiled","sourceKind":"reportSpec","document":{"version":1,"kind":"reportSpec","source":{"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},"title":"Demo Report","parameters":{"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},"layoutIntent":{"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},"refinements":[],"calculatedFields":[],"datasets":[{"id":"primary","dataSourceRef":"demo","request":{}}],"blocks":[{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]}}}`)
		compileReq := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(compileBody))
		compileReq = compileReq.WithContext(svcauth.InjectUser(compileReq.Context(), "compile-user"))
		compileRec := httptest.NewRecorder()
		handler.ServeHTTP(compileRec, compileReq)
		if compileRec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", compileRec.Code, compileRec.Body.String())
		}

		var compileEnvelope struct {
			Result string `json:"result"`
		}
		if err := json.Unmarshal(compileRec.Body.Bytes(), &compileEnvelope); err != nil {
			t.Fatalf("decode compile envelope: %v", err)
		}
		var result struct {
			ArtifactRef string          `json:"artifactRef"`
			ReportSpec  json.RawMessage `json:"reportSpec"`
		}
		if err := json.Unmarshal([]byte(compileEnvelope.Result), &result); err != nil {
			t.Fatalf("decode compile result: %v", err)
		}
		if result.ArtifactRef != "report://draft/compiled" {
			t.Fatalf("expected artifact ref, got %q", result.ArtifactRef)
		}
		requireJSONEq(t, `{"version":1,"kind":"reportSpec","source":{"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},"title":"Demo Report","parameters":{"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},"layoutIntent":{"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},"refinements":[],"calculatedFields":[],"datasets":[{"id":"primary","dataSourceRef":"demo","request":{}}],"blocks":[{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]}`, string(result.ReportSpec))
	})
}

func TestNewAPIHandler_ReportingSubmitExportRejectsInvalidCanonicalPayload(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "config.yaml"), `
default:
  reporting:
    enabled: true
`)

		rt, client, finder, err := BuildWorkspaceRuntime(context.Background(), RuntimeOptions{
			WorkspaceRoot: root,
			Defaults: &execconfig.Defaults{
				Model:    "test-model",
				Embedder: "test-embedder",
				Agent:    "coder",
			},
		})
		if err != nil {
			t.Fatalf("BuildWorkspaceRuntime failed: %v", err)
		}
		if rt.Reporting == nil {
			t.Fatal("expected reporting runtime to be enabled from workspace defaults")
		}

		handler, err := NewAPIHandler(context.Background(), APIOptions{
			Version:     "test-version",
			Runtime:     rt,
			Client:      client,
			AgentFinder: finder,
		})
		if err != nil {
			t.Fatalf("NewAPIHandler failed: %v", err)
		}

		submitBody := []byte(`{"name":"reporting:submit_export","args":{"artifactRef":"report://draft/performance","format":"pdf","scope":"draft","reportPrint":{"version":1,"kind":"reportPrint"}}}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(submitBody))
		req = req.WithContext(svcauth.InjectUser(req.Context(), "api-user"))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "invalid reportPrint: missing specVersion") {
			t.Fatalf("expected invalid reportPrint error, got %s", rec.Body.String())
		}
	})
}

func TestNewAPIHandler_ReportingArtifactScopeLifecycle(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "config.yaml"), `
default:
  reporting:
    enabled: true
`)

		rt, client, finder, err := BuildWorkspaceRuntime(context.Background(), RuntimeOptions{
			WorkspaceRoot: root,
			Defaults: &execconfig.Defaults{
				Model:    "test-model",
				Embedder: "test-embedder",
				Agent:    "coder",
			},
		})
		if err != nil {
			t.Fatalf("BuildWorkspaceRuntime failed: %v", err)
		}
		if rt.Reporting == nil {
			t.Fatal("expected reporting runtime to be enabled from workspace defaults")
		}

		handler, err := NewAPIHandler(context.Background(), APIOptions{
			Version:     "test-version",
			Runtime:     rt,
			Client:      client,
			AgentFinder: finder,
		})
		if err != nil {
			t.Fatalf("NewAPIHandler failed: %v", err)
		}

		submitBody := []byte(`{"name":"reporting:submit_export","args":{"artifactRef":"report://draft/performance","format":"pdf","scope":"draft","reportPrint":` + validReportingAPIPrintJSON() + `}}`)
		authContext := svcauth.InjectUser(context.Background(), "artifact-user")
		submitReq := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(submitBody))
		submitReq = submitReq.WithContext(authContext)
		submitRec := httptest.NewRecorder()
		handler.ServeHTTP(submitRec, submitReq)
		if submitRec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", submitRec.Code, submitRec.Body.String())
		}

		var submitEnvelope struct {
			Result string `json:"result"`
		}
		if err := json.Unmarshal(submitRec.Body.Bytes(), &submitEnvelope); err != nil {
			t.Fatalf("decode submit envelope: %v", err)
		}
		var job struct {
			JobID string `json:"jobId"`
		}
		if err := json.Unmarshal([]byte(submitEnvelope.Result), &job); err != nil {
			t.Fatalf("decode submit result: %v", err)
		}
		if job.JobID == "" {
			t.Fatalf("expected job id in submit result: %s", submitEnvelope.Result)
		}

		startMethod, err := rt.Reporting.Method("start_export")
		if err != nil {
			t.Fatalf("resolve start_export: %v", err)
		}
		startOut := &reportingsvc.ExportJob{}
		requireNoErr(t, startMethod(authContext, &reportingsvc.StartExportInput{JobID: job.JobID}, startOut))
		if startOut.Status != reportingsvc.JobStatusRunning {
			t.Fatalf("expected running status, got %q", startOut.Status)
		}

		completeMethod, err := rt.Reporting.Method("complete_export")
		if err != nil {
			t.Fatalf("resolve complete_export: %v", err)
		}
		completeOut := &reportingsvc.ExportJob{}
		requireNoErr(t, completeMethod(authContext, &reportingsvc.CompleteExportRequest{
			JobID:       job.JobID,
			ContentType: "application/pdf",
			Data:        []byte("%PDF"),
		}, completeOut))
		if completeOut.Status != reportingsvc.JobStatusSucceeded {
			t.Fatalf("expected succeeded status, got %q", completeOut.Status)
		}
		if completeOut.ArtifactID == "" {
			t.Fatal("expected artifact id after completion")
		}

		artifactBody := []byte(`{"name":"reporting:get_artifact","args":{"artifactId":"` + completeOut.ArtifactID + `"}}`)
		artifactReq := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(artifactBody))
		artifactReq = artifactReq.WithContext(svcauth.InjectUser(artifactReq.Context(), "artifact-user"))
		artifactRec := httptest.NewRecorder()
		handler.ServeHTTP(artifactRec, artifactReq)
		if artifactRec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", artifactRec.Code, artifactRec.Body.String())
		}
		var artifactEnvelope struct {
			Result string `json:"result"`
		}
		if err := json.Unmarshal(artifactRec.Body.Bytes(), &artifactEnvelope); err != nil {
			t.Fatalf("decode artifact envelope: %v", err)
		}
		var artifact struct {
			ArtifactID  string `json:"artifactId"`
			ContentType string `json:"contentType"`
			Data        []byte `json:"data"`
		}
		if err := json.Unmarshal([]byte(artifactEnvelope.Result), &artifact); err != nil {
			t.Fatalf("decode artifact result: %v", err)
		}
		if artifact.ArtifactID != completeOut.ArtifactID {
			t.Fatalf("expected artifact %q, got %q", completeOut.ArtifactID, artifact.ArtifactID)
		}
		if artifact.ContentType != "application/pdf" {
			t.Fatalf("expected application/pdf, got %q", artifact.ContentType)
		}
		if string(artifact.Data) != "%PDF" {
			t.Fatalf("expected artifact data, got %q", string(artifact.Data))
		}

		deniedReq := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(artifactBody))
		deniedReq = deniedReq.WithContext(svcauth.InjectUser(deniedReq.Context(), "other-user"))
		deniedRec := httptest.NewRecorder()
		handler.ServeHTTP(deniedRec, deniedReq)
		if deniedRec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for other user, got %d body=%s", deniedRec.Code, deniedRec.Body.String())
		}
	})
}

func requireNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAPIHandler_ReportingInternalWorkerToolsAreNotCallable(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "config.yaml"), `
default:
  model: test-model
  embedder: test-embedder
  agent: coder
  reporting:
    enabled: true
`)

		rt, client, finder, err := BuildWorkspaceRuntime(context.Background(), RuntimeOptions{
			WorkspaceRoot: root,
			Defaults: &execconfig.Defaults{
				Model:    "test-model",
				Embedder: "test-embedder",
				Agent:    "coder",
			},
		})
		if err != nil {
			t.Fatalf("BuildWorkspaceRuntime failed: %v", err)
		}
		if rt.Reporting == nil {
			t.Fatal("expected reporting runtime to be enabled from workspace defaults")
		}

		handler, err := NewAPIHandler(context.Background(), APIOptions{
			Version:     "test-version",
			Runtime:     rt,
			Client:      client,
			AgentFinder: finder,
		})
		if err != nil {
			t.Fatalf("NewAPIHandler failed: %v", err)
		}

		submitBody := []byte(`{"name":"reporting:submit_export","args":{"artifactRef":"report://draft/performance","format":"pdf","scope":"draft","reportPrint":` + validReportingAPIPrintJSON() + `}}`)
		submitReq := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(submitBody))
		submitReq = submitReq.WithContext(svcauth.InjectUser(submitReq.Context(), "internal-boundary-user"))
		submitRec := httptest.NewRecorder()
		handler.ServeHTTP(submitRec, submitReq)
		if submitRec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", submitRec.Code, submitRec.Body.String())
		}

		var submitEnvelope struct {
			Result string `json:"result"`
		}
		requireNoErr(t, json.Unmarshal(submitRec.Body.Bytes(), &submitEnvelope))
		var job struct {
			JobID string `json:"jobId"`
		}
		requireNoErr(t, json.Unmarshal([]byte(submitEnvelope.Result), &job))
		if job.JobID == "" {
			t.Fatalf("expected queued job id, got %s", submitEnvelope.Result)
		}

		runExportBody := []byte(`{"name":"reporting:run_export","args":{"jobId":"` + job.JobID + `"}}`)
		runExportReq := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(runExportBody))
		runExportReq = runExportReq.WithContext(svcauth.InjectUser(runExportReq.Context(), "internal-boundary-user"))
		runExportRec := httptest.NewRecorder()
		handler.ServeHTTP(runExportRec, runExportReq)
		if runExportRec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for reporting:run_export, got %d body=%s", runExportRec.Code, runExportRec.Body.String())
		}

		runQueuedBody := []byte(`{"name":"reporting:run_queued_exports","args":{"limit":1}}`)
		runQueuedReq := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(runQueuedBody))
		runQueuedReq = runQueuedReq.WithContext(svcauth.InjectUser(runQueuedReq.Context(), "internal-boundary-user"))
		runQueuedRec := httptest.NewRecorder()
		handler.ServeHTTP(runQueuedRec, runQueuedReq)
		if runQueuedRec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for reporting:run_queued_exports, got %d body=%s", runQueuedRec.Code, runQueuedRec.Body.String())
		}

		statusBody := []byte(`{"name":"reporting:get_export_status","args":{"jobId":"` + job.JobID + `"}}`)
		statusReq := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(statusBody))
		statusReq = statusReq.WithContext(svcauth.InjectUser(statusReq.Context(), "internal-boundary-user"))
		statusRec := httptest.NewRecorder()
		handler.ServeHTTP(statusRec, statusReq)
		if statusRec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", statusRec.Code, statusRec.Body.String())
		}
		var statusEnvelope struct {
			Result string `json:"result"`
		}
		requireNoErr(t, json.Unmarshal(statusRec.Body.Bytes(), &statusEnvelope))
		var queued struct {
			Status string `json:"status"`
		}
		requireNoErr(t, json.Unmarshal([]byte(statusEnvelope.Result), &queued))
		if queued.Status != "queued" {
			t.Fatalf("expected queued status after blocked internal tool calls, got %q", queued.Status)
		}
	})
}

func requireJSONEq(t *testing.T, expected, actual string) {
	t.Helper()
	var left interface{}
	var right interface{}
	if err := json.Unmarshal([]byte(expected), &left); err != nil {
		t.Fatalf("bad expected json: %v", err)
	}
	if err := json.Unmarshal([]byte(actual), &right); err != nil {
		t.Fatalf("bad actual json: %v", err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("json mismatch\nexpected: %s\nactual:   %s", expected, actual)
	}
}

func withWorkspaceRoot(t *testing.T, body func(root string)) {
	t.Helper()
	prev := workspace.Root()
	root := t.TempDir()
	workspace.SetRoot(root)
	t.Cleanup(func() {
		workspace.SetRoot(prev)
	})
	body(root)
}

func mustWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimLeft(contents, "\n")), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func validReportingAPIPrintJSON() string {
	return `{"version":1,"kind":"reportPrint","specVersion":1,"specHash":"spec-1","fillVersion":1,"fillHash":"fill-1","source":{"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},"title":"Demo Report","pageGeometry":{"width":612,"height":792,"marginTop":48,"marginRight":48,"marginBottom":48,"marginLeft":48,"headerHeight":24,"footerHeight":24},"pages":[{"number":1,"elements":[{"id":"body-1","kind":"text","box":{"x":48,"y":96,"width":200,"height":18}}],"headerElements":[],"footerElements":[]}],"bookmarks":[{"id":"section-1","title":"Section 1","pageNumber":1}],"diagnostics":[]}`
}

func validReportingAPIExportRequestJSON() string {
	return `{"version":1,"kind":"reportExportRequest","target":{"format":"pdf"},"source":{"from":"savedPayload","artifactKind":"reportBuilder.savedReportPayload","artifactRef":"reportBuilder.savedReportPayload://rbreport_forecasting_q3","title":"Forecasting Q3","reportId":"forecastingQ3","payloadId":"rbreport_forecasting_q3","sourceArtifactId":"forecasting_q3","documentVersion":4},"reportSpec":` + validReportingAPIReportSpecJSON() + `,"reportFill":` + validReportingAPIReportFillJSON() + `,"reportPrint":` + validReportingAPIPrintJSON() + `}`
}

func validReportingAPIReportSpecJSON() string {
	return `{"version":1,"kind":"reportSpec","source":{"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},"title":"Demo Report","parameters":{"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},"layoutIntent":{"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},"refinements":[],"calculatedFields":[],"datasets":[{"id":"primary","dataSourceRef":"demo","request":{}}],"blocks":[{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]}`
}

func validReportingAPIReportFillJSON() string {
	return `{"version":1,"kind":"reportFill","specVersion":1,"specHash":"spec-1","source":{"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},"parameters":{"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},"refinements":[],"calculatedFields":[],"datasets":[{"id":"primary","dataSourceRef":"demo","request":{"limit":25,"offset":0},"provenance":{"requestHash":"request-1","rowCount":1,"truncated":false,"hasMore":false,"diagnostics":[]},"rows":[{"channel":"Display"}]}],"blocks":[{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[],"content":{"columns":[],"rowCount":1,"resolvedRows":[]}}],"diagnostics":[]}`
}
