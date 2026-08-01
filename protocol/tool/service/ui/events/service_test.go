package events

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportfs "github.com/viant/agently-core/app/store/reporting/fs"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	authsvc "github.com/viant/agently-core/service/auth"
	reportingrunsvc "github.com/viant/agently-core/service/reportingrun"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	fsstate "github.com/viant/agently-core/workspace/store/fs"
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
	resp := rec.Result()
	defer resp.Body.Close()
	var envelope map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode %s response: %v", method, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("%s returned HTTP %d: %#v", method, resp.StatusCode, envelope)
	}
	if errValue := envelope["error"]; errValue != nil {
		t.Fatalf("%s returned RPC error: %#v", method, errValue)
	}
	result, _ := envelope["result"].(map[string]interface{})
	return result
}

func seedEventWindow(t *testing.T, bridge *forgeuisvc.Service) {
	t.Helper()
	postUIRPC(t, bridge, "ui.hello", map[string]interface{}{"clientId": "active-client"})
	postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{
		"clientId": "active-client",
		"data": map[string]interface{}{
			"clientId":       "active-client",
			"conversationId": "conv-1",
			"selected": map[string]interface{}{
				"windowId": "genericBuilder__conv-1",
			},
			"windows": []interface{}{
				map[string]interface{}{
					"windowId":       "genericBuilder__conv-1",
					"windowKey":      "genericBuilder",
					"windowTitle":    "Generic Builder",
					"conversationId": "conv-1",
					"presentation":   "hosted",
					"region":         "chat.top",
					"parentKey":      "chat/new",
				},
			},
		},
	})
}

func TestListFallsBackToExactWindowIDWhenClientIDIsStale(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	seedEventWindow(t, bridge)
	svc := New(bridge)
	svc.reg.RecordEvent("default", "active-client", uireg.UIEvent{
		ConversationID: "conv-1",
		ClientID:       "active-client",
		WindowID:       "genericBuilder__conv-1",
		WindowKey:      "genericBuilder",
		Kind:           "control.set_value",
		Actor:          "agent",
	})

	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	out := &ListOutput{}
	if err := svc.list(ctx, &ListInput{
		ClientID:  "stale-client",
		WindowID:  "genericBuilder__conv-1",
		WindowKey: "genericBuilder",
		Kinds:     []string{"control.set_value"},
		Limit:     10,
	}, out); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(out.Events) != 1 {
		t.Fatalf("expected one exact-window fallback event, got %#v", out.Events)
	}
	if out.Events[0].ClientID != "active-client" || out.Events[0].WindowID != "genericBuilder__conv-1" {
		t.Fatalf("expected active exact-window event, got %#v", out.Events[0])
	}
}

func TestRecordScopesBrowserEventToCurrentConversationWindow(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	seedEventWindow(t, bridge)
	svc := New(bridge)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	out := &RecordOutput{}
	err := svc.record(ctx, &RecordInput{
		WindowKey: "genericBuilder",
		Kind:      "report.export_start",
		Detail: map[string]interface{}{
			"reportName": "Inventory Brief",
			"filters":    map[string]interface{}{"orderId": 2680567},
		},
	}, out)
	if err != nil {
		t.Fatalf("record failed: %v", err)
	}
	if !out.Recorded || out.Event.ConversationID != "conv-1" || out.Event.WindowID != "genericBuilder__conv-1" {
		t.Fatalf("unexpected record output: %#v", out)
	}
	if out.Event.Seq <= 0 || out.Event.At.IsZero() {
		t.Fatalf("expected stored event identity in output: %#v", out.Event)
	}
	listed := &ListOutput{}
	if err := svc.list(ctx, &ListInput{Kinds: []string{"report.export_start"}, Limit: 10}, listed); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(listed.Events) != 1 || listed.Events[0].Detail["reportName"] != "Inventory Brief" {
		t.Fatalf("unexpected recorded events: %#v", listed.Events)
	}
}

func TestListDefaultsToNewestFirst(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	seedEventWindow(t, bridge)
	svc := New(bridge)
	for _, kind := range []string{"report.run_start", "report.run", "report.export_complete"} {
		svc.reg.RecordEvent("default", "active-client", uireg.UIEvent{
			ConversationID: "conv-1",
			WindowID:       "genericBuilder__conv-1",
			WindowKey:      "genericBuilder",
			Kind:           kind,
		})
	}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	out := &ListOutput{}
	if err := svc.list(ctx, &ListInput{Limit: 10}, out); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(out.Events) != 3 || out.Events[0].Kind != "report.export_complete" || out.Events[2].Kind != "report.run_start" {
		t.Fatalf("expected newest-first events, got %#v", out.Events)
	}
}

func TestRecordContinuesExactAuthenticatedWindowAcrossBridgeReconnect(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	svc := New(bridge)
	svc.reg.RecordEvent("default", "browser-client", uireg.UIEvent{
		ConversationID: "conv-1",
		ClientID:       "browser-client",
		WindowID:       "report-window-1",
		WindowKey:      "reportBuilder",
		Kind:           "view.open",
		Actor:          "agent",
	})

	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	out := &RecordOutput{}
	err := svc.record(ctx, &RecordInput{
		WindowID: "report-window-1",
		Kind:     "report.run_start",
		Detail:   map[string]interface{}{"runId": "run-1"},
	}, out)
	if err != nil {
		t.Fatalf("record after reconnect failed: %v", err)
	}
	if !out.Recorded || out.Event.ClientID != "browser-client" || out.Event.WindowKey != "reportBuilder" {
		t.Fatalf("expected exact prior browser identity, got %#v", out)
	}
}

func TestRecordRejectsUnknownWindowAcrossBridgeReconnect(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	svc := New(bridge)
	svc.reg.RecordEvent("default", "browser-client", uireg.UIEvent{
		ConversationID: "conv-1",
		ClientID:       "browser-client",
		WindowID:       "report-window-1",
		WindowKey:      "reportBuilder",
		Kind:           "view.open",
		Actor:          "agent",
	})

	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	err := svc.record(ctx, &RecordInput{
		WindowID: "forged-window",
		Kind:     "report.run_start",
	}, &RecordOutput{})
	if err == nil {
		t.Fatal("expected an unknown reconnect window to be rejected")
	}
}

func TestDurableRunEventsUsePersistedLifecycleAfterRestartAndExactWindowID(t *testing.T) {
	stateStore := fsstate.NewStateStore(t.TempDir())
	firstStore := reportfs.New(stateStore).(reportstore.RunClient)
	firstRuns := reportingrunsvc.New(reportingrunsvc.Options{
		Store: firstStore,
		NewID: func() string {
			return "durable-run-1"
		},
	})
	ownerBase := authsvc.InjectUser(context.Background(), "owner-1")
	begun, err := firstRuns.Begin(ownerBase, &reportingrunsvc.BeginInput{
		ConversationID: "conv-1",
		Origin:         "prompt",
		UIRunRequestID: "durable-request-1",
	})
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	completed, err := firstRuns.Complete(ownerBase, &reportingrunsvc.CompleteInput{
		ReportRunID:      begun.Run.ReportRunID,
		ConversationID:   "conv-1",
		ExpectedRevision: 1,
		ReportSpec:       []byte(`{"kind":"reportSpec"}`),
		ReportFill:       []byte(`{"kind":"reportFill"}`),
		ReportPrint:      []byte(`{"kind":"reportPrint"}`),
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	restartedStore := reportfs.New(stateStore).(reportstore.RunClient)
	restartedRuns := reportingrunsvc.New(reportingrunsvc.Options{Store: restartedStore})
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	seedEventWindow(t, bridge)
	svc := New(bridge, WithDurableReportRuns(restartedRuns))
	ownerCtx := authsvc.InjectUser(
		runtimerequestctx.WithConversationID(context.Background(), "conv-1"),
		"owner-1",
	)
	detail := map[string]interface{}{
		"reportRunId": completed.ReportRunID,
		"revision":    completed.Revision,
		"status":      "completed",
	}

	if err := svc.record(ownerCtx, &RecordInput{
		WindowKey: "genericBuilder",
		Kind:      "report.run",
		Detail:    detail,
	}, &RecordOutput{}); err == nil {
		t.Fatal("durable report.run routed by windowKey without windowId")
	}
	out := &RecordOutput{}
	if err := svc.record(ownerCtx, &RecordInput{
		WindowID:  "genericBuilder__conv-1",
		WindowKey: "stale-window-key",
		Kind:      "report.run",
		Detail:    detail,
	}, out); err != nil {
		t.Fatalf("restart-backed report.run error = %v", err)
	}
	if !out.Recorded || out.Event.WindowID != "genericBuilder__conv-1" || out.Event.WindowKey != "genericBuilder" {
		t.Fatalf("restart-backed report.run output = %#v", out)
	}
	if err := svc.record(ownerCtx, &RecordInput{
		WindowKey: "genericBuilder",
		Kind:      "report.export_complete",
		Detail:    map[string]interface{}{"artifactId": "artifact-1", "status": "succeeded"},
	}, &RecordOutput{}); err != nil {
		t.Fatalf("export event semantics changed under durable T1 validation: %v", err)
	}
	if err := svc.record(ownerCtx, &RecordInput{
		WindowID: "genericBuilder__conv-1",
		Kind:     "report.run_start",
		Detail: map[string]interface{}{
			"reportRunId": completed.ReportRunID,
			"revision":    1,
			"status":      "running",
		},
	}, &RecordOutput{}); err == nil {
		t.Fatal("stale/out-of-order report.run_start was accepted after completion")
	}
	otherOwnerCtx := authsvc.InjectUser(
		runtimerequestctx.WithConversationID(context.Background(), "conv-1"),
		"owner-2",
	)
	if err := svc.record(otherOwnerCtx, &RecordInput{
		WindowID: "genericBuilder__conv-1",
		Kind:     "report.run",
		Detail:   detail,
	}, &RecordOutput{}); err == nil {
		t.Fatal("cross-owner durable report.run was accepted")
	}
}
