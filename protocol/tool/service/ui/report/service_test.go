package report

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	registry "github.com/viant/agently-core/internal/tool/registry"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
	"github.com/viant/agently-core/protocol/mcp/manager"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

func postUIRPC(t *testing.T, bridge *forgeuisvc.Service, method string, params map[string]interface{}) map[string]interface{} {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "test-" + method,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		t.Fatalf("marshal rpc request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	bridge.Hub().ServeHTTPRPC(rec, req)
	var envelope map[string]interface{}
	if err := json.NewDecoder(rec.Result().Body).Decode(&envelope); err != nil {
		t.Fatalf("decode %s response: %v", method, err)
	}
	result, _ := envelope["result"].(map[string]interface{})
	return result
}

func prepareReportWindow(t *testing.T) *forgeuisvc.Service {
	t.Helper()
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	postUIRPC(t, bridge, "ui.hello", map[string]interface{}{"clientId": "client-1"})
	postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{
		"clientId": "client-1",
		"data": map[string]interface{}{
			"clientId":       "client-1",
			"conversationId": "conv-1",
			"windows": []interface{}{
				map[string]interface{}{
					"windowId":       "report-window-1",
					"windowKey":      "reportBuilder",
					"conversationId": "conv-1",
				},
			},
		},
	})
	return bridge
}

func respondToReportRun(t *testing.T, bridge *forgeuisvc.Service, result map[string]interface{}) {
	t.Helper()
	poll := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": "client-1", "timeoutMs": 1000})
	command, _ := poll["params"].(map[string]interface{})
	require.Equal(t, "ui.report.run", command["method"])
	postUIRPC(t, bridge, "ui.response", map[string]interface{}{
		"id":     command["id"],
		"ok":     true,
		"result": result,
	})
}

func TestServiceSaveDispatchesCanonicalBrowserAction(t *testing.T) {
	bridge := prepareReportWindow(t)

	service := New(bridge)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	done := make(chan error, 1)
	output := &ActionOutput{}
	go func() {
		done <- service.save(ctx, &WindowInput{WindowID: "report-window-1"}, output)
	}()

	poll := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": "client-1", "timeoutMs": 1000})
	command, _ := poll["params"].(map[string]interface{})
	if got := command["method"]; got != "ui.report.save" {
		t.Fatalf("expected ui.report.save command, got %#v", got)
	}
	postUIRPC(t, bridge, "ui.response", map[string]interface{}{
		"id": command["id"],
		"ok": true,
		"result": map[string]interface{}{
			"ok":         true,
			"artifactId": "artifact-1",
			"reportId":   "delivery-review",
		},
	})
	if err := <-done; err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if !output.OK || output.Result["artifactId"] != "artifact-1" || output.Result["reportId"] != "delivery-review" {
		t.Fatalf("unexpected save output: %#v", output)
	}
}

type fakeTerminalRunWaiter struct {
	reportRunID    string
	conversationID string
	run            *reportrun.Record
	err            error
}

func (f *fakeTerminalRunWaiter) WaitTerminal(_ context.Context, reportRunID, conversationID string) (*reportrun.Record, error) {
	f.reportRunID = reportRunID
	f.conversationID = conversationID
	return f.run, f.err
}

func TestServiceRunOrchestrationDisabledPreservesDispatchBehavior(t *testing.T) {
	bridge := prepareReportWindow(t)
	service := New(bridge)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	done := make(chan error, 1)
	output := &ActionOutput{}
	go func() {
		done <- service.run(ctx, &WindowInput{WindowID: "report-window-1"}, output)
	}()

	respondToReportRun(t, bridge, map[string]interface{}{"ok": true})
	require.NoError(t, <-done)
	require.True(t, output.OK)
	require.NotContains(t, output.Result, "status")
	require.NotContains(t, output.Result, "revision")
}

func TestServiceRunOrchestrationWaitsForExactDurableRun(t *testing.T) {
	bridge := prepareReportWindow(t)
	waiter := &fakeTerminalRunWaiter{run: &reportrun.Record{
		ReportRunID:    "run-1",
		ConversationID: "conv-1",
		Status:         reportrun.StatusCompleted,
		Revision:       7,
	}}
	service := New(bridge, WithOrchestration(waiter))
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	done := make(chan error, 1)
	output := &ActionOutput{}
	go func() {
		done <- service.run(ctx, &WindowInput{WindowID: "report-window-1"}, output)
	}()

	respondToReportRun(t, bridge, map[string]interface{}{
		"ok":          true,
		"durable":     true,
		"reportRunId": "run-1",
	})
	require.NoError(t, <-done)
	require.Equal(t, "run-1", waiter.reportRunID)
	require.Equal(t, "conv-1", waiter.conversationID)
	require.True(t, output.OK)
	require.Equal(t, true, output.Result["durable"])
	require.Equal(t, "run-1", output.Result["reportRunId"])
	require.Equal(t, reportrun.StatusCompleted, output.Result["status"])
	require.Equal(t, int64(7), output.Result["revision"])
}

func TestServiceRunOrchestrationAcceptsExactNativeMaterialization(t *testing.T) {
	bridge := prepareReportWindow(t)
	waiter := &fakeTerminalRunWaiter{}
	service := New(bridge, WithOrchestration(waiter))
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	done := make(chan error, 1)
	output := &ActionOutput{}
	go func() {
		done <- service.run(ctx, &WindowInput{WindowID: "report-window-1"}, output)
	}()

	respondToReportRun(t, bridge, map[string]interface{}{
		"ok":                true,
		"materialized":      true,
		"materializationId": "native-run-1",
		"status":            "completed",
		"datasetRefs":       []interface{}{"summary", "daily"},
	})
	require.NoError(t, <-done)
	require.True(t, output.OK)
	require.Empty(t, waiter.reportRunID)
	require.Equal(t, "native-run-1", output.Result["materializationId"])
}

func TestServiceRunOrchestrationWaitsForAcceptedNativeMaterialization(t *testing.T) {
	bridge := prepareReportWindow(t)
	waiter := &fakeTerminalRunWaiter{}
	service := New(bridge, WithOrchestration(waiter))
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	done := make(chan error, 1)
	output := &ActionOutput{}
	go func() {
		done <- service.run(ctx, &WindowInput{WindowID: "report-window-1"}, output)
	}()

	respondToReportRun(t, bridge, map[string]interface{}{
		"ok":                true,
		"accepted":          true,
		"windowId":          "report-window-1",
		"materialized":      false,
		"materializationId": "native-run-async-1",
		"status":            "running",
	})
	poll := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": "client-1", "timeoutMs": 1000})
	command, _ := poll["params"].(map[string]interface{})
	require.Equal(t, "ui.report.getCurrent", command["method"])
	postUIRPC(t, bridge, "ui.response", map[string]interface{}{
		"id": command["id"],
		"ok": true,
		"result": map[string]interface{}{
			"ok": true,
			"materialization": map[string]interface{}{
				"id":          "native-run-async-1",
				"status":      "completed",
				"datasetRefs": []interface{}{"summary", "daily"},
				"rowCounts":   map[string]interface{}{"summary": float64(1), "daily": float64(8)},
			},
		},
	})
	require.NoError(t, <-done)
	require.True(t, output.OK)
	require.Equal(t, true, output.Result["materialized"])
	require.Equal(t, "completed", output.Result["status"])
	require.Equal(t, []interface{}{"summary", "daily"}, output.Result["datasetRefs"])
	require.Empty(t, waiter.reportRunID)
}

func TestServiceRunOrchestrationRejectsNonDurableAndFailedRuns(t *testing.T) {
	for name, result := range map[string]map[string]interface{}{
		"missing durable":   {"ok": true, "reportRunId": "run-1"},
		"missing exact id":  {"ok": true, "durable": true},
		"native missing id": {"ok": true, "materialized": true, "status": "completed"},
	} {
		t.Run(name, func(t *testing.T) {
			bridge := prepareReportWindow(t)
			waiter := &fakeTerminalRunWaiter{}
			service := New(bridge, WithOrchestration(waiter))
			ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
			done := make(chan error, 1)
			go func() {
				done <- service.run(ctx, &WindowInput{WindowID: "report-window-1"}, &ActionOutput{})
			}()
			respondToReportRun(t, bridge, result)
			require.Error(t, <-done)
			require.Empty(t, waiter.reportRunID)
		})
	}

	bridge := prepareReportWindow(t)
	waiter := &fakeTerminalRunWaiter{run: &reportrun.Record{
		ReportRunID:    "run-failed",
		ConversationID: "conv-1",
		Status:         reportrun.StatusFailed,
		FailureText:    "datasource unavailable",
		Revision:       2,
	}}
	service := New(bridge, WithOrchestration(waiter))
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	done := make(chan error, 1)
	output := &ActionOutput{}
	go func() {
		done <- service.run(ctx, &WindowInput{WindowID: "report-window-1"}, output)
	}()
	respondToReportRun(t, bridge, map[string]interface{}{
		"ok":          true,
		"durable":     true,
		"reportRunId": "run-failed",
	})
	require.ErrorContains(t, <-done, "datasource unavailable")
	require.False(t, output.OK)
	require.Equal(t, reportrun.StatusFailed, output.Result["status"])
	require.Equal(t, int64(2), output.Result["revision"])
}

func TestServiceRegistryEffectiveTimeout(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	require.NoError(t, reg.AddInternalService(New(nil)))

	timeout, ok := reg.ToolTimeout("ui/report:run")
	require.True(t, ok)
	require.Equal(t, 330*time.Second, timeout)
	require.Greater(t, timeout, 300*time.Second)

	for _, method := range []string{"getCurrent", "save"} {
		timeout, ok = reg.ToolTimeout("ui/report:" + method)
		require.False(t, ok, method)
		require.Zero(t, timeout, method)
	}

	retryable, configured := reg.ToolRetryable("ui/report:run")
	require.True(t, configured)
	require.False(t, retryable)
	for _, method := range []string{"getCurrent", "save"} {
		_, configured = reg.ToolRetryable("ui/report:" + method)
		require.False(t, configured, method)
	}
}

func TestServiceMethodsRegistered(t *testing.T) {
	service := &Service{}
	for _, name := range []string{"getCurrent", "run", "save"} {
		method, err := service.Method(name)
		if err != nil || method == nil {
			t.Fatalf("expected %s method, got method=%#v err=%v", name, method, err)
		}
	}
}
