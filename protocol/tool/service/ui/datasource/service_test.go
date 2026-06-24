package datasource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

func postUIRPC(t *testing.T, bridge *forgeuisvc.Service, method string, params map[string]interface{}) map[string]interface{} {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "test-" + method,
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
	var envelope map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode %s response: %v", method, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("%s returned HTTP %d: %#v", method, resp.StatusCode, envelope)
	}
	if errValue := envelope["error"]; errValue != nil {
		t.Fatalf("%s returned RPC error: %#v", method, errValue)
	}
	result, _ := envelope["result"].(map[string]interface{})
	return result
}

func seedActiveWindow(t *testing.T, bridge *forgeuisvc.Service) {
	t.Helper()
	postUIRPC(t, bridge, "ui.hello", map[string]interface{}{"clientId": "active-client"})
	postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{
		"clientId": "active-client",
		"data": map[string]interface{}{
			"clientId":       "active-client",
			"conversationId": "conv-1",
			"selected": map[string]interface{}{
				"windowId": "genericBuilder__conv-1",
			},
			"windows": []interface{}{
				map[string]interface{}{
					"windowId":       "genericBuilder__conv-1",
					"windowKey":      "genericBuilder",
					"windowTitle":    "Generic Builder",
					"conversationId": "conv-1",
					"presentation":   "hosted",
					"region":         "chat.top",
					"parentKey":      "chat/new",
					"dataSources": map[string]interface{}{
						"forecast_rows": map[string]interface{}{
							"dataSourceRef": "forecast_rows",
							"rows": []interface{}{
								map[string]interface{}{"country": "US", "avails": 123},
							},
						},
					},
				},
			},
		},
	})
}

func TestListAndPeekFallBackToExactWindowIDWhenClientIDIsStale(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	seedActiveWindow(t, bridge)

	svc := New(bridge)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	listOut := &ListOutput{}
	if err := svc.list(ctx, &ListInput{
		ClientID:  "stale-client",
		WindowID:  "genericBuilder__conv-1",
		WindowKey: "genericBuilder",
	}, listOut); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if listOut.ClientID != "active-client" || listOut.WindowID != "genericBuilder__conv-1" {
		t.Fatalf("expected active exact window list result, got %#v", listOut)
	}
	if len(listOut.DataSourceRefs) != 1 || listOut.DataSourceRefs[0] != "forecast_rows" {
		t.Fatalf("expected forecast_rows datasource ref, got %#v", listOut.DataSourceRefs)
	}

	peekOut := &PeekOutput{}
	if err := svc.peek(ctx, &PeekInput{
		ClientID:      "stale-client",
		WindowID:      "genericBuilder__conv-1",
		WindowKey:     "genericBuilder",
		DataSourceRef: "forecast_rows",
	}, peekOut); err != nil {
		t.Fatalf("peek failed: %v", err)
	}
	if peekOut.ClientID != "active-client" || peekOut.WindowID != "genericBuilder__conv-1" || peekOut.Snapshot == nil {
		t.Fatalf("expected active exact window peek result, got %#v", peekOut)
	}
}

func TestRefreshFallsBackToExactWindowIDWhenClientIDIsStale(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	seedActiveWindow(t, bridge)

	svc := New(bridge)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	done := make(chan error, 1)
	go func() {
		out := &CommandOutput{}
		err := svc.refresh(ctx, &RefreshInput{
			ClientID:      "stale-client",
			WindowID:      "genericBuilder__conv-1",
			WindowKey:     "genericBuilder",
			DataSourceRef: "forecast_rows",
		}, out)
		if err != nil {
			done <- err
			return
		}
		if !out.OK || out.ClientID != "active-client" {
			done <- fmt.Errorf("expected command routed to active-client, got %#v", out)
			return
		}
		done <- nil
	}()

	result := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{
		"clientId":  "active-client",
		"timeoutMs": 1000,
	})
	command, ok := result["params"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected command params, got %#v", result["params"])
	}
	if got := command["method"]; got != "ui.data.fetch" {
		t.Fatalf("expected ui.data.fetch, got %#v", got)
	}
	commandParams, ok := command["params"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected command params map, got %#v", command["params"])
	}
	if got := commandParams["windowId"]; got != "genericBuilder__conv-1" {
		t.Fatalf("expected generic builder window id, got %#v", got)
	}
	if got := commandParams["dataSourceRef"]; got != "forecast_rows" {
		t.Fatalf("expected forecast_rows datasource ref, got %#v", got)
	}

	postUIRPC(t, bridge, "ui.response", map[string]interface{}{
		"id":     command["id"],
		"ok":     true,
		"result": map[string]interface{}{"ok": true},
	})
	if err := <-done; err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
}
