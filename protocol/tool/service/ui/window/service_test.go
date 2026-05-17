package window

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

func TestWindowAlreadyFocused(t *testing.T) {
	snap := &uireg.Snapshot{
		Selected: uireg.SnapshotSelected{WindowID: "orderPerformance_1"},
	}
	win := &uireg.WindowSnapshot{
		WindowID: "orderPerformance_1",
	}
	if !windowAlreadyFocused(snap, win) {
		t.Fatalf("expected focused window to be detected")
	}
}

func TestWindowAlreadyFocused_FalseWhenDifferent(t *testing.T) {
	snap := &uireg.Snapshot{
		Selected: uireg.SnapshotSelected{WindowID: "chat/new"},
	}
	win := &uireg.WindowSnapshot{
		WindowID: "orderPerformance_1",
	}
	if windowAlreadyFocused(snap, win) {
		t.Fatalf("expected non-focused window to remain false")
	}
}

func TestResolveListSnapshots_UsesPreferredClientFallback(t *testing.T) {
	fallback := &uireg.ClientSnapshot{ClientID: "client-1"}
	got := resolveListSnapshots(nil, "client-1", fallback)
	if len(got) != 1 || got[0].ClientID != "client-1" {
		t.Fatalf("expected preferred client fallback, got %#v", got)
	}
}

func TestResolveListSnapshots_DoesNotUseMismatchedFallback(t *testing.T) {
	fallback := &uireg.ClientSnapshot{ClientID: "client-2"}
	got := resolveListSnapshots(nil, "client-1", fallback)
	if len(got) != 0 {
		t.Fatalf("expected no fallback for mismatched client, got %#v", got)
	}
}

func TestCompactWindowSnapshot_StripsDatasourcePayloads(t *testing.T) {
	win := &uireg.WindowSnapshot{
		WindowID:       "orderPerformance_1",
		WindowKey:      "orderPerformance",
		WindowTitle:    "Order Summary",
		ConversationID: "conv-1",
		Presentation:   "hosted",
		Region:         "chat.top",
		ParentKey:      "chat/new",
		DataSources: map[string]uireg.DataSourceSnapshot{
			"order_performance_profile": {
				DataSourceRef: "order_performance_profile",
				Metrics: map[string]interface{}{
					"orderId": 2637048,
				},
			},
		},
	}
	got := compactWindowSnapshot(win)
	if got == nil {
		t.Fatalf("expected compact snapshot")
	}
	if got.DataSources != nil {
		t.Fatalf("expected datasource payloads to be stripped, got %#v", got.DataSources)
	}
	if got.ConversationID != "conv-1" || got.Presentation != "hosted" || got.Region != "chat.top" || got.ParentKey != "chat/new" {
		t.Fatalf("expected ownership metadata to survive compaction, got %#v", got)
	}
	if win.DataSources == nil {
		t.Fatalf("expected original window snapshot to stay intact")
	}
}

func TestListDataSourceRefs(t *testing.T) {
	win := &uireg.WindowSnapshot{
		DataSources: map[string]uireg.DataSourceSnapshot{
			"b": {},
			"a": {},
		},
	}
	got := listDataSourceRefs(win)
	if len(got) != 2 {
		t.Fatalf("expected 2 refs, got %#v", got)
	}
	seen := map[string]bool{}
	for _, ref := range got {
		seen[ref] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("expected refs a and b, got %#v", got)
	}
}

func TestCompactWindowParameters(t *testing.T) {
	input := map[string]interface{}{
		"AdOrderId": []interface{}{2637048},
		"foo":       "bar",
	}
	got := compactWindowParameters(input)
	if got == nil {
		t.Fatalf("expected compact parameters")
	}
	if got["foo"] != "bar" {
		t.Fatalf("expected foo parameter to survive, got %#v", got)
	}
	ids, ok := got["AdOrderId"].([]interface{})
	if !ok || len(ids) != 1 || ids[0] != 2637048 {
		t.Fatalf("expected AdOrderId to survive, got %#v", got["AdOrderId"])
	}
	if len(compactWindowParameters(nil)) != 0 {
		t.Fatalf("expected nil parameters to stay empty")
	}
}

func TestServiceMethod_SelectTabRegistered(t *testing.T) {
	svc := &Service{}
	method, err := svc.Method("selectTab")
	if err != nil {
		t.Fatalf("expected selectTab method to resolve, got %v", err)
	}
	if method == nil {
		t.Fatalf("expected selectTab method implementation")
	}
}

func TestServiceMethod_SetFormDataRegistered(t *testing.T) {
	svc := &Service{}
	method, err := svc.Method("setFormData")
	if err != nil {
		t.Fatalf("expected setFormData method to resolve, got %v", err)
	}
	if method == nil {
		t.Fatalf("expected setFormData method implementation")
	}
}

func TestSetFormDataThenGetReflectsUpdatedLiveWindowSnapshot(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bridge.Hub().ServeWS(w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]interface{}{
		"type":     "ui.hello",
		"clientId": "client-1",
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	initialSnapshot := map[string]interface{}{
		"clientId":       "client-1",
		"conversationId": "conv-1",
		"selected": map[string]interface{}{
			"windowId": "metricReportBuilder__conv-1",
		},
		"windows": []interface{}{
			map[string]interface{}{
				"windowId":       "metricReportBuilder__conv-1",
				"windowKey":      "metricReportBuilder",
				"windowTitle":    "Performance Metrics",
				"conversationId": "conv-1",
				"presentation":   "hosted",
				"region":         "chat.top",
				"parentKey":      "chat/new",
				"windowForm":     map[string]interface{}{},
				"metadata": map[string]interface{}{
					"view": map[string]interface{}{
						"controls": []interface{}{},
					},
				},
			},
		},
	}
	if err := conn.WriteJSON(map[string]interface{}{
		"type":     "ui.snapshot",
		"clientId": "client-1",
		"data":     initialSnapshot,
	}); err != nil {
		t.Fatalf("write initial snapshot: %v", err)
	}

	svc := New(bridge)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")

	deadline := time.Now().Add(2 * time.Second)
	for {
		probe := &GetOutput{}
		err := svc.get(ctx, &GetInput{WindowID: "metricReportBuilder__conv-1"}, probe)
		if err == nil && probe.Window != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("window snapshot did not become visible to registry in time: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	done := make(chan error, 1)
	go func() {
		out := &CommandOutput{}
		err := svc.setFormData(ctx, &SetFormDataInput{
			WindowID: "metricReportBuilder__conv-1",
			Values: map[string]interface{}{
				"prefill": map[string]interface{}{
					"advertiserId": 123,
					"dealId":       "deal-xyz",
				},
			},
		}, out)
		if err != nil {
			done <- err
			return
		}
		if !out.OK {
			done <- fmt.Errorf("unexpected output: %#v", out)
			return
		}
		done <- nil
	}()

	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var request map[string]interface{}
	if err := conn.ReadJSON(&request); err != nil {
		select {
		case callErr := <-done:
			t.Fatalf("setFormData returned before command read: %v", callErr)
		default:
		}
		t.Fatalf("read command: %v", err)
	}
	if got := request["method"]; got != "ui.window.setFormData" {
		t.Fatalf("expected ui.window.setFormData command, got %#v", got)
	}
	params, ok := request["params"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected params map, got %#v", request["params"])
	}
	values, ok := params["values"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected values map, got %#v", params["values"])
	}
	prefill, ok := values["prefill"].(map[string]interface{})
	if !ok || prefill["dealId"] != "deal-xyz" {
		t.Fatalf("expected semantic prefill payload, got %#v", values)
	}

	updatedSnapshot := map[string]interface{}{
		"clientId":       "client-1",
		"conversationId": "conv-1",
		"selected": map[string]interface{}{
			"windowId": "metricReportBuilder__conv-1",
		},
		"windows": []interface{}{
			map[string]interface{}{
				"windowId":       "metricReportBuilder__conv-1",
				"windowKey":      "metricReportBuilder",
				"windowTitle":    "Performance Metrics",
				"conversationId": "conv-1",
				"presentation":   "hosted",
				"region":         "chat.top",
				"parentKey":      "chat/new",
				"windowForm": map[string]interface{}{
					"prefill": map[string]interface{}{
						"advertiserId": float64(123),
						"dealId":       "deal-xyz",
					},
				},
				"metadata": map[string]interface{}{
					"view": map[string]interface{}{
						"controls": []interface{}{},
					},
				},
			},
		},
	}
	if err := conn.WriteJSON(map[string]interface{}{
		"type":     "ui.snapshot",
		"clientId": "client-1",
		"data":     updatedSnapshot,
	}); err != nil {
		t.Fatalf("write updated snapshot: %v", err)
	}

	if err := conn.WriteJSON(map[string]interface{}{
		"id": request["id"],
		"ok": true,
		"result": map[string]interface{}{
			"ok": true,
		},
	}); err != nil {
		t.Fatalf("write response: %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("setFormData failed: %v", err)
	}

	got := &GetOutput{}
	if err := svc.get(ctx, &GetInput{WindowID: "metricReportBuilder__conv-1"}, got); err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Window == nil {
		t.Fatalf("expected window snapshot")
	}
	prefillMap, ok := got.Window.WindowForm["prefill"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected prefill map in windowForm, got %#v", got.Window.WindowForm)
	}
	if prefillMap["dealId"] != "deal-xyz" {
		t.Fatalf("expected updated live snapshot value, got %#v", prefillMap)
	}
}

func TestBuildWindowSurface(t *testing.T) {
	win := &uireg.WindowSnapshot{
		WindowForm: map[string]interface{}{
			"periodViewKpis":      "7d",
			"granularityViewKpis": "hour",
			"sectionView":         "kpiTab",
		},
		ViewState: map[string]interface{}{
			"tabs": map[string]interface{}{
				"analysisPane": "kpiTab",
			},
		},
		Metadata: map[string]interface{}{
			"view": map[string]interface{}{
				"tabs": []interface{}{
					map[string]interface{}{"containerId": "analysisPane", "tabId": "deliveryTab", "title": "Delivery"},
					map[string]interface{}{"containerId": "analysisPane", "tabId": "kpiTab", "title": "KPIs"},
				},
				"controls": []interface{}{
					map[string]interface{}{
						"id":          "periodViewKpis",
						"label":       "Period",
						"type":        "buttonGroup",
						"scope":       "windowForm",
						"bindingPath": "periodViewKpis",
						"options": []interface{}{
							map[string]interface{}{"value": "today", "label": "Today"},
							map[string]interface{}{"value": "7d", "label": "7D"},
						},
					},
					map[string]interface{}{
						"id":          "granularityViewKpis",
						"label":       "Granularity",
						"type":        "radio",
						"scope":       "windowForm",
						"bindingPath": "granularityViewKpis",
					},
				},
			},
		},
	}
	got := buildWindowSurface(win)
	if got == nil {
		t.Fatalf("expected window surface summary")
	}
	if len(got.Tabs) != 2 {
		t.Fatalf("expected 2 tabs, got %#v", got.Tabs)
	}
	if got.Tabs[1].TabID != "kpiTab" || !got.Tabs[1].Selected {
		t.Fatalf("expected selected KPI tab, got %#v", got.Tabs)
	}
	if len(got.Controls) != 2 {
		t.Fatalf("expected 2 controls, got %#v", got.Controls)
	}
	if got.Controls[1].ID != "periodViewKpis" || got.Controls[1].Value != "7d" {
		t.Fatalf("expected resolved period control value, got %#v", got.Controls)
	}
	if len(got.Controls[1].Options) != 2 {
		t.Fatalf("expected options to survive, got %#v", got.Controls[1].Options)
	}
}

func TestBuildWindowSurface_OmitsNilBindingFields(t *testing.T) {
	win := &uireg.WindowSnapshot{
		WindowForm: map[string]interface{}{
			"periodView": "7d",
		},
		Metadata: map[string]interface{}{
			"view": map[string]interface{}{
				"controls": []interface{}{
					map[string]interface{}{
						"id":      "periodView",
						"label":   "Period",
						"type":    "radio",
						"scope":   "windowForm",
						"options": []interface{}{map[string]interface{}{"value": "7d", "label": "7D"}},
					},
				},
			},
		},
	}
	got := buildWindowSurface(win)
	if got == nil || len(got.Controls) != 1 {
		t.Fatalf("expected one surface control, got %#v", got)
	}
	if got.Controls[0].BindingPath != "" {
		t.Fatalf("expected empty bindingPath, got %#v", got.Controls[0].BindingPath)
	}
	if got.Controls[0].DataField != "" {
		t.Fatalf("expected empty dataField, got %#v", got.Controls[0].DataField)
	}
	if got.Controls[0].Value != "7d" {
		t.Fatalf("expected resolved control value, got %#v", got.Controls[0].Value)
	}
}
