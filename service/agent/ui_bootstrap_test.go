package agent

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
	"net/http"
	"net/http/httptest"
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

func TestCurrentUIObservationUsesSelectedWindowAndCurrentChatOnly(t *testing.T) {
	client := &uireg.ClientSnapshot{UpdatedAt: time.UnixMilli(2000), Snapshot: &uireg.Snapshot{ObservedAt: 1500, ConversationID: "chat-a", Selected: uireg.SnapshotSelected{WindowID: "owner"}, Windows: []uireg.WindowSnapshot{{WindowID: "owner", WindowKey: "entity", ConversationID: "chat-a", DataSources: map[string]uireg.DataSourceSnapshot{"rows": {}}, SemanticContext: map[string]interface{}{"version": 1, "revision": 7, "tabs": []interface{}{map[string]interface{}{"containerId": "root", "tabId": "properties"}, map[string]interface{}{"containerId": "nested", "tabId": "advanced"}}, "sources": []interface{}{map[string]interface{}{"dataSourceRef": "rows", "selectedIds": []interface{}{97}, "filters": map[string]interface{}{"Name": "Visible", "password": "do-not-copy", "access_token": "do-not-copy", "opaque": "Bearer do-not-copy"}, "sort": []interface{}{map[string]interface{}{"field": "name", "direction": "asc"}}, "page": 2, "dirty": true, "changedFields": []interface{}{"name", "password"}, "form": map[string]interface{}{"name": "do-not-copy"}}}}}, {WindowID: "other", ConversationID: "chat-b", SemanticContext: map[string]interface{}{"version": 1}}}}}
	result := summarizeWorkspaceCurrentUI(client, "chat-a")
	for _, required := range []string{`"tabId":"advanced"`, `"selectedIds":[97]`, `"Name":"Visible"`, `"page":2`, `"dirty":true`, `"changedFields":["name"]`, `"observedAt":1500`, `"revision":7`} {
		if !strings.Contains(result, required) {
			t.Fatalf("missing current UI observation %s: %s", required, result)
		}
	}
	if strings.Contains(result, "do-not-copy") || strings.Contains(result, "password") || strings.Contains(result, "chat-b") {
		t.Fatal("sensitive/unrelated context leaked")
	}
	if got := summarizeWorkspaceCurrentUI(client, "chat-b"); got != "" {
		t.Fatalf("cross-chat UI observation: %s", got)
	}
	client.Snapshot.Selected.WindowID = "other"
	if got := summarizeWorkspaceCurrentUI(client, "chat-a"); got != "" {
		t.Fatalf("switched-chat window reused: %s", got)
	}
	client.Snapshot.Windows = nil
	if got := summarizeWorkspaceCurrentUI(client, "chat-a"); got != "" {
		t.Fatalf("deleted/closed window retained: %s", got)
	}
}

func TestWorkspaceUIBootstrapConsumesUIExposedCurrentContext(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	server := httptest.NewServer(http.HandlerFunc(bridge.Hub().ServeWS))
	defer server.Close()
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err = connection.WriteJSON(map[string]interface{}{"type": "ui.hello", "clientId": "observing-client"}); err != nil {
		t.Fatal(err)
	}
	snapshot := map[string]interface{}{"ts": 1500, "conversationId": "chat-current", "selected": map[string]interface{}{"windowId": "current"}, "windows": []interface{}{
		map[string]interface{}{"windowId": "current", "windowKey": "entity", "conversationId": "chat-current", "presentation": "hosted", "dataSources": map[string]interface{}{"rows": map[string]interface{}{}}, "semanticContext": map[string]interface{}{"version": 1, "revision": 3, "tabs": []interface{}{map[string]interface{}{"containerId": "root", "tabId": "properties"}, map[string]interface{}{"containerId": "nested", "tabId": "advanced"}}, "sources": []interface{}{map[string]interface{}{"dataSourceRef": "rows", "selectedIds": []interface{}{97}, "filters": map[string]interface{}{"Name": "Visible", "password": "private-value"}, "page": 2, "dirty": true}}}},
		map[string]interface{}{"windowId": "unrelated", "windowKey": "other-private-window", "conversationId": "chat-other", "presentation": "hosted"},
	}}
	if err = connection.WriteJSON(map[string]interface{}{"type": "ui.snapshot", "clientId": "observing-client", "data": snapshot}); err != nil {
		t.Fatal(err)
	}
	service := &Service{}
	service.SetUIBridge(bridge)
	var prompt string
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		prompt = service.workspaceUIBootstrap(context.Background(), "chat-current")
		if strings.Contains(prompt, "currentUIObservation") {
			break
		}
		time.Sleep(time.Millisecond)
	}
	for _, value := range []string{`"selectedIds":[97]`, `"tabId":"advanced"`, `"Name":"Visible"`, `"page":2`, `"dirty":true`, `"observedAt":1500`, "Metadata", "Read", "Operate"} {
		if !strings.Contains(prompt, value) {
			t.Fatalf("current UI missing from actual bootstrap: %s\n%s", value, prompt)
		}
	}
	if strings.Contains(prompt, "private-value") || strings.Contains(prompt, "other-private-window") {
		t.Fatal("sensitive or cross-chat state reached bootstrap")
	}
}
