package events

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
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

func seedEventWindow(t *testing.T, bridge *forgeuisvc.Service) {
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
				},
			},
		},
	})
}

func TestListFallsBackToExactWindowIDWhenClientIDIsStale(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	seedEventWindow(t, bridge)
	svc := New(bridge)
	svc.reg.RecordEvent("default", "active-client", uireg.UIEvent{
		ConversationID: "conv-1",
		ClientID:       "active-client",
		WindowID:       "genericBuilder__conv-1",
		WindowKey:      "genericBuilder",
		Kind:           "control.set_value",
		Actor:          "agent",
	})

	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	out := &ListOutput{}
	if err := svc.list(ctx, &ListInput{
		ClientID:  "stale-client",
		WindowID:  "genericBuilder__conv-1",
		WindowKey: "genericBuilder",
		Kinds:     []string{"control.set_value"},
		Limit:     10,
	}, out); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(out.Events) != 1 {
		t.Fatalf("expected one exact-window fallback event, got %#v", out.Events)
	}
	if out.Events[0].ClientID != "active-client" || out.Events[0].WindowID != "genericBuilder__conv-1" {
		t.Fatalf("expected active exact-window event, got %#v", out.Events[0])
	}
}
