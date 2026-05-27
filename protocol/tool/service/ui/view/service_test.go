package view

import (
	"fmt"
	"testing"

	viewproto "github.com/viant/agently-core/protocol/ui/view"
)

func TestExpandOpenParametersBindsOneInputToMultipleTargets(t *testing.T) {
	specParams := []viewproto.Parameter{
		{Name: "RecordId", BindTo: "order_performance_profile.parameters.RecordId"},
		{Name: "RecordId", BindTo: "order_performance_period_today.parameters.RecordId"},
		{Name: "RecordId", BindTo: "order_performance_period_yesterday.parameters.RecordId"},
		{Name: "RecordId", BindTo: "order_performance_period_7d.parameters.RecordId"},
		{Name: "RecordId", BindTo: "order_performance_period_30d.parameters.RecordId"},
	}

	actual := expandOpenParameters(specParams, map[string]interface{}{
		"RecordId": []interface{}{2664124.0},
	})

	assertNestedValue(t, actual, []interface{}{2664124.0}, "order_performance_profile", "parameters", "RecordId")
	assertNestedValue(t, actual, []interface{}{2664124.0}, "order_performance_period_today", "parameters", "RecordId")
	assertNestedValue(t, actual, []interface{}{2664124.0}, "order_performance_period_yesterday", "parameters", "RecordId")
	assertNestedValue(t, actual, []interface{}{2664124.0}, "order_performance_period_7d", "parameters", "RecordId")
	assertNestedValue(t, actual, []interface{}{2664124.0}, "order_performance_period_30d", "parameters", "RecordId")
}

func TestExpandOpenParametersPreservesUnboundParameters(t *testing.T) {
	specParams := []viewproto.Parameter{
		{Name: "RecordId", BindTo: "order_performance_profile.parameters.RecordId"},
	}

	actual := expandOpenParameters(specParams, map[string]interface{}{
		"RecordId": []interface{}{2664124.0},
		"ClientID": "client-1",
	})

	if actual["ClientID"] != "client-1" {
		t.Fatalf("expected passthrough ClientID, got %#v", actual["ClientID"])
	}
}

func TestExpandOpenParametersBindsSemanticBuilderPrefill(t *testing.T) {
	specParams := []viewproto.Parameter{
		{Name: "customerId", BindTo: "prefill.customerId"},
		{Name: "dealId", BindTo: "prefill.dealId"},
		{Name: "targetingIncl", BindTo: "prefill.targetingIncl"},
	}

	actual := expandOpenParameters(specParams, map[string]interface{}{
		"customerId":    123.0,
		"dealId":        778899.0,
		"targetingIncl": "iris:1466062,123",
	})

	assertNestedValue(t, actual, 123.0, "prefill", "customerId")
	assertNestedValue(t, actual, 778899.0, "prefill", "dealId")
	assertNestedValue(t, actual, "iris:1466062,123", "prefill", "targetingIncl")
}

func TestMissingRequiredParameters(t *testing.T) {
	specParams := []viewproto.Parameter{
		{Name: "RecordId", Required: true, BindTo: "order_performance_profile.parameters.RecordId"},
		{Name: "RecordId", Required: true, BindTo: "order_performance_period_today.parameters.RecordId"},
	}

	if missing := missingRequiredParameters(specParams, nil); len(missing) != 1 || missing[0] != "RecordId" {
		t.Fatalf("expected RecordId to be reported missing, got %#v", missing)
	}

	if missing := missingRequiredParameters(specParams, map[string]interface{}{
		"RecordId": []interface{}{2664124.0},
	}); len(missing) != 0 {
		t.Fatalf("expected no missing required parameters, got %#v", missing)
	}
}

func TestAvailableViewIDs(t *testing.T) {
	items := []ListItem{
		{ID: "orderPerformance"},
		{ID: " approvals "},
		{ID: ""},
	}
	got := availableViewIDs(items)
	if len(got) != 2 {
		t.Fatalf("expected 2 ids, got %#v", got)
	}
	if got[0] != "approvals" || got[1] != "orderPerformance" {
		t.Fatalf("unexpected ids: %#v", got)
	}
}

func TestBuildOpenWindowOptions_HostedViewsAttachToChatRootAndReplaceRegion(t *testing.T) {
	item := &ListItem{
		WindowKey:    "order",
		Presentation: "hosted",
		Region:       "chat.top",
		OpenMode:     "replace",
	}
	got := buildOpenWindowOptions(item, "conv-1", "")
	if got["conversationId"] != "conv-1" {
		t.Fatalf("expected conversationId, got %#v", got["conversationId"])
	}
	if got["parentKey"] != "chat/new" {
		t.Fatalf("expected chat/new parentKey, got %#v", got["parentKey"])
	}
	if got["replaceHostedRegion"] != true {
		t.Fatalf("expected replaceHostedRegion=true, got %#v", got["replaceHostedRegion"])
	}
}

func TestBuildOpenWindowOptions_NonHostedViewsDoNotForceHostedOwnership(t *testing.T) {
	item := &ListItem{
		WindowKey:    "schedule",
		Presentation: "",
		Region:       "",
	}
	got := buildOpenWindowOptions(item, "conv-1", "")
	if _, ok := got["parentKey"]; ok {
		t.Fatalf("did not expect parentKey for non-hosted view")
	}
	if _, ok := got["replaceHostedRegion"]; ok {
		t.Fatalf("did not expect replaceHostedRegion for non-hosted view")
	}
}

func TestBuildOpenWindowOptions_AppendOverrideDisablesReplacement(t *testing.T) {
	item := &ListItem{
		WindowKey:    "order",
		Presentation: "hosted",
		Region:       "chat.top",
		OpenMode:     "replace",
	}
	got := buildOpenWindowOptions(item, "conv-1", "append")
	if got["replaceHostedRegion"] != false {
		t.Fatalf("expected replaceHostedRegion=false for append override, got %#v", got["replaceHostedRegion"])
	}
}

func TestComputeWindowID_HostedViewsAreConversationScoped(t *testing.T) {
	item := &ListItem{
		WindowKey:    "order",
		Presentation: "hosted",
		Region:       "chat.top",
	}
	parameters := map[string]interface{}{
		"RecordId": []interface{}{2656980.0},
	}
	got := computeWindowID("order", parameters, "conv-1", item)
	expected := "order_" + fmt.Sprint(generateIntHash(parameters)) + "__conv-1"
	if got != expected {
		t.Fatalf("unexpected hosted window id: %s", got)
	}
}

func TestComputeWindowID_NonHostedViewsRemainUnscoped(t *testing.T) {
	item := &ListItem{
		WindowKey: "schedule",
	}
	got := computeWindowID("schedule", nil, "conv-1", item)
	if got != "schedule" {
		t.Fatalf("unexpected non-hosted window id: %s", got)
	}
}

func TestShouldRefreshOpenedWindow(t *testing.T) {
	if shouldRefreshOpenedWindow(nil, "win-1") {
		t.Fatalf("did not expect nil item to refresh")
	}
	if shouldRefreshOpenedWindow(&ListItem{Capabilities: viewproto.Capabilities{Datasource: true}}, "") {
		t.Fatalf("did not expect empty window id to refresh")
	}
	if shouldRefreshOpenedWindow(&ListItem{Presentation: "hosted"}, "win-1") {
		t.Fatalf("did not expect hosted view without datasource capability to refresh")
	}
	if shouldRefreshOpenedWindow(&ListItem{Presentation: "tab", Capabilities: viewproto.Capabilities{Datasource: true}}, "win-1") {
		t.Fatalf("did not expect non-hosted datasource view to refresh")
	}
	if !shouldRefreshOpenedWindow(&ListItem{Presentation: "hosted", Capabilities: viewproto.Capabilities{Datasource: true}}, "win-1") {
		t.Fatalf("expected hosted datasource view to refresh")
	}
}

func assertNestedValue(t *testing.T, holder map[string]interface{}, expected interface{}, parts ...string) {
	t.Helper()
	current := interface{}(holder)
	for _, part := range parts {
		asMap, ok := current.(map[string]interface{})
		if !ok {
			t.Fatalf("expected map at %q, got %#v", part, current)
		}
		current, ok = asMap[part]
		if !ok {
			t.Fatalf("missing nested key %q in %#v", part, asMap)
		}
	}
	if fmt.Sprintf("%#v", current) != fmt.Sprintf("%#v", expected) {
		t.Fatalf("unexpected bound value: got=%#v want=%#v", current, expected)
	}
}
