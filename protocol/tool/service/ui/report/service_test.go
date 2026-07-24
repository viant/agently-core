package report

import (
	"bytes"
	"context"
	"encoding/json"
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
	var envelope map[string]interface{}
	if err := json.NewDecoder(rec.Result().Body).Decode(&envelope); err != nil {
		t.Fatalf("decode %s response: %v", method, err)
	}
	result, _ := envelope["result"].(map[string]interface{})
	return result
}

func TestServiceSaveDispatchesCanonicalBrowserAction(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	postUIRPC(t, bridge, "ui.hello", map[string]interface{}{"clientId": "client-1"})
	postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{
		"clientId": "client-1",
		"data": map[string]interface{}{
			"clientId":       "client-1",
			"conversationId": "conv-1",
			"windows": []interface{}{
				map[string]interface{}{
					"windowId":       "report-window-1",
					"windowKey":      "reportBuilder",
					"conversationId": "conv-1",
				},
			},
		},
	})

	service := New(bridge)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	done := make(chan error, 1)
	output := &ActionOutput{}
	go func() {
		done <- service.save(ctx, &WindowInput{WindowID: "report-window-1"}, output)
	}()

	poll := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": "client-1", "timeoutMs": 1000})
	command, _ := poll["params"].(map[string]interface{})
	if got := command["method"]; got != "ui.report.save" {
		t.Fatalf("expected ui.report.save command, got %#v", got)
	}
	postUIRPC(t, bridge, "ui.response", map[string]interface{}{
		"id": command["id"],
		"ok": true,
		"result": map[string]interface{}{
			"ok":         true,
			"artifactId": "artifact-1",
			"reportId":   "delivery-review",
		},
	})
	if err := <-done; err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if !output.OK || output.Result["artifactId"] != "artifact-1" || output.Result["reportId"] != "delivery-review" {
		t.Fatalf("unexpected save output: %#v", output)
	}
}

func TestServiceMethodsRegistered(t *testing.T) {
	service := &Service{}
	for _, name := range []string{"getCurrent", "run", "save"} {
		method, err := service.Method(name)
		if err != nil || method == nil {
			t.Fatalf("expected %s method, got method=%#v err=%v", name, method, err)
		}
	}
}
