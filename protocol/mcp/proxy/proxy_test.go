package proxy

import "testing"

func TestNormalizeToolName_MultiSegmentDisplayNamespace(t *testing.T) {
	tests := []struct {
		server string
		name   string
		want   string
	}{
		{server: "inventory_planner", name: "inventory:planner/get_plan", want: "get_plan"},
		{server: "inventory_planner", name: "inventory/planner/get_plan", want: "get_plan"},
		{server: "inventory-planner", name: "inventory-planner:get_plan", want: "get_plan"},
		{server: "other", name: "inventory:planner/get_plan", want: "inventory:planner/get_plan"},
	}
	for _, test := range tests {
		if got := normalizeToolName(test.server, test.name); got != test.want {
			t.Errorf("normalizeToolName(%q, %q) = %q, want %q", test.server, test.name, got, test.want)
		}
	}
}
