package window

import (
	"context"
	"fmt"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
	"strings"
	"testing"
)

func TestScopedRefreshDispatchAndRevision(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	postUIRPC(t, bridge, "ui.hello", map[string]interface{}{"clientId": "refresh-client"})
	postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{"clientId": "refresh-client", "data": map[string]interface{}{"conversationId": "refresh-chat", "windows": []interface{}{map[string]interface{}{"windowId": "refresh-window", "windowKey": "fixture", "conversationId": "refresh-chat", "operationRevision": 7}}}})
	service := New(bridge)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "refresh-chat")
	stale := uint32(6)
	if err := service.refresh(ctx, &RefreshInput{WindowID: "refresh-window", ExpectedRevision: &stale, ReloadMetadata: true}, &RefreshOutput{}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale denial: %v", err)
	}
	if err := service.refresh(runtimerequestctx.WithConversationID(context.Background(), "foreign-chat"), &RefreshInput{WindowID: "refresh-window", ReloadMetadata: true}, &RefreshOutput{}); err == nil {
		t.Fatal("foreign chat refresh admitted")
	}
	revision := uint32(7)
	done := make(chan error, 1)
	go func() {
		out := &RefreshOutput{}
		err := service.refresh(ctx, &RefreshInput{WindowID: "refresh-window", Scope: "tab", ContainerID: "sections", TabID: "second", ExpectedRevision: &revision, ReloadMetadata: true}, out)
		if err == nil && (!out.OK || !out.MetadataRefreshed || out.DataRefreshed || out.OperationRevision != 8) {
			err = fmt.Errorf("unexpected result %#v", out)
		}
		done <- err
	}()
	polled := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": "refresh-client", "timeoutMs": 1000})
	request := polled["params"].(map[string]interface{})
	if request["method"] != "ui.workspace.refresh" {
		t.Fatalf("unexpected method %#v", request)
	}
	params := request["params"].(map[string]interface{})
	if params["scope"] != "tab" || params["tabId"] != "second" || params["conversationId"] != "refresh-chat" || params["reloadMetadata"] != true {
		t.Fatalf("lost scoped authority %#v", params)
	}
	postUIRPC(t, bridge, "ui.response", map[string]interface{}{"id": request["id"], "ok": true, "result": map[string]interface{}{"status": "refreshed", "scope": "tab", "metadataRefreshed": true, "dataRefreshed": false, "operationRevision": 8}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"refresh", "refreshCapabilities"} {
		if _, err := service.Method(method); err != nil {
			t.Fatal(err)
		}
	}
}
