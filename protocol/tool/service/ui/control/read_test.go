package control

import (
	"context"
	"encoding/json"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
	"strings"
	"testing"
	"time"
)

func controlReadFixture(t *testing.T) (*forgeuisvc.Service, *Service, context.Context) {
	t.Helper()
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	postUIRPC(t, bridge, "ui.hello", map[string]interface{}{"clientId": "read-client"})
	postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{"clientId": "read-client", "data": map[string]interface{}{"conversationId": "read-chat", "windows": []interface{}{map[string]interface{}{"windowId": "read-window", "windowKey": "entity", "conversationId": "read-chat", "presentation": "hosted", "operationRevision": 17, "metadata": map[string]interface{}{"view": map[string]interface{}{"controls": []interface{}{map[string]interface{}{"id": "opaque", "type": "password", "scope": "windowForm"}}}}}}}})
	postUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": "read-client", "timeoutMs": 1})
	return bridge, New(bridge), requestctx.WithPreferredUIClientID(requestctx.WithConversationID(context.Background(), "read-chat"), "read-client")
}
func TestControlReadMethodsAreRegisteredAndScoped(t *testing.T) {
	_, service, ctx := controlReadFixture(t)
	for _, method := range []string{"get", "list"} {
		if _, err := service.Method(method); err != nil {
			t.Fatal(err)
		}
	}
	stale := uint32(16)
	for _, input := range []ListInput{{ReadTarget: ReadTarget{WindowID: "read-window", ConversationID: "foreign"}}, {ReadTarget: ReadTarget{WindowID: "read-window", ExpectedRevision: &stale}}, {ReadTarget: ReadTarget{WindowID: "foreign-window"}}} {
		if err := service.listControls(ctx, &input, &ReadOutput{}); err == nil {
			t.Fatal("foreign/stale read accepted")
		}
	}
	if err := service.getControl(ctx, &GetInput{ReadTarget: ReadTarget{WindowID: "read-window"}}, &ReadOutput{}); err == nil {
		t.Fatal("missing control identity accepted")
	}
	if err := service.listControls(context.Background(), &ListInput{ReadTarget: ReadTarget{WindowID: "read-window"}}, &ReadOutput{}); err == nil {
		t.Fatal("unscoped read accepted")
	}
}
func TestControlGetUsesPassiveScopedReadAndRedactsHostSensitiveValues(t *testing.T) {
	bridge, service, ctx := controlReadFixture(t)
	var output ReadOutput
	done := make(chan error, 1)
	go func() {
		done <- service.getControl(ctx, &GetInput{ReadTarget: ReadTarget{WindowID: "read-window"}, ControlID: "opaque"}, &output)
	}()
	response := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": "read-client", "timeoutMs": 1000})
	command := response["params"].(map[string]interface{})
	params := command["params"].(map[string]interface{})
	if command["method"] != "ui.controls.list" || params["windowId"] != "read-window" || params["conversationId"] != "read-chat" || params["controlId"] != "opaque" || params["limit"] != float64(2) {
		t.Fatalf("incorrect passive Read command: %#v", command)
	}
	postUIRPC(t, bridge, "ui.response", map[string]interface{}{"id": command["id"], "ok": true, "result": map[string]interface{}{"operationRevision": 18, "offset": 0, "limit": 2, "total": 1, "hasMore": false, "controls": []interface{}{map[string]interface{}{"controlId": "opaque", "type": "text", "value": "opaque-sensitive-value", "visible": true, "disabled": false, "readOnly": false, "supportedActions": []string{}}}}})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Read did not settle")
	}
	raw, _ := json.Marshal(output)
	if strings.Contains(string(raw), "opaque-sensitive-value") || !output.ReadRedacted || output.OperationRevision != 18 || output.WindowID != "read-window" || len(output.Controls) != 1 {
		t.Fatalf("unsafe or wrong current Read output: %s", raw)
	}
}
func TestControlListBoundsPaginationAndKeepsSafeValues(t *testing.T) {
	bridge, service, ctx := controlReadFixture(t)
	var output ReadOutput
	done := make(chan error, 1)
	go func() {
		done <- service.listControls(ctx, &ListInput{ReadTarget: ReadTarget{WindowID: "read-window"}, Offset: 2, Limit: 500}, &output)
	}()
	response := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": "read-client", "timeoutMs": 1000})
	command := response["params"].(map[string]interface{})
	params := command["params"].(map[string]interface{})
	if params["offset"] != float64(2) || params["limit"] != float64(50) {
		t.Fatalf("unbounded Read: %#v", params)
	}
	postUIRPC(t, bridge, "ui.response", map[string]interface{}{"id": command["id"], "ok": true, "result": map[string]interface{}{"operationRevision": 19, "offset": 2, "limit": 50, "total": 4, "hasMore": false, "controls": []interface{}{map[string]interface{}{"controlId": "name", "value": "Visible", "visible": true, "disabled": false, "readOnly": false, "supportedActions": []string{"setValue"}}}}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if output.Controls[0].Value != "Visible" || output.OperationRevision != 19 || output.Total != 4 || output.Offset != 2 {
		t.Fatalf("safe read value/page lost: %+v", output)
	}
	// The new public Read schema has no caller-provided runtime proof fields.
	input, _ := json.Marshal(GetInput{ReadTarget: ReadTarget{WindowID: "read-window"}, ControlID: "name"})
	if strings.Contains(string(input), "authorityBinding") || strings.Contains(string(input), "selectionToken") {
		t.Fatal("runtime proofs in public Read input")
	}
}
