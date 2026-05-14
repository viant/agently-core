package view

import (
	"testing"

	viewproto "github.com/viant/agently-core/protocol/ui/view"
)

func TestExpandOpenParametersBindsOneInputToMultipleTargets(t *testing.T) {
	specParams := []viewproto.Parameter{
		{Name: "AdOrderId", BindTo: "order_performance_profile.parameters.AdOrderId"},
		{Name: "AdOrderId", BindTo: "order_performance_period_today.parameters.AdOrderId"},
		{Name: "AdOrderId", BindTo: "order_performance_period_yesterday.parameters.AdOrderId"},
		{Name: "AdOrderId", BindTo: "order_performance_period_7d.parameters.AdOrderId"},
		{Name: "AdOrderId", BindTo: "order_performance_period_30d.parameters.AdOrderId"},
	}

	actual := expandOpenParameters(specParams, map[string]interface{}{
		"AdOrderId": []interface{}{2664124.0},
	})

	assertNestedValue(t, actual, "order_performance_profile", "parameters", "AdOrderId")
	assertNestedValue(t, actual, "order_performance_period_today", "parameters", "AdOrderId")
	assertNestedValue(t, actual, "order_performance_period_yesterday", "parameters", "AdOrderId")
	assertNestedValue(t, actual, "order_performance_period_7d", "parameters", "AdOrderId")
	assertNestedValue(t, actual, "order_performance_period_30d", "parameters", "AdOrderId")
}

func TestExpandOpenParametersPreservesUnboundParameters(t *testing.T) {
	specParams := []viewproto.Parameter{
		{Name: "AdOrderId", BindTo: "order_performance_profile.parameters.AdOrderId"},
	}

	actual := expandOpenParameters(specParams, map[string]interface{}{
		"AdOrderId": []interface{}{2664124.0},
		"ClientID":  "client-1",
	})

	if actual["ClientID"] != "client-1" {
		t.Fatalf("expected passthrough ClientID, got %#v", actual["ClientID"])
	}
}

func TestMissingRequiredParameters(t *testing.T) {
	specParams := []viewproto.Parameter{
		{Name: "AdOrderId", Required: true, BindTo: "order_performance_profile.parameters.AdOrderId"},
		{Name: "AdOrderId", Required: true, BindTo: "order_performance_period_today.parameters.AdOrderId"},
	}

	if missing := missingRequiredParameters(specParams, nil); len(missing) != 1 || missing[0] != "AdOrderId" {
		t.Fatalf("expected AdOrderId to be reported missing, got %#v", missing)
	}

	if missing := missingRequiredParameters(specParams, map[string]interface{}{
		"AdOrderId": []interface{}{2664124.0},
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

func assertNestedValue(t *testing.T, holder map[string]interface{}, parts ...string) {
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
	ids, ok := current.([]interface{})
	if !ok || len(ids) != 1 || ids[0] != 2664124.0 {
		t.Fatalf("unexpected bound value: %#v", current)
	}
}
