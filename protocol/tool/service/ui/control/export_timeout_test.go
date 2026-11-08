package control

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	bridge "github.com/viant/agently-core/service/primitiveprovider"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
)

// An export can produce its file after the ordinary command deadline. The
// original click must still own the response when the browser reports success.
func TestRegisteredReportExportReceivesLateReceipt(t *testing.T) {
	b := bridge.NewService(&bridge.Config{})
	const client, chat, window = "export-client", "example-chat", "example-report"
	postUIRPC(t, b, "ui.hello", map[string]any{"clientId": client})
	postUIRPC(t, b, "ui.snapshot", map[string]any{"clientId": client, "data": map[string]any{
		"conversationId": chat, "windows": []map[string]any{{
			"windowId": window, "windowKey": "report://example/sales", "conversationId": chat,
			"resource": map[string]any{"uri": "report://example/sales", "kind": "published"},
		}},
	}})
	postUIRPC(t, b, "ui.poll", map[string]any{"clientId": client, "timeoutMs": 1})
	ctx, cancel := context.WithTimeout(requestctx.WithConversationID(context.Background(), chat), 25*time.Second)
	defer cancel()
	out := &CommandOutput{}
	done := make(chan error, 1)
	go func() { done <- New(b).click(ctx, &ClickInput{WindowID: window, ControlID: "report-output:xlsx"}, out) }()
	poll := func() map[string]any {
		res := postUIRPC(t, b, "ui.poll", map[string]any{"clientId": client, "timeoutMs": 2000})
		cmd, ok := res["params"].(map[string]any)
		require.True(t, ok, "command was not dispatched")
		return cmd
	}
	cmd := poll()
	if cmd["method"] == "ui.controls.list" {
		postUIRPC(t, b, "ui.response", map[string]any{"id": cmd["id"], "ok": true, "result": map[string]any{
			"total": 1, "offset": 0, "limit": 2, "controls": []map[string]any{{
				"controlId": "report-output:xlsx", "type": "button", "visible": true, "supportedActions": []string{"click"},
			}},
		}})
		cmd = poll()
	}
	require.Equal(t, "ui.control.click", cmd["method"])
	select {
	case err := <-done:
		t.Fatalf("export click ended before its receipt: %v", err)
	case <-time.After(15200 * time.Millisecond):
	}
	postUIRPC(t, b, "ui.response", map[string]any{"id": cmd["id"], "ok": true, "result": map[string]any{
		"receipt": map[string]any{"status": "succeeded", "format": "xlsx", "artifactId": "example-file", "jobId": "example-job"},
	}})
	require.NoError(t, <-done)
	require.Equal(t, &ActionReceipt{Status: "succeeded", Format: "xlsx", ArtifactID: "example-file", JobID: "example-job"}, out.Receipt)
}

func TestOrdinaryOperationsKeepDefaultCommandTimeout(t *testing.T) {
	service := &Service{}
	for _, tc := range []struct{ uri, method, id string }{
		{"report://example/sales", "ui.control.focus", "report-output:xlsx"},
		{"report://example/sales", "ui.control.setValue", "report-output:table"},
		{"report://example/sales", "ui.control.click", "report-output:print"},
		{"report://example/sales", "ui.control.click", "report-output:other"},
		{"window://example/orders", "ui.control.click", "report-output:xlsx"},
	} {
		win := &uireg.WindowSnapshot{Resource: &identity.ResolvedResource{URI: tc.uri}}
		require.Zero(t, service.exportClickTimeoutMs(t.Context(), tc.method, tc.id, win, ReadTarget{}))
	}
}
