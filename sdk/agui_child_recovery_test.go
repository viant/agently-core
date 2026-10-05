package sdk

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"strings"
	"testing"
	"time"
)

func (c *datlyObservedClient) aguiInspectInvocation(ctx context.Context, run *aguistore.Run, inv requestctx.Invocation) (*aguiRecoveredNative, error) {
	return c.native.aguiInspectInvocation(ctx, run, inv)
}
func TestAGUIRecoveryReconcilesNativeChildRegisteredBeforeFirstEventAndOpenJournal(t *testing.T) {
	for _, open := range []bool{false, true} {
		t.Run("open="+map[bool]string{false: "false", true: "true"}[open], func(t *testing.T) {
			c := newDatlyObservedClient(t, 64)
			ctx := recoveryContext()
			record, _ := observerAdmission(t, c, observerInput())
			child := conversation.NewConversation()
			child.SetId("child-thread")
			child.SetCreatedByUserID("owner")
			child.SetConversationParentId(record.ThreadID)
			child.SetConversationParentTurnId(record.TurnID)
			require.NoError(t, c.conv.PatchConversations(ctx, child))
			turn := conversation.NewTurn()
			turn.SetId("child-turn")
			turn.SetConversationID("child-thread")
			turn.SetStatus("succeeded")
			require.NoError(t, c.conv.PatchTurn(ctx, turn))
			answer := conversation.NewMessage()
			answer.SetId("child-answer")
			answer.SetConversationID("child-thread")
			answer.SetTurnID("child-turn")
			answer.SetRole("assistant")
			answer.SetType("text")
			answer.SetContent("authoritative child completion")
			require.NoError(t, c.conv.PatchMessage(ctx, answer))
			require.NoError(t, nativeTestTurn(ctx, c, record.TurnID, "succeeded"))
			require.NoError(t, nativeTestMessage(ctx, c, record.TurnID, "root-answer", "assistant", "authoritative root completion"))
			projection, err := aguistate.New(nil, json.RawMessage(`[{"id":"user-message","role":"user","content":"hello"}]`))
			require.NoError(t, err)
			translator := agui.NewTranslator(record.ThreadID, record.RunID)
			require.NoError(t, translator.SetNativeIdentity(record.TurnID))
			writer := &aguiJournalWriter{ctx: ctx, client: c, store: c.store, run: record, projection: projection}
			require.NoError(t, writer.write(encodeAGUIEvents(translator.Start()), nil))
			require.NoError(t, writer.write([]json.RawMessage{rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": projection.Messages})}, nil))
			inv := requestctx.Invocation{ID: "child-run", ConversationID: "child-thread", TurnID: "child-turn", Name: "child", ParentConversationID: record.ThreadID, ParentTurnID: record.TurnID}
			require.NoError(t, writer.write(encodeAGUIEvents(translator.RegisterInvocation(inv)), nil))
			if open {
				// Reused conversation metadata can point at a newer invocation;
				// the durable registration, not mutable metadata, keeps ancestry.
				reused := conversation.NewConversation()
				reused.SetId(inv.ConversationID)
				reused.SetConversationParentTurnId("later-parent-turn")
				require.NoError(t, c.conv.PatchConversations(ctx, reused))
			}
			if open {
				require.NoError(t, writer.write(encodeAGUIEvents(translator.TranslateSubagent(inv, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: inv.ConversationID, TurnID: inv.TurnID, MessageID: "child-answer", Content: "partial"})), nil))
			}
			claimed, err := c.store.Claim(ctx, "owner", record.ThreadID, record.RunID, writer.run.Revision, "lost-observer", time.Millisecond)
			require.NoError(t, err)
			time.Sleep(5 * time.Millisecond)
			require.NoError(t, recoverAGUIDurable(ctx, c, c, c.store, claimed))
			actual, err := c.store.GetRun(ctx, "owner", record.ThreadID, record.RunID)
			require.NoError(t, err)
			require.Equal(t, aguistore.StatusFinished, actual.Status)
			journal, err := aguiRecoveryJournal(ctx, c.store, actual)
			require.NoError(t, err)
			starts, ends := 0, 0
			startIndex, endIndex, rootEnd := -1, -1, -1
			for i, raw := range journal {
				var event struct {
					Type  string `json:"type"`
					Owner string `json:"subagentRunId"`
				}
				require.NoError(t, json.Unmarshal(raw, &event))
				require.NoError(t, agui.ValidateEvent(raw))
				if event.Type == "SUBAGENT_STARTED" && event.Owner == inv.ID {
					starts++
					startIndex = i
				}
				if event.Type == "SUBAGENT_FINISHED" && event.Owner == inv.ID {
					ends++
					endIndex = i
				}
				if event.Type == "RUN_FINISHED" {
					rootEnd = i
				}
				require.NotContains(t, string(raw), "RUN_ERROR")
			}
			require.Equal(t, 1, starts)
			require.Equal(t, 1, ends)
			require.Less(t, startIndex, endIndex)
			require.Less(t, endIndex, rootEnd)
			require.Zero(t, c.queries.Load())
			thread, err := c.store.GetThread(ctx, "owner", record.ThreadID)
			require.NoError(t, err)
			require.Contains(t, string(thread.Messages), "authoritative child completion")
			require.Contains(t, string(thread.Messages), "authoritative root completion")
			require.Contains(t, string(thread.Messages), `"subagentRunId":"child-run"`)
		})
	}
}
func TestAGUINativeChildInspectionRejectsForgedPersistedParentScope(t *testing.T) {
	c := newDatlyObservedClient(t, 64)
	ctx := recoveryContext()
	record, _ := observerAdmission(t, c, observerInput())
	child := conversation.NewConversation()
	child.SetId("foreign-child")
	child.SetCreatedByUserID("owner")
	child.SetConversationParentId("foreign-root")
	child.SetConversationParentTurnId("foreign-turn")
	require.NoError(t, c.conv.PatchConversations(ctx, child))
	inv := requestctx.Invocation{ID: "child", ConversationID: "foreign-child", TurnID: "child-turn", Name: "child", ParentConversationID: record.ThreadID, ParentTurnID: record.TurnID}
	_, err := c.aguiInspectInvocation(ctx, record, inv)
	require.ErrorContains(t, err, "parent does not match")
	require.False(t, strings.Contains(err.Error(), "SELECT"))
}

func TestAGUIRecoveredChildQueuedRunningAndMissingNeverInventTerminals(t *testing.T) {
	c := newDatlyObservedClient(t, 64)
	ctx := recoveryContext()
	record, _ := observerAdmission(t, c, observerInput())
	child := conversation.NewConversation()
	child.SetId("child-thread")
	child.SetCreatedByUserID("owner")
	child.SetConversationParentId(record.ThreadID)
	child.SetConversationParentTurnId(record.TurnID)
	require.NoError(t, c.conv.PatchConversations(ctx, child))
	projection, err := aguistate.New(nil, json.RawMessage(`[]`))
	require.NoError(t, err)
	translator := agui.NewTranslator(record.ThreadID, record.RunID)
	require.NoError(t, translator.SetNativeIdentity(record.TurnID))
	writer := &aguiJournalWriter{ctx: ctx, client: c, store: c.store, run: record, projection: projection}
	require.NoError(t, writer.write(encodeAGUIEvents(translator.Start()), nil))
	inv := requestctx.Invocation{ID: "child-run", ConversationID: "child-thread", TurnID: "child-turn", Name: "child", ParentConversationID: record.ThreadID, ParentTurnID: record.TurnID}
	require.NoError(t, writer.write(encodeAGUIEvents(translator.RegisterInvocation(inv)), nil))
	sequence := writer.run.LastSequence
	root := &aguiRecoveredNative{Status: "succeeded"}
	settled, err := aguiReconcileRecoveredChildren(ctx, c, writer, translator, root)
	require.NoError(t, err)
	require.False(t, settled)
	require.Equal(t, sequence, writer.run.LastSequence, "missing native turn is not start/finish evidence")
	turn := conversation.NewTurn()
	turn.SetId(inv.TurnID)
	turn.SetConversationID(inv.ConversationID)
	turn.SetStatus("queued")
	require.NoError(t, c.conv.PatchTurn(ctx, turn))
	settled, err = aguiReconcileRecoveredChildren(ctx, c, writer, translator, root)
	require.NoError(t, err)
	require.False(t, settled)
	require.Equal(t, sequence, writer.run.LastSequence, "queued native turn is not execution-start evidence")
	turn.SetStatus("running")
	require.NoError(t, c.conv.PatchTurn(ctx, turn))
	settled, err = aguiReconcileRecoveredChildren(ctx, c, writer, translator, root)
	require.NoError(t, err)
	require.False(t, settled)
	require.Len(t, translator.ActiveInvocations(), 1)
	sequence = writer.run.LastSequence
	settled, err = aguiReconcileRecoveredChildren(ctx, c, writer, translator, root)
	require.NoError(t, err)
	require.False(t, settled)
	require.Equal(t, sequence, writer.run.LastSequence, "unchanged polling must not append snapshot noise")
	turn.SetStatus("succeeded")
	require.NoError(t, c.conv.PatchTurn(ctx, turn))
	settled, err = aguiReconcileRecoveredChildren(ctx, c, writer, translator, root)
	require.NoError(t, err)
	require.True(t, settled)
	require.Empty(t, translator.ActiveInvocations())
	journal, err := aguiRecoveryJournal(ctx, c.store, writer.run)
	require.NoError(t, err)
	var starts, ends int
	for _, raw := range journal {
		var event struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(raw, &event))
		if event.Type == "SUBAGENT_STARTED" {
			starts++
		}
		if event.Type == "SUBAGENT_FINISHED" {
			ends++
		}
	}
	require.Equal(t, 1, starts)
	require.Equal(t, 1, ends)
}
