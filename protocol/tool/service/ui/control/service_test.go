package control

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

func TestServiceMethod_SetValueRegistered(t *testing.T) {
	svc := &Service{}
	method, err := svc.Method("setValue")
	if err != nil {
		t.Fatalf("expected setValue method to resolve, got %v", err)
	}
	if method == nil {
		t.Fatalf("expected setValue method implementation")
	}
}

func TestNormalizeOptionalClientID(t *testing.T) {
	if got := normalizeOptionalClientID(" default "); got != "" {
		t.Fatalf("expected default client id to normalize empty, got %q", got)
	}
	if got := normalizeOptionalClientID("client-1"); got != "client-1" {
		t.Fatalf("expected client id to survive, got %q", got)
	}
}

func TestSetValueFallsBackToExactWindowIDWhenClientIDIsStale(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
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

	svc := New(bridge)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	done := make(chan error, 1)
	go func() {
		out := &CommandOutput{}
		err := svc.setValue(ctx, &SetValueInput{
			ClientID:    "stale-client",
			WindowID:    "genericBuilder__conv-1",
			WindowKey:   "genericBuilder",
			ControlID:   "country",
			Scope:       "windowForm",
			BindingPath: "prefill.includeCountry",
			Value:       []interface{}{"US"},
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
	if got := command["method"]; got != "ui.control.setValue" {
		t.Fatalf("expected ui.control.setValue, got %#v", got)
	}
	commandParams, ok := command["params"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected command params map, got %#v", command["params"])
	}
	if got := commandParams["windowId"]; got != "genericBuilder__conv-1" {
		t.Fatalf("expected generic builder window id, got %#v", got)
	}
	if got := commandParams["controlId"]; got != "country" {
		t.Fatalf("expected country control id, got %#v", got)
	}

	postUIRPC(t, bridge, "ui.response", map[string]interface{}{
		"id":     command["id"],
		"ok":     true,
		"result": map[string]interface{}{"ok": true},
	})
	if err := <-done; err != nil {
		t.Fatalf("setValue failed: %v", err)
	}
}
