package report

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

func TestReportRunRetainsLateCompletionReceipt(t *testing.T) {
	t.Parallel()
	bridge := prepareReportWindow(t)
	service := New(bridge)
	ctx, cancel := context.WithTimeout(requestctx.WithConversationID(context.Background(), "conv-1"), 25*time.Second)
	defer cancel()
	output := &ActionOutput{}
	done := make(chan error, 1)
	go func() { done <- service.run(ctx, &WindowInput{WindowID: "report-window-1"}, output) }()
	poll := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": "client-1", "timeoutMs": 1000})
	command := poll["params"].(map[string]interface{})
	require.Equal(t, "ui.report.run", command["method"])
	select {
	case err := <-done:
		t.Fatalf("report run lost ownership before completion: %v", err)
	case <-time.After(15200 * time.Millisecond):
	}
	postUIRPC(t, bridge, "ui.response", map[string]interface{}{
		"id": command["id"], "ok": true,
		"result": map[string]interface{}{"ok": true, "status": "completed", "materializationId": "example-run"},
	})
	require.NoError(t, <-done)
	require.True(t, output.OK)
	require.Equal(t, "completed", output.Result["status"])
	require.Equal(t, "example-run", output.Result["materializationId"])
}

func TestReportReadKeepsOrdinaryCommandDeadline(t *testing.T) {
	t.Parallel()
	bridge := prepareReportWindow(t)
	service := New(bridge)
	ctx, cancel := context.WithTimeout(requestctx.WithConversationID(context.Background(), "conv-1"), 25*time.Second)
	defer cancel()
	done := make(chan error, 1)
	output := &ActionOutput{}
	go func() { done <- service.getCurrent(ctx, &WindowInput{WindowID: "report-window-1"}, output) }()
	poll := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": "client-1", "timeoutMs": 1000})
	command := poll["params"].(map[string]interface{})
	require.Equal(t, "ui.report.getCurrent", command["method"])
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(18 * time.Second):
		t.Fatal("ordinary report inspection inherited the long run budget")
	}
	postUIRPC(t, bridge, "ui.response", map[string]interface{}{
		"id": command["id"], "ok": true, "result": map[string]interface{}{"status": "ready"},
	})
	require.False(t, output.OK, "late responses must not revive an expired inspection")
}

func TestReportRunBudgetStillHonorsCallerCancellation(t *testing.T) {
	bridge := prepareReportWindow(t)
	service := New(bridge)
	ctx, cancel := context.WithCancel(requestctx.WithConversationID(context.Background(), "conv-1"))
	defer cancel()
	done := make(chan error, 1)
	output := &ActionOutput{}
	go func() { done <- service.run(ctx, &WindowInput{WindowID: "report-window-1"}, output) }()
	poll := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": "client-1", "timeoutMs": 1000})
	command := poll["params"].(map[string]interface{})
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("report run ignored caller cancellation")
	}
	postUIRPC(t, bridge, "ui.response", map[string]interface{}{
		"id": command["id"], "ok": true, "result": map[string]interface{}{"status": "completed"},
	})
	require.False(t, output.OK, "late receipts must not revive a canceled run")
}

func TestCurrentReportOptionalWaitRetainsLateCompletion(t *testing.T) {
	t.Parallel()
	bridge := prepareReportWindow(t)
	service := New(bridge)
	ctx, cancel := context.WithTimeout(requestctx.WithConversationID(context.Background(), "conv-1"), 25*time.Second)
	defer cancel()
	done := make(chan error, 1)
	out := &ActionOutput{}
	go func() {
		done <- service.getCurrent(ctx, &WindowInput{WindowID: "report-window-1", WaitForCompletion: true, Include: &CurrentInclude{State: true}}, out)
	}()
	poll := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": "client-1", "timeoutMs": 1000})
	command := poll["params"].(map[string]interface{})
	select {
	case err := <-done:
		t.Fatalf("completion read returned early: %v", err)
	case <-time.After(15200 * time.Millisecond):
	}
	postUIRPC(t, bridge, "ui.response", map[string]interface{}{"id": command["id"], "ok": true, "result": map[string]interface{}{"ok": true, "status": "ready", "hasCompletedRun": true}})
	require.NoError(t, <-done)
	require.True(t, out.OK)
	require.Equal(t, "ready", out.Result["status"])
}
