package sdk

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
)

func TestAGUIOverflowRecoveryKeepsFragmentedForgeOutOfChat(t *testing.T) {
	c := newDatlyObservedClient(t, 1)
	c.store = &shortFirstLeaseStore{Store: c.store}
	ctx := recoveryContext()
	record, query := observerAdmission(t, c, observerInput())
	prefix := "Intro\n```forge-data\n{\"id\":\"rows\",\"data\":[{\"secret_authoring_key\":"
	suffix := "1}]}\n```\n```forge-ui\n{\"version\":1,\"blocks\":[]}\n```\nDone."
	resume := make(chan struct{})
	nativeDone := make(chan error, 1)
	c.query = func(executionCtx context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		if err := nativeTestTurn(executionCtx, c, in.MessageID, "running"); err != nil {
			return nil, err
		}
		if err := nativeTestMessage(executionCtx, c, in.MessageID, "assistant", "assistant", prefix); err != nil {
			return nil, err
		}
		zero := 0
		_ = c.bus.Publish(executionCtx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: in.ConversationID, TurnID: in.MessageID, AssistantMessageID: "assistant", Content: prefix, ContentOffset: &zero})
		for i := 0; i < 10000; i++ {
			_ = c.bus.Publish(executionCtx, &streaming.Event{Type: streaming.EventTypeUsage, ConversationID: in.ConversationID, TurnID: in.MessageID})
		}
		<-resume
		offset := len(prefix)
		for _, fragment := range []string{suffix[:10], suffix[10:25], suffix[25:]} {
			position := offset
			_ = c.bus.Publish(executionCtx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: in.ConversationID, TurnID: in.MessageID, AssistantMessageID: "assistant", Content: fragment, ContentOffset: &position})
			offset += len(fragment)
		}
		_ = c.bus.Publish(executionCtx, &streaming.Event{Type: streaming.EventTypeModelCompleted, ConversationID: in.ConversationID, TurnID: in.MessageID, AssistantMessageID: "assistant", Content: prefix + suffix})
		if err := nativeTestMessage(executionCtx, c, in.MessageID, "assistant", "assistant", prefix+suffix); err != nil {
			nativeDone <- err
			return nil, err
		}
		err := nativeTestTurn(executionCtx, c, in.MessageID, "succeeded")
		nativeDone <- err
		return &agentsvc.QueryOutput{Content: prefix + suffix}, err
	}
	err := runAGUIDurable(ctx, c, c, c.store, record, observerInputWithIdentity(record), query, aguiPending{}, nil)
	require.ErrorIs(t, err, errAGUIObserverLost)
	actual, err := c.store.GetRun(ctx, "owner", record.ThreadID, record.RunID)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return actual.LeaseUntil != nil && !actual.LeaseUntil.After(time.Now()) }, 3*time.Second, 5*time.Millisecond)
	// The native task is stopped at a channel boundary, so replacing its bus
	// before releasing it is synchronized and models attachment on a new process.
	c.bus = streaming.NewMemoryBus(64)
	c.subscribed.Store(false)
	done := make(chan error, 1)
	go func() { done <- recoverAGUIDurable(ctx, c, c, c.store, actual) }()
	require.Eventually(t, func() bool { return c.subscribed.Load() }, time.Second, time.Millisecond)
	close(resume)
	require.NoError(t, <-nativeDone)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("rich recovery did not finish")
	}
	actual, err = c.store.GetRun(ctx, "owner", record.ThreadID, record.RunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusFinished, actual.Status)
	journal, err := aguiRecoveryJournal(ctx, c.store, actual)
	require.NoError(t, err)
	activity := false
	for _, raw := range journal {
		var event struct {
			Type     string                       `json:"type"`
			Delta    string                       `json:"delta"`
			Messages []map[string]json.RawMessage `json:"messages"`
		}
		require.NoError(t, json.Unmarshal(raw, &event))
		if event.Type == "TEXT_MESSAGE_CONTENT" {
			require.NotContains(t, event.Delta, "forge-data")
			require.NotContains(t, event.Delta, "secret_authoring_key")
		}
		for _, message := range event.Messages {
			if string(message["role"]) == `"assistant"` {
				require.NotContains(t, string(message["content"]), "secret_authoring_key")
				require.NotContains(t, string(message["content"]), "forge-ui")
			}
		}
		activity = activity || strings.Contains(string(raw), "agently.rendered-content")
		require.NotContains(t, string(raw), "RUN_ERROR")
	}
	require.True(t, activity)
	require.EqualValues(t, 1, c.queries.Load())
	thread, err := c.store.GetThread(ctx, "owner", record.ThreadID)
	require.NoError(t, err)
	require.Contains(t, string(thread.Messages), "Done.")
}
func observerInputWithIdentity(record *aguistore.Run) *agui.RunAgentInput {
	var input agui.RunAgentInput
	_ = json.Unmarshal(record.Input, &input)
	return &input
}
func TestAGUIDetachedJournalPersistsInheritedRawFrontendDefinitions(t *testing.T) {
	c := newDatlyObservedClient(t, 64)
	ctx := recoveryContext()
	input := observerInput()
	input.Tools = []agui.Tool{{Name: "browser", Description: "frontend", Parameters: json.RawMessage(`{"type":"object","properties":{"n":{"const":9007199254740993}}}`), Metadata: json.RawMessage(`{"exact":9007199254740993,"flag":false}`)}}
	record, _ := observerAdmission(t, c, input)
	parent := newAGUIInvocationObserver(ctx, c, record.ThreadID, record.TurnID)
	parent.runtime, parent.store, parent.run = c, c.store, record
	inv := requestctx.Invocation{Detached: true, ExecutionMode: "detach", ID: "detached-tools", ConversationID: "child", TurnID: "child-turn", Name: "child", ParentConversationID: record.ThreadID, ParentTurnID: record.TurnID}
	childShell := conversation.NewConversation()
	childShell.SetId(inv.ConversationID)
	childShell.SetCreatedByUserID("owner")
	require.NoError(t, c.conv.PatchConversations(recoveryContext(), childShell))
	nativeCtx, err := startAGUIDetached(ctx, parent, inv)
	require.NoError(t, err)
	actual, err := c.store.GetRun(ctx, "owner", inv.ConversationID, inv.ID)
	require.NoError(t, err)
	var saved agui.RunAgentInput
	require.NoError(t, json.Unmarshal(actual.Input, &saved))
	require.Len(t, saved.Tools, 1)
	require.JSONEq(t, string(input.Tools[0].Parameters), string(saved.Tools[0].Parameters))
	require.Equal(t, string(input.Tools[0].Metadata), string(saved.Tools[0].Metadata))
	session, err := aguiClientToolSession(&saved)
	require.NoError(t, err)
	require.Equal(t, json.Number("9007199254740993"), session.Definitions()[0].Parameters["properties"].(map[string]interface{})["n"].(map[string]interface{})["const"])
	require.NoError(t, requestctx.NotifyInvocationReturned(nativeCtx, requestctx.InvocationResult{Invocation: inv, NativeStatus: "succeeded", Content: "done"}))
}

func TestAGUIRecoveryLiveChildPresentationUsesNativeRawCheckpoint(t *testing.T) {
	c := newDatlyObservedClient(t, 64)
	ctx := recoveryContext()
	record, _ := observerAdmission(t, c, observerInput())
	require.NoError(t, nativeTestTurn(ctx, c, record.TurnID, "running"))
	inv := requestctx.Invocation{ID: "child-run", ConversationID: "child-thread", TurnID: "child-turn", Name: "child", ParentConversationID: record.ThreadID, ParentTurnID: record.TurnID}
	child := conversation.NewConversation()
	child.SetId(inv.ConversationID)
	child.SetCreatedByUserID("owner")
	child.SetConversationParentId(record.ThreadID)
	child.SetConversationParentTurnId(record.TurnID)
	require.NoError(t, c.conv.PatchConversations(ctx, child))
	childTurn := conversation.NewTurn()
	childTurn.SetId(inv.TurnID)
	childTurn.SetConversationID(inv.ConversationID)
	childTurn.SetStatus("running")
	require.NoError(t, c.conv.PatchTurn(ctx, childTurn))
	prefix := "Child\n```forge-data\n{\"id\":\"rows\",\"data\":[{\"secret_child_key\":"
	suffix := "1}]}\n```\nDone."
	message := conversation.NewMessage()
	message.SetId("child-answer")
	message.SetConversationID(inv.ConversationID)
	message.SetTurnID(inv.TurnID)
	message.SetRole("assistant")
	message.SetType("text")
	message.SetContent(prefix)
	require.NoError(t, c.conv.PatchMessage(ctx, message))
	projection, err := aguistate.New(nil, json.RawMessage(`[{"id":"user-message","role":"user","content":"hello"}]`))
	require.NoError(t, err)
	translator := agui.NewTranslator(record.ThreadID, record.RunID)
	require.NoError(t, translator.SetNativeIdentity(record.TurnID))
	writer := &aguiJournalWriter{ctx: ctx, client: c, store: c.store, run: record, projection: projection}
	require.NoError(t, writer.write(encodeAGUIEvents(translator.Start()), nil))
	require.NoError(t, writer.write([]json.RawMessage{rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": projection.Messages})}, nil))
	require.NoError(t, writer.write(encodeAGUIEvents(translator.RegisterInvocation(inv)), nil))
	visible, _ := plainAGUIContent(prefix, false)
	require.NoError(t, writer.write(encodeAGUIEvents(translator.TranslateSubagent(inv, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: inv.ConversationID, TurnID: inv.TurnID, MessageID: "child-answer", Content: visible})), nil))
	expired, err := c.store.Claim(ctx, "owner", record.ThreadID, record.RunID, writer.run.Revision, "lost-observer", time.Millisecond)
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- recoverAGUIDurable(ctx, c, c, c.store, expired) }()
	require.Eventually(t, func() bool { return c.subscribed.Load() }, time.Second, time.Millisecond)
	offset := len(prefix)
	require.NoError(t, c.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: inv.ConversationID, TurnID: inv.TurnID, AssistantMessageID: "child-answer", Content: suffix, ContentOffset: &offset}))
	require.Eventually(t, func() bool {
		thread, e := c.store.GetThread(ctx, "owner", record.ThreadID)
		return e == nil && strings.Contains(string(thread.Messages), "Done.")
	}, 3*time.Second, 5*time.Millisecond)
	message.SetContent(prefix + suffix)
	require.NoError(t, c.conv.PatchMessage(ctx, message))
	childTurn.SetStatus("succeeded")
	require.NoError(t, c.conv.PatchTurn(ctx, childTurn))
	require.NoError(t, nativeTestTurn(ctx, c, record.TurnID, "succeeded"))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("child presentation recovery did not finish")
	}
	actual, err := c.store.GetRun(ctx, "owner", record.ThreadID, record.RunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusFinished, actual.Status)
	journal, err := aguiRecoveryJournal(ctx, c.store, actual)
	require.NoError(t, err)
	for _, raw := range journal {
		var event struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		}
		require.NoError(t, json.Unmarshal(raw, &event))
		if event.Type == "TEXT_MESSAGE_CONTENT" {
			require.NotContains(t, event.Delta, "secret_child_key")
			require.NotContains(t, event.Delta, "forge-data")
		}
		require.NotContains(t, string(raw), "RUN_ERROR")
	}
	require.Zero(t, c.queries.Load())
}
