package window

import (
	"testing"

	uireg "github.com/viant/agently-core/service/ui/window/registry"
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
		WindowID:    "orderPerformance_1",
		WindowKey:   "orderPerformance",
		WindowTitle: "Order Summary",
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
