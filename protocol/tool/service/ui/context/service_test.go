package context

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
)

func TestGetUsesRequestClientInsteadOfMostRecentlyPolledClient(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	for _, clientID := range []string{"caller", "other"} {
		postContextUIRPC(t, bridge, "ui.hello", map[string]interface{}{"clientId": clientID})
		postContextUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{
			"clientId": clientID,
			"data": map[string]interface{}{
				"clientId": clientID, "conversationId": "conv-1",
				"selected": map[string]interface{}{"windowId": clientID + "-window"},
				"windows": []interface{}{map[string]interface{}{
					"windowId": clientID + "-window", "windowKey": "advertiser",
					"conversationId": "conv-1", "presentation": "hosted",
				}},
			},
		})
	}
	// Only the other client has a heartbeat, so it wins the default registry order.
	postContextUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": "other", "timeoutMs": 1})
	svc := New(bridge)
	baseCtx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	for _, tc := range []struct {
		name, preferred, explicit, want string
		missing                         bool
	}{
		{name: "default registry order", want: "other"},
		{name: "request client wins", preferred: "caller", want: "caller"},
		{name: "explicit client wins", preferred: "caller", explicit: "other", want: "other"},
		{name: "default input uses request client", preferred: "caller", explicit: "default", want: "caller"},
		{name: "missing request client", preferred: "missing", want: "missing", missing: true},
		{name: "missing explicit client", preferred: "caller", explicit: "missing", want: "missing", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := runtimerequestctx.WithPreferredUIClientID(baseCtx, tc.preferred)
			out := &GetOutput{}
			if err := svc.get(ctx, &GetInput{ClientID: tc.explicit}, out); err != nil {
				t.Fatal(err)
			}
			if out.ClientID != tc.want {
				t.Fatalf("client = %q, want %q", out.ClientID, tc.want)
			}
			if tc.missing {
				if len(out.Windows) != 0 || out.FocusedWindowID != "" || out.Selected != nil {
					t.Fatalf("unavailable client fell back to another client's context: %#v", out)
				}
				return
			}
			if len(out.Windows) != 1 || out.Windows[0].Window.WindowID != tc.want+"-window" || out.FocusedWindowID != tc.want+"-window" || out.Selected == nil || out.Selected.WindowID != tc.want+"-window" {
				t.Fatalf("wrong client window or focus: %#v", out)
			}
		})
	}
}

func postContextUIRPC(t *testing.T, bridge *forgeuisvc.Service, method string, params map[string]interface{}) {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": "test", "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	bridge.Hub().ServeHTTPRPC(rec, req)
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %s response: %v", method, err)
	}
	if rec.Code < 200 || rec.Code >= 300 || len(envelope.Error) != 0 && string(envelope.Error) != "null" {
		t.Fatalf("%s failed: HTTP %d, %s", method, rec.Code, rec.Body.String())
	}
}

func TestGetProjectsConversationEventsWithoutLiveWindowSnapshot(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	svc := New(bridge)
	svc.reg.RecordConversationEvent("conv-1", uireg.UIEvent{
		WindowID:  "reportBuilder__conv-1",
		WindowKey: "reportBuilder",
		Kind:      "report.context_updated",
		Detail: map[string]interface{}{
			"reportName": "Performance Brief",
			"reportId":   "performance",
			"sourceKind": "preset",
			"filters":    map[string]interface{}{"orderId": 8567},
		},
	})
	svc.reg.RecordConversationEvent("conv-1", uireg.UIEvent{
		WindowID:  "reportBuilder__conv-1",
		WindowKey: "reportBuilder",
		Kind:      "report.run",
		Detail: map[string]interface{}{
			"reportName": "Performance Brief",
			"reportId":   "performance",
			"sourceKind": "preset",
			"filters":    map[string]interface{}{"orderId": 8567},
			"runId":      "run-1",
			"status":     "succeeded",
		},
	})

	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	out := &GetOutput{}
	if err := svc.get(ctx, &GetInput{WindowKey: "reportBuilder"}, out); err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if len(out.Windows) != 0 || len(out.RecentEvents) != 2 {
		t.Fatalf("expected event context without a live window snapshot, got %#v", out)
	}
	if len(out.CurrentReports) != 1 || out.CurrentReports[0].LatestRun == nil {
		t.Fatalf("expected current report projection from conversation events, got %#v", out.CurrentReports)
	}
}

func TestBuildCurrentReportContextsCorrelatesLatestRunAndExport(t *testing.T) {
	events := []uireg.UIEvent{
		{Seq: 1, WindowID: "window-1", WindowKey: "performance", Kind: "report.context_updated", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "filters": map[string]interface{}{"orderId": 8567},
		}},
		{Seq: 2, WindowID: "window-1", WindowKey: "performance", Kind: "report.run", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "filters": map[string]interface{}{"orderId": 8567}, "runId": "run-1",
		}},
		{Seq: 3, WindowID: "window-1", WindowKey: "performance", Kind: "report.export_complete", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "filters": map[string]interface{}{"orderId": 8567}, "artifactId": "artifact-1",
		}},
	}
	contexts := buildCurrentReportContexts(events)
	if len(contexts) != 1 {
		t.Fatalf("expected one report context, got %#v", contexts)
	}
	current := contexts[0]
	if current.ReportName != "Performance Brief" || current.LatestRun == nil || current.LatestRun.Seq != 2 || current.LatestExport == nil || current.LatestExport.Seq != 3 {
		t.Fatalf("unexpected current report context: %#v", current)
	}
	if current.Filters["orderId"] != float64(8567) && current.Filters["orderId"] != 8567 {
		t.Fatalf("expected order filter, got %#v", current.Filters)
	}
}

func TestBuildCurrentReportContextsUsesCompletedEventsMatchingActiveScope(t *testing.T) {
	events := []uireg.UIEvent{
		{Seq: 8, WindowID: "reportBuilder__conv-1", WindowKey: "reportBuilder", Kind: "report.export_start", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "filters": map[string]interface{}{"orderId": 8567},
		}},
		{Seq: 4, WindowID: "reportBuilder__conv-1", WindowKey: "reportBuilder", Kind: "report.run", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "filters": map[string]interface{}{"orderId": 8567}, "runId": "run-1", "status": "succeeded",
		}},
		{Seq: 7, WindowID: "reportBuilder__conv-1", WindowKey: "reportBuilder", Kind: "report.run_start", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "filters": map[string]interface{}{"orderId": 8567}, "runId": "run-2",
		}},
		{Seq: 6, WindowID: "reportBuilder__conv-1", WindowKey: "reportBuilder", Kind: "report.export_complete", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "filters": map[string]interface{}{"orderId": 999}, "artifactId": "wrong-scope",
		}},
		{Seq: 5, WindowID: "reportBuilder__conv-1", WindowKey: "reportBuilder", Kind: "report.export_complete", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "artifactRef": "scratchpad://artifact/artifact-1", "filters": map[string]interface{}{"orderId": 8567}, "artifactId": "artifact-1", "status": "succeeded",
		}},
		{Seq: 9, WindowID: "reportBuilder__conv-1", WindowKey: "reportBuilder", Kind: "report.context_updated", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "artifactRef": "report://performance", "sourceKind": "preset", "filters": map[string]interface{}{"orderId": 8567},
		}},
	}

	contexts := buildCurrentReportContexts(events)
	if len(contexts) != 1 {
		t.Fatalf("expected one current report, got %#v", contexts)
	}
	current := contexts[0]
	if current.LatestRun == nil || current.LatestRun.Seq != 4 {
		t.Fatalf("expected latest completed matching run, got %#v", current.LatestRun)
	}
	if current.LatestExport == nil || current.LatestExport.Seq != 5 {
		t.Fatalf("expected latest completed exact-scope export, got %#v", current.LatestExport)
	}
	if current.ContextEvent == nil || current.ContextEvent.Seq != 9 || current.SourceKind != "preset" {
		t.Fatalf("expected newest context anchor, got %#v", current)
	}
}

func TestBuildCurrentReportContextsDoesNotReuseExportWithoutArtifact(t *testing.T) {
	events := []uireg.UIEvent{
		{Seq: 1, WindowID: "window-1", Kind: "report.context_updated", Detail: map[string]interface{}{
			"reportName": "Inline Brief", "filters": map[string]interface{}{"orderId": 7},
		}},
		{Seq: 2, WindowID: "window-1", Kind: "report.export_complete", Detail: map[string]interface{}{
			"reportName": "Inline Brief", "filters": map[string]interface{}{"orderId": 7}, "status": "succeeded",
		}},
	}
	contexts := buildCurrentReportContexts(events)
	if len(contexts) != 1 || contexts[0].LatestExport != nil {
		t.Fatalf("export without artifactId must not be reusable: %#v", contexts)
	}
}

func TestNewestFirstUIEventsUsesSequenceThenTimestamp(t *testing.T) {
	base := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	events := []uireg.UIEvent{
		{Seq: 1, At: base, Kind: "first"},
		{Seq: 3, At: base, Kind: "third"},
		{Seq: 2, At: base.Add(time.Minute), Kind: "second"},
	}
	ordered := newestFirstUIEvents(events)
	got := []string{ordered[0].Kind, ordered[1].Kind, ordered[2].Kind}
	if !reflect.DeepEqual(got, []string{"third", "second", "first"}) {
		t.Fatalf("expected newest-first events, got %#v", got)
	}
}

func TestContextReadRedactsSensitiveStateWithoutChangingScopeOrRuntimeProofs(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	postContextUIRPC(t, bridge, "ui.hello", map[string]interface{}{"clientId": "private-read"})
	postContextUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{"clientId": "private-read", "data": map[string]interface{}{"conversationId": "chat", "selected": map[string]interface{}{"windowId": "entity"}, "windows": []interface{}{map[string]interface{}{
		"windowId": "entity", "windowKey": "entity", "conversationId": "chat", "presentation": "hosted", "operationRevision": 123,
		"resourceTarget": map[string]interface{}{"platform": "web", "selectionToken": "existing-runtime-proof"},
		"windowForm":     map[string]interface{}{"Name": "Visible", "pin": "opaque-private-value", "apiKey": "opaque-private-value", "note": "Basic dXNlcjpwYXNz"},
		"metadata":       map[string]interface{}{"view": map[string]interface{}{"dataSourceRef": "rows", "controls": []interface{}{map[string]interface{}{"id": "pin", "type": "password", "scope": "windowForm", "dataField": "pin", "dataSourceRef": "rows"}}}},
	}}}})
	var output GetOutput
	if err := New(bridge).get(runtimerequestctx.WithConversationID(context.Background(), "chat"), &GetInput{ClientID: "private-read"}, &output); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"opaque-private-value", "dXNlcjpwYXNz"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("read leaked sensitive value %q", secret)
		}
	}
	if !output.ReadRedacted || len(output.Windows) != 1 || output.Windows[0].Window.OperationRevision != 123 || output.Windows[0].Window.WindowForm["Name"] != "Visible" {
		t.Fatalf("safe read state/scope was changed: %s", encoded)
	}
	if output.Windows[0].Window.ResourceTarget.SelectionToken != "existing-runtime-proof" {
		t.Fatal("existing runtime target proof contract changed")
	}
}
