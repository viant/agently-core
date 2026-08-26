package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

func TestSnapshotBelongsToConversation_UsesWindowConversationID(t *testing.T) {
	snapshot := &Snapshot{
		Windows: []WindowSnapshot{
			{
				WindowID:       "reportBuilder__conv-new",
				WindowKey:      "reportBuilder",
				ConversationID: "conv-new",
				Presentation:   "hosted",
				Region:         "chat.top",
				ParentKey:      "chat/new",
			},
		},
	}

	if !snapshotBelongsToConversation(snapshot, "conv-new") {
		t.Fatalf("expected window-level conversation id to own snapshot")
	}
}

func TestSnapshotBelongsToConversation_DoesNotMatchChatOnly(t *testing.T) {
	snapshot := &Snapshot{
		Windows: []WindowSnapshot{
			{
				WindowID:  "chat/new",
				WindowKey: "chat/new",
			},
		},
	}

	if snapshotBelongsToConversation(snapshot, "conv-new") {
		t.Fatalf("expected chat-only snapshot not to match arbitrary conversation")
	}
}

func TestFilterSnapshotForConversation_FiltersHostedWindowsByConversation(t *testing.T) {
	snapshot := &Snapshot{
		ConversationID: "conv-new",
		Selected:       SnapshotSelected{WindowID: "reportBuilder__conv-old"},
		Windows: []WindowSnapshot{
			{
				WindowID:    "chat/new",
				WindowKey:   "chat/new",
				WindowTitle: "Chat",
			},
			{
				WindowID:       "reportBuilder__conv-old",
				WindowKey:      "reportBuilder",
				WindowTitle:    "Performance Metrics",
				ConversationID: "conv-old",
				Presentation:   "hosted",
				Region:         "chat.top",
				ParentKey:      "chat/new",
				InTab:          true,
			},
			{
				WindowID:       "reportBuilder__conv-new",
				WindowKey:      "reportBuilder",
				WindowTitle:    "Performance Metrics",
				ConversationID: "conv-new",
				Presentation:   "hosted",
				Region:         "chat.top",
				ParentKey:      "chat/new",
				InTab:          true,
			},
		},
	}

	got := filterSnapshotForConversation(snapshot, "conv-new")
	if got == nil {
		t.Fatalf("expected filtered snapshot")
	}
	if len(got.Windows) != 2 {
		t.Fatalf("expected chat + current hosted window only, got %#v", got.Windows)
	}
	if got.Windows[1].WindowID != "reportBuilder__conv-new" {
		t.Fatalf("expected current conversation hosted window, got %#v", got.Windows[1])
	}
	if got.Selected.WindowID != "" {
		t.Fatalf("expected stale selected hosted window to be cleared, got %#v", got.Selected.WindowID)
	}
}

func TestFilterSnapshotForConversation_KeepsNonHostedWindowsVisible(t *testing.T) {
	snapshot := &Snapshot{
		ConversationID: "conv-new",
		Windows: []WindowSnapshot{
			{
				WindowID:    "chat/new",
				WindowKey:   "chat/new",
				WindowTitle: "Chat",
			},
			{
				WindowID:     "schedule",
				WindowKey:    "schedule",
				WindowTitle:  "Automation",
				Presentation: "",
			},
		},
	}

	got := filterSnapshotForConversation(snapshot, "conv-new")
	if got == nil {
		t.Fatalf("expected filtered snapshot")
	}
	if len(got.Windows) != 2 {
		t.Fatalf("expected non-hosted windows to stay visible, got %#v", got.Windows)
	}
}

func TestIsReadableClientAllowsStablePollingSnapshot(t *testing.T) {
	now := time.Now()
	item := ClientSnapshot{
		UpdatedAt:  now.Add(-time.Minute),
		LastPollAt: now,
		Transport:  "http",
		Snapshot:   &Snapshot{},
	}
	if !isReadableClient(item, now) {
		t.Fatalf("expected actively polling HTTP client with stable snapshot to stay readable")
	}
	if isFreshSnapshot(item, now) {
		t.Fatalf("expected fixture snapshot to be stale")
	}
	if !isServiceableClient(item, now) {
		t.Fatalf("expected fixture client to be serviceable")
	}
}

func TestIsReadableClientAllowsRegisteredSnapshotWithoutPoll(t *testing.T) {
	now := time.Now()
	item := ClientSnapshot{
		UpdatedAt: now.Add(-time.Minute),
		Snapshot:  &Snapshot{},
		Transport: "http",
	}
	if !isReadableClient(item, now) {
		t.Fatalf("expected registered read-only snapshot to stay readable")
	}
	if isFreshSnapshot(item, now) {
		t.Fatalf("expected fixture snapshot to be stale")
	}
	if isServiceableClient(item, now) {
		t.Fatalf("expected fixture client to be unavailable for command routing")
	}
}

func TestIsAttachedClientAllowsUnchangedSnapshotWithCurrentPoll(t *testing.T) {
	now := time.Now()
	item := ClientSnapshot{
		UpdatedAt:  now.Add(-time.Minute),
		LastPollAt: now,
		Transport:  "http",
		Snapshot:   &Snapshot{},
	}
	if !isAttachedClient(item, now) {
		t.Fatalf("expected unchanged actively polling client to remain attached")
	}
}

func TestIsServiceableClientOutlivesNativeLongPoll(t *testing.T) {
	now := time.Now()
	item := ClientSnapshot{
		LastPollAt: now.Add(-20 * time.Second),
		Transport:  "http",
		Snapshot:   &Snapshot{},
	}
	if !isServiceableClient(item, now) {
		t.Fatalf("expected a client inside its 20-second long poll to remain serviceable")
	}
	item.LastPollAt = now.Add(-31 * time.Second)
	if isServiceableClient(item, now) {
		t.Fatalf("expected a client beyond the reconnect margin to be stale")
	}
}

func TestIsAttachedClientRejectsStaleDisconnectedSnapshot(t *testing.T) {
	now := time.Now()
	item := ClientSnapshot{
		UpdatedAt:  now.Add(-time.Minute),
		LastPollAt: now.Add(-time.Minute),
		Transport:  "http",
		Snapshot:   &Snapshot{},
	}
	if isAttachedClient(item, now) {
		t.Fatalf("expected stale disconnected client not to remain attached")
	}
}

func TestListAttachedByConversationAcceptsFreshSnapshotBetweenPolls(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{
		"clientId": "web-client",
		"data": map[string]interface{}{
			"clientId":       "web-client",
			"conversationId": "conv-1",
			"windows": []interface{}{
				map[string]interface{}{
					"windowId":       "chat/new",
					"windowKey":      "chat/new",
					"conversationId": "conv-1",
				},
			},
		},
	})
	registry := New(bridge)
	attached, err := registry.ListAttachedByConversation(context.Background(), "conv-1")
	if err != nil {
		t.Fatalf("list attached clients: %v", err)
	}
	if len(attached) != 1 || attached[0].ClientID != "web-client" {
		t.Fatalf("expected fresh attached client, got %#v", attached)
	}
	serviceable, err := registry.ListByConversation(context.Background(), "conv-1")
	if err != nil {
		t.Fatalf("list serviceable clients: %v", err)
	}
	if len(serviceable) != 0 {
		t.Fatalf("expected no active poll before command delivery, got %#v", serviceable)
	}
}

func TestReadableSnapshotAcceptsCollectionMetricsPayload(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{
		"clientId": "web-client",
		"data": map[string]interface{}{
			"clientId":       "web-client",
			"conversationId": "conv-1",
			"windows": []interface{}{
				map[string]interface{}{
					"windowId":       "chat/new",
					"windowKey":      "chat/new",
					"conversationId": "conv-1",
					"dataSources": map[string]interface{}{
						"delivery": map[string]interface{}{
							"dataSourceRef": "delivery",
							"metrics": []interface{}{
								map[string]interface{}{"name": "spend", "value": 42},
							},
						},
					},
				},
			},
		},
	})

	readable, err := New(bridge).ListReadableByConversation(context.Background(), "conv-1")
	if err != nil {
		t.Fatalf("list readable clients: %v", err)
	}
	if len(readable) != 1 {
		t.Fatalf("expected collection-shaped metrics to preserve the snapshot, got %#v", readable)
	}
	metrics, ok := readable[0].Snapshot.Windows[0].DataSources["delivery"].Metrics.([]interface{})
	if !ok || len(metrics) != 1 {
		t.Fatalf("expected collection-shaped metrics payload, got %#v", readable[0].Snapshot.Windows[0].DataSources["delivery"].Metrics)
	}
}

func TestReadableSnapshotsDoNotRequireHTTPPollFreshness(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})

	postUIRPC(t, bridge, "ui.hello", map[string]interface{}{
		"clientId": "mobile-client",
	})
	postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{
		"clientId": "mobile-client",
		"data": map[string]interface{}{
			"clientId":       "mobile-client",
			"conversationId": "conv-1",
			"selected": map[string]interface{}{
				"windowId": "genericBuilder__conv-1",
			},
			"windows": []interface{}{
				map[string]interface{}{
					"windowId":       "chat/new",
					"windowKey":      "chat/new",
					"windowTitle":    "Chat",
					"conversationId": "conv-1",
				},
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

	reg := New(bridge)
	serviceable, err := reg.ListByConversation(context.Background(), "conv-1")
	if err != nil {
		t.Fatalf("list serviceable: %v", err)
	}
	if len(serviceable) != 0 {
		t.Fatalf("expected HTTP client without poll to be unavailable for command routing, got %#v", serviceable)
	}

	readable, err := reg.ListReadableByConversation(context.Background(), "conv-1")
	if err != nil {
		t.Fatalf("list readable: %v", err)
	}
	if len(readable) != 1 {
		t.Fatalf("expected fresh HTTP snapshot to be readable, got %#v", readable)
	}
	if readable[0].ClientID != "mobile-client" {
		t.Fatalf("expected mobile-client, got %q", readable[0].ClientID)
	}
	if len(readable[0].Snapshot.Windows) != 2 {
		t.Fatalf("expected chat plus generic builder windows, got %#v", readable[0].Snapshot.Windows)
	}

	_, _, _, win, err := reg.FindReadableWindow(context.Background(), "conv-1", "", "genericBuilder__conv-1", "")
	if err != nil {
		t.Fatalf("find readable window: %v", err)
	}
	if win.WindowID != "genericBuilder__conv-1" {
		t.Fatalf("expected generic builder window id, got %#v", win)
	}
	if _, _, _, _, err := reg.FindWindow(context.Background(), "conv-1", "", "genericBuilder__conv-1", ""); err == nil {
		t.Fatalf("expected command-routable lookup to require serviceable HTTP poll")
	}
}

func TestFindReadableWindowFallsBackToExactWindowIDWhenClientIDIsStale(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	postWindowSnapshot(t, bridge, "active-client", "conv-1", "genericBuilder__conv-1", "genericBuilder")

	reg := New(bridge)
	clientID, _, _, win, err := reg.FindReadableWindow(context.Background(), "conv-1", "stale-client", "genericBuilder__conv-1", "genericBuilder")
	if err != nil {
		t.Fatalf("find readable window: %v", err)
	}
	if clientID != "active-client" {
		t.Fatalf("expected active-client, got %q", clientID)
	}
	if win.WindowID != "genericBuilder__conv-1" {
		t.Fatalf("expected exact window id, got %#v", win)
	}
}

func TestFindWindowFallsBackToExactWindowIDWhenClientIDIsStale(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	postWindowSnapshot(t, bridge, "active-client", "conv-1", "genericBuilder__conv-1", "genericBuilder")
	postUIRPC(t, bridge, "ui.poll", map[string]interface{}{
		"clientId":  "active-client",
		"timeoutMs": 1,
	})

	reg := New(bridge)
	clientID, _, _, win, err := reg.FindWindow(context.Background(), "conv-1", "stale-client", "genericBuilder__conv-1", "genericBuilder")
	if err != nil {
		t.Fatalf("find window: %v", err)
	}
	if clientID != "active-client" {
		t.Fatalf("expected active-client, got %q", clientID)
	}
	if win.WindowID != "genericBuilder__conv-1" {
		t.Fatalf("expected exact window id, got %#v", win)
	}
}

func TestListEventsReturnsExplicitClientEventsWithoutSnapshot(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	reg := New(bridge)
	reg.RecordEvent("", "mobile-client-no-snapshot", UIEvent{
		ConversationID: "conv-invalid",
		ClientID:       "mobile-client-no-snapshot",
		Kind:           "error",
		Actor:          "agent",
		Detail: map[string]interface{}{
			"payload": map[string]interface{}{
				"invalidWorkspaceId": "legacyAlias",
			},
		},
	})

	events := reg.ListEvents("conv-invalid", "mobile-client-no-snapshot", "", "", 10, 0)
	if len(events) != 1 {
		t.Fatalf("expected one explicit-client event, got %#v", events)
	}
	if events[0].Kind != "error" {
		t.Fatalf("expected error event, got %#v", events[0])
	}
	payload, ok := events[0].Detail["payload"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected payload map, got %#v", events[0].Detail)
	}
	if payload["invalidWorkspaceId"] != "legacyAlias" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestListEventsFallsBackToExactWindowIDWhenClientIDIsStale(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	postWindowSnapshot(t, bridge, "active-client", "conv-1", "genericBuilder__conv-1", "genericBuilder")
	reg := New(bridge)
	reg.RecordEvent("default", "active-client", UIEvent{
		ConversationID: "conv-1",
		ClientID:       "active-client",
		WindowID:       "genericBuilder__conv-1",
		WindowKey:      "genericBuilder",
		Kind:           "control.set_value",
		Actor:          "agent",
	})

	events := reg.ListEvents("conv-1", "stale-client", "genericBuilder__conv-1", "genericBuilder", 10, 0)
	if len(events) != 1 {
		t.Fatalf("expected one exact-window fallback event, got %#v", events)
	}
	if events[0].ClientID != "active-client" || events[0].WindowID != "genericBuilder__conv-1" {
		t.Fatalf("expected active exact-window event, got %#v", events[0])
	}
}

func TestListEventsDoesNotFallbackByWindowKeyWhenClientIDIsStale(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	postWindowSnapshot(t, bridge, "active-client", "conv-1", "genericBuilder__conv-1", "genericBuilder")
	reg := New(bridge)
	reg.RecordEvent("default", "active-client", UIEvent{
		ConversationID: "conv-1",
		ClientID:       "active-client",
		WindowID:       "genericBuilder__conv-1",
		WindowKey:      "genericBuilder",
		Kind:           "control.set_value",
		Actor:          "agent",
	})

	events := reg.ListEvents("conv-1", "stale-client", "", "genericBuilder", 10, 0)
	if len(events) != 0 {
		t.Fatalf("expected no window-key-only stale client fallback, got %#v", events)
	}
}

func TestFindWindowDoesNotFallbackByWindowKeyWhenClientIDIsStale(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	postWindowSnapshot(t, bridge, "active-client", "conv-1", "genericBuilder__conv-1", "genericBuilder")
	postUIRPC(t, bridge, "ui.poll", map[string]interface{}{
		"clientId":  "active-client",
		"timeoutMs": 1,
	})

	reg := New(bridge)
	_, _, _, _, err := reg.FindWindow(context.Background(), "conv-1", "stale-client", "", "genericBuilder")
	if err == nil {
		t.Fatalf("expected stale client id with windowKey-only lookup not to cross clients")
	}
}

func TestFindWindowFallsBackToRecentViewOpenEventWhenSnapshotIsMissing(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	reg := New(bridge)
	reg.RecordEvent("default", "active-client", UIEvent{
		ConversationID: "conv-1",
		ClientID:       "active-client",
		WindowID:       "forecastingCubeBuilder__conv-1",
		WindowKey:      "forecastingCubeBuilder",
		Kind:           "view.open",
		Actor:          "agent",
		At:             time.Now(),
	})

	clientID, namespace, _, win, err := reg.FindWindow(context.Background(), "conv-1", "active-client", "forecastingCubeBuilder__conv-1", "forecastingCubeBuilder")
	if err != nil {
		t.Fatalf("find window from event fallback: %v", err)
	}
	if clientID != "active-client" {
		t.Fatalf("expected active-client, got %q", clientID)
	}
	if namespace != "default" {
		t.Fatalf("expected default namespace, got %q", namespace)
	}
	if win == nil || win.WindowID != "forecastingCubeBuilder__conv-1" || win.WindowKey != "forecastingCubeBuilder" {
		t.Fatalf("unexpected window fallback: %#v", win)
	}
}

func postWindowSnapshot(t *testing.T, bridge *forgeuisvc.Service, clientID, conversationID, windowID, windowKey string) {
	t.Helper()
	postUIRPC(t, bridge, "ui.hello", map[string]interface{}{"clientId": clientID})
	postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{
		"clientId": clientID,
		"data": map[string]interface{}{
			"clientId":       clientID,
			"conversationId": conversationID,
			"selected": map[string]interface{}{
				"windowId": windowID,
			},
			"windows": []interface{}{
				map[string]interface{}{
					"windowId":       windowID,
					"windowKey":      windowKey,
					"windowTitle":    "Generic Builder",
					"conversationId": conversationID,
					"presentation":   "hosted",
					"region":         "chat.top",
					"parentKey":      "chat/new",
				},
			},
		},
	})
}

func postUIRPC(t *testing.T, bridge *forgeuisvc.Service, method string, params map[string]interface{}) {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      method,
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("post %s status: %s", method, resp.Status)
	}
}
