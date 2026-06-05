package view

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/viant/afs"
	windowtool "github.com/viant/agently-core/protocol/tool/service/ui/window"
	viewproto "github.com/viant/agently-core/protocol/ui/view"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	"github.com/viant/agently-core/workspace"
	repo "github.com/viant/agently-core/workspace/repository/forgewindow"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
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

func TestBuildOpenWindowOptions_PreservesWorkspaceLayoutHints(t *testing.T) {
	item := &ListItem{
		WindowKey:          "order",
		Presentation:       "hosted",
		Region:             "chat.top",
		WorkspaceSharePct:  72,
		WorkspaceMinHeight: 500,
	}
	got := buildOpenWindowOptions(item, "conv-1", "")
	if got["workspaceSharePct"] != 72 {
		t.Fatalf("expected workspaceSharePct=72, got %#v", got["workspaceSharePct"])
	}
	if got["workspaceMinHeight"] != 500 {
		t.Fatalf("expected workspaceMinHeight=500, got %#v", got["workspaceMinHeight"])
	}
}

func TestServiceLoadAll_PreservesWorkspaceLayoutHintsFromSpec(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "order.yaml"), `
id: order
title: Order Summary
windowKey: order
presentation: hosted
region: chat.top
workspaceSharePct: 72
workspaceMinHeight: 500
`)
		svc := &Service{
			repo: repo.New(afs.New()),
		}
		items, err := svc.loadAll(context.Background())
		if err != nil {
			t.Fatalf("loadAll failed: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("expected one view item, got %#v", items)
		}
		if items[0].WorkspaceSharePct != 72 {
			t.Fatalf("expected workspaceSharePct=72, got %#v", items[0].WorkspaceSharePct)
		}
		if items[0].WorkspaceMinHeight != 500 {
			t.Fatalf("expected workspaceMinHeight=500, got %#v", items[0].WorkspaceMinHeight)
		}
	})
}

func TestServiceLoadAll_LoadsImportOnlyForgeViewSpecs(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "order.yaml"), `
$import(order/shared/main.yaml)
`)
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "order", "shared", "main.yaml"), `
$import(web/main.yaml)
`)
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "order", "shared", "web", "main.yaml"), `
id: order
title: Order Summary
windowKey: order
presentation: hosted
region: chat.top
workspaceSharePct: 72
workspaceMinHeight: 500
`)
		svc := &Service{
			repo: repo.New(afs.New()),
		}
		items, err := svc.loadAll(context.Background())
		if err != nil {
			t.Fatalf("loadAll failed: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("expected one view item, got %#v", items)
		}
		if items[0].ID != "order" || items[0].WindowKey != "order" {
			t.Fatalf("unexpected item: %#v", items[0])
		}
	})
}

func TestOpenReturnsWindowIdVisibleToWindowList(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "forecastingCubeBuilder.yaml"), `
id: forecastingCubeBuilder
title: Forecasting
windowKey: forecastingCubeBuilder
presentation: hosted
region: chat.top
`)
		const conversationID = "conv-forecast"
		const clientID = "web-client-1"

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
			"clientId": clientID,
		}); err != nil {
			t.Fatalf("write hello: %v", err)
		}
		if err := conn.WriteJSON(map[string]interface{}{
			"type":     "ui.snapshot",
			"clientId": clientID,
			"data": map[string]interface{}{
				"clientId":       clientID,
				"conversationId": conversationID,
				"selected": map[string]interface{}{
					"windowId": "chat/new",
					"tabId":    "chat/new",
				},
				"windows": []interface{}{
					map[string]interface{}{
						"windowId":       "chat/new",
						"windowKey":      "chat/new",
						"windowTitle":    "Chat",
						"conversationId": conversationID,
					},
				},
			},
		}); err != nil {
			t.Fatalf("write initial snapshot: %v", err)
		}
		waitForSnapshotEntry(t, bridge, clientID)

		commandDone := make(chan error, 1)
		go func() {
			var request map[string]interface{}
			if err := conn.ReadJSON(&request); err != nil {
				commandDone <- err
				return
			}
			if got := request["method"]; got != "ui.window.open" {
				commandDone <- fmt.Errorf("expected ui.window.open command, got %#v", got)
				return
			}
			params, ok := request["params"].(map[string]interface{})
			if !ok {
				commandDone <- fmt.Errorf("expected params map, got %#v", request["params"])
				return
			}
			windowID := strings.TrimSpace(stringValue(params["windowId"]))
			windowKey := strings.TrimSpace(stringValue(params["windowKey"]))
			windowTitle := strings.TrimSpace(stringValue(params["windowTitle"]))
			if windowID == "" || windowKey == "" {
				commandDone <- fmt.Errorf("expected window id/key in params, got %#v", params)
				return
			}
			if err := conn.WriteJSON(map[string]interface{}{
				"type":     "ui.snapshot",
				"clientId": clientID,
				"data": map[string]interface{}{
					"clientId": clientID,
					"selected": map[string]interface{}{
						"windowId": windowID,
						"tabId":    windowID,
					},
					"windows": []interface{}{
						map[string]interface{}{
							"windowId":       "chat/new",
							"windowKey":      "chat/new",
							"windowTitle":    "Chat",
							"conversationId": conversationID,
						},
						map[string]interface{}{
							"windowId":       windowID,
							"windowKey":      windowKey,
							"windowTitle":    windowTitle,
							"conversationId": conversationID,
							"presentation":   "hosted",
							"region":         "chat.top",
							"parentKey":      "chat/new",
						},
					},
				},
			}); err != nil {
				commandDone <- err
				return
			}
			if err := conn.WriteJSON(map[string]interface{}{
				"id": request["id"],
				"ok": true,
				"result": map[string]interface{}{
					"windowId": windowID,
				},
			}); err != nil {
				commandDone <- err
				return
			}
			commandDone <- nil
		}()

		ctx := runtimerequestctx.WithPreferredUIClientID(
			runtimerequestctx.WithConversationID(context.Background(), conversationID),
			clientID,
		)
		viewSvc := New(repo.New(afs.New()), bridge)
		openOut := &OpenOutput{}
		if err := viewSvc.open(ctx, &OpenInput{ID: "forecastingCubeBuilder", TimeoutMs: 2_000}, openOut); err != nil {
			t.Fatalf("open failed: %v", err)
		}
		if err := <-commandDone; err != nil {
			t.Fatalf("bridge command handling failed: %v", err)
		}
		if openOut.WindowID == "" {
			t.Fatalf("expected open to return window id")
		}

		windowSvc := windowtool.New(bridge)
		listMethod, err := windowSvc.Method("list")
		if err != nil {
			t.Fatalf("resolve window list method: %v", err)
		}
		listOut := &windowtool.ListOutput{}
		if err := listMethod(ctx, &windowtool.ListInput{ClientID: clientID}, listOut); err != nil {
			t.Fatalf("window list failed: %v", err)
		}
		for _, item := range listOut.Items {
			if item.WindowID == openOut.WindowID {
				return
			}
		}
		t.Fatalf("open returned %q, but list returned %#v", openOut.WindowID, listOut.Items)
	})
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

func TestClientNamespaceFromSnapshotsUsesExactClient(t *testing.T) {
	clients := []uireg.ClientSnapshot{
		{ClientID: "web-client", Namespace: "web-ns"},
		{ClientID: "mobile-client", Namespace: "mobile-ns"},
	}

	if got := clientNamespaceFromSnapshots(clients, " mobile-client "); got != "mobile-ns" {
		t.Fatalf("expected mobile namespace, got %q", got)
	}
}

func TestClientNamespaceFromSnapshotsReturnsEmptyForMissingClient(t *testing.T) {
	clients := []uireg.ClientSnapshot{
		{ClientID: "web-client", Namespace: "web-ns"},
	}

	if got := clientNamespaceFromSnapshots(clients, "mobile-client"); got != "" {
		t.Fatalf("expected no namespace for missing client, got %q", got)
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

func withWorkspaceRoot(t *testing.T, body func(root string)) {
	t.Helper()
	prev := workspace.Root()
	root := t.TempDir()
	workspace.SetRoot(root)
	t.Cleanup(func() {
		workspace.SetRoot(prev)
	})
	body(root)
}

func waitForSnapshotEntry(t *testing.T, bridge *forgeuisvc.Service, clientID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, entry := range bridge.Hub().SnapshotEntries() {
			if entry.ClientID == clientID {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("snapshot for client %q did not become visible", clientID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func mustWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimLeft(contents, "\n")), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
