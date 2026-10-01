package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	uireg "github.com/viant/agently-core/service/ui/window/registry"
)

func TestSummarizeWorkspaceSelectionTargetsBoundsAndRedactsRows(t *testing.T) {
	win := &uireg.WindowSnapshot{DataSources: map[string]uireg.DataSourceSnapshot{
		"order_audiences": {Collection: []interface{}{
			map[string]interface{}{"id": 7425336, "name": "Default Line", "email": "private@example.com"},
			map[string]interface{}{"id": 2, "name": "Second Line"},
			map[string]interface{}{"id": 3, "name": "Third Line"},
			map[string]interface{}{"id": 4, "name": "Fourth Line"},
		}},
		"other": {Collection: []interface{}{map[string]interface{}{"name": "No ID"}}},
	}}
	result := summarizeWorkspaceSelectionTargets(win)
	if strings.Contains(result, "private@example.com") || strings.Contains(result, "Fourth Line") || strings.Contains(result, "No ID") {
		t.Fatalf("selection summary leaked unrelated or unbounded row data: %s", result)
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0]["dataSourceRef"] != "order_audiences" || rows[0]["id"] != float64(7425336) {
		t.Fatalf("unexpected selection targets: %#v", rows)
	}
}

func TestSelectWorkspaceUIBootstrapClientPrefersRequestClient(t *testing.T) {
	items := []uireg.ClientSnapshot{
		{
			ClientID:  "web-client",
			Snapshot:  &uireg.Snapshot{ClientID: "web-client"},
			UpdatedAt: time.Now(),
		},
		{
			ClientID:  "mobile-client",
			Snapshot:  &uireg.Snapshot{ClientID: "mobile-client"},
			UpdatedAt: time.Now().Add(-time.Second),
		},
	}

	got := selectWorkspaceUIBootstrapClient(items, " mobile-client ")

	if got == nil || got.ClientID != "mobile-client" {
		t.Fatalf("expected preferred mobile client, got %#v", got)
	}
}

func TestSelectWorkspaceUIBootstrapClientDoesNotFallbackWhenPreferredMissing(t *testing.T) {
	items := []uireg.ClientSnapshot{
		{
			ClientID:  "web-client",
			Snapshot:  &uireg.Snapshot{ClientID: "web-client"},
			UpdatedAt: time.Now(),
		},
	}

	if got := selectWorkspaceUIBootstrapClient(items, "mobile-client"); got != nil {
		t.Fatalf("expected no snapshot for missing preferred client, got %#v", got)
	}
}

func TestSelectWorkspaceUIBootstrapClientUsesFirstAvailableWithoutPreferredClient(t *testing.T) {
	items := []uireg.ClientSnapshot{
		{
			ClientID:  "web-client",
			Snapshot:  &uireg.Snapshot{ClientID: "web-client"},
			UpdatedAt: time.Now(),
		},
		{
			ClientID:  "mobile-client",
			Snapshot:  &uireg.Snapshot{ClientID: "mobile-client"},
			UpdatedAt: time.Now().Add(-time.Second),
		},
	}

	got := selectWorkspaceUIBootstrapClient(items, "")

	if got == nil || got.ClientID != "web-client" {
		t.Fatalf("expected first available client without preference, got %#v", got)
	}
}

func TestSummarizeWorkspaceControlIncludesStableIDAndOptions(t *testing.T) {
	got := summarizeWorkspaceControl(uireg.SurfaceControl{
		ID: "advertiserListMode", Label: "View", Scope: "windowForm",
		Options: []uireg.SurfaceControlOption{
			{Value: "all", Label: "All advertisers"},
			{Value: "starred", Label: "Starred only"},
		},
	})
	if got != "advertiserListMode(View):windowForm[all:All advertisers|starred:Starred only]" {
		t.Fatalf("unexpected control summary: %q", got)
	}
}
