package sdk

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	queue "github.com/viant/agently-core/model/toolapprovalqueue"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/binding"
	approvalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	"github.com/viant/agently-core/runtime/aguistate"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/shared/toolapproval"
	"github.com/viant/agently-core/service/shared/toolexec"
)

func TestAGUIApprovalHistoryConsumesCommittedOriginalReceipt(t *testing.T) {
	f := newCoordinatedApprovalFixture(t, 1, false)
	ctx := requestctx.WithTurnMeta(recoveryContext(), requestctx.TurnMeta{ConversationID: "approval-thread", TurnID: "approval-turn"})
	const marker = "queued for user approval"
	const result = `{"values":{"HOME":"fixture-home","PATH":"fixture-path"}}`
	f.registry.result = result
	registry := &approvalHistoryRegistry{approvalCountingRegistry: f.registry}
	f.client.registry = registry
	f.client.agent = agentsvc.New(nil, nil, nil, registry, &config.Defaults{}, f.client.conv, agentsvc.WithDataService(f.client.data))
	originalArgs := rawAGUI(map[string]any{"names": []string{"HOME", "PATH", "SHELL"}})
	queueUpdate := &queue.ToolApprovalQueue{Has: &queue.ToolApprovalQueueHas{}}
	queueUpdate.SetId("approval-0")
	queueUpdate.SetUserId("owner")
	queueUpdate.SetArguments(originalArgs)
	queueUpdate.SetMetadata(rawAGUI(map[string]any{"opId": "backend-op-0", "approval": toolapproval.View{Editors: []*toolapproval.EditorView{{Name: "names", Path: "names", Kind: "checkbox_list", Options: []*toolapproval.OptionView{{ID: "HOME", Item: "HOME"}, {ID: "PATH", Item: "PATH"}, {ID: "SHELL", Item: "SHELL"}}}}}}))
	require.NoError(t, f.client.conv.(toolApprovalQueuePatcher).PatchToolApprovalQueue(ctx, queueUpdate))
	request := conversation.NewPayload()
	request.SetId("approval-tool-0/request")
	request.SetInlineBody(originalArgs)
	request.SetSizeBytes(len(originalArgs))
	require.NoError(t, f.client.conv.PatchPayload(ctx, request))
	message := conversation.NewMessage()
	message.SetId("approval-tool-0")
	message.SetType("tool_op")
	message.SetContent(marker)
	message.SetStatus("pending")
	require.NoError(t, f.client.conv.PatchMessage(ctx, message))
	parent := conversation.NewMessage()
	parent.SetId("approval-assistant")
	parent.SetMode("task")
	parent.SetInterim(1)
	archived := 1
	parent.Archived, parent.Has.Archived = &archived, true
	require.NoError(t, f.client.conv.PatchMessage(ctx, parent))
	payload := conversation.NewPayload()
	payload.SetId("approval-tool-0/queued-response")
	payload.SetKind("tool_response")
	payload.SetStorage("inline")
	payload.SetMimeType("text/plain")
	payload.SetInlineBody([]byte(marker))
	payload.SetSizeBytes(len(marker))
	require.NoError(t, f.client.conv.PatchPayload(ctx, payload))
	call := conversation.NewToolCall()
	call.SetMessageID("approval-tool-0")
	call.SetOpID("backend-op-0")
	call.SetStatus("queued")
	call.ResponsePayloadID = &payload.Id
	call.Has.ResponsePayloadID = true
	require.NoError(t, f.client.conv.PatchToolCall(ctx, call))
	before, err := f.client.agent.BuildBinding(ctx, &agentsvc.QueryInput{ConversationID: "approval-thread", Agent: &agentmdl.Agent{}})
	require.NoError(t, err)
	require.Equal(t, marker, before.History.Current.Messages[0].Content)
	answer := approvalAnswer("approval-0", "approve")
	answerPayload := rawAGUI(map[string]any{"action": "approve", "editedFields": map[string]any{"names": []string{"HOME", "PATH"}}})
	answer.Payload = &answerPayload
	_, err = f.client.aguiApplyApproval(ctx, f.original, f.pending.Interrupts[0], answer)
	require.NoError(t, err)
	require.JSONEq(t, `{"names":["HOME","PATH"]}`, string(registry.arguments))
	stored, err := f.client.conv.GetMessage(ctx, message.Id, conversation.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, result, stored.GetContent())
	require.Equal(t, result, stored.GetContentPreferContent())
	require.Equal(t, "completed", stored.MessageToolCall.Status)
	require.JSONEq(t, `{"names":["HOME","PATH"]}`, *stored.MessageToolCall.MessageRequestPayload.InlineBody)
	rows, err := f.client.conv.(toolApprovalQueueLister).ListToolApprovalQueues(ctx, &queue.QueueRowsInput{UserId: "owner", ConversationId: "approval-thread", TurnId: "approval-turn", QueueStatus: "pending", Has: &queue.QueueRowsInputHas{UserId: true, ConversationId: true, TurnId: true, QueueStatus: true}})
	require.NoError(t, err)
	require.Empty(t, rows)
	b, err := f.client.agent.BuildBinding(ctx, &agentsvc.QueryInput{ConversationID: "approval-thread", Agent: &agentmdl.Agent{}})
	require.NoError(t, err)
	count := 0
	turns := append([]*binding.Turn(nil), b.History.Past...)
	if b.History.Current != nil {
		turns = append(turns, b.History.Current)
	}
	for _, turn := range turns {
		for _, msg := range turn.Messages {
			if msg.Kind == binding.MessageKindToolResult {
				count++
				require.Equal(t, "backend-op-0", msg.ToolOpID)
				require.Equal(t, result, msg.Content)
				require.Equal(t, []interface{}{"HOME", "PATH"}, msg.ToolArgs["names"])
			}
		}
	}
	require.Equal(t, 1, count)
	runID := "receipt-view"
	record, _, err := f.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "approval-thread", RunID: runID, TurnID: "approval-turn", PriorRunID: f.original.RunID, ExpectedPriorRevision: f.original.Revision, Input: rawAGUI(map[string]any{"threadId": "approval-thread", "runId": runID, "messages": []any{}})})
	require.NoError(t, err)
	publicCall := agui.ProtocolToolCallID("approval-turn", "backend-op-0")
	wireBefore := rawAGUI([]any{
		map[string]any{"id": "approval-assistant", "role": "assistant", "toolCalls": []any{map[string]any{"id": publicCall, "type": "function", "function": map[string]any{"name": "backend", "arguments": "{}"}}}},
		map[string]any{"id": "approval-tool-0", "role": "tool", "toolCallId": publicCall, "content": marker},
	})
	projection, err := aguistate.New(nil, wireBefore)
	require.NoError(t, err)
	translator := agui.NewTranslator(record.ThreadID, record.RunID)
	require.NoError(t, translator.SetNativeIdentity(record.TurnID))
	var messages []json.RawMessage
	require.NoError(t, json.Unmarshal(wireBefore, &messages))
	require.NoError(t, translator.SeedMessages(messages))
	writer := &aguiJournalWriter{ctx: ctx, client: f.client, store: f.store, run: record, projection: projection}
	require.NoError(t, writer.write(encodeAGUIEvents(translator.Start()), nil))
	for _, nativeStatus := range []string{"waiting_for_user", "succeeded"} {
		t.Run(nativeStatus, func(t *testing.T) {
			turn := conversation.NewTurn()
			turn.SetId("approval-turn")
			turn.SetStatus(nativeStatus)
			require.NoError(t, f.client.conv.PatchTurn(ctx, turn))
			interrupt := f.pending.Interrupts[0]
			require.NoError(t, aguiProjectApprovalReceipt(ctx, f.client, writer, translator, interrupt))
			sequence := writer.run.LastSequence
			require.NoError(t, aguiProjectApprovalReceipt(ctx, f.client, writer, translator, interrupt))
			require.Equal(t, sequence, writer.run.LastSequence, "the committed receipt projects exactly once")
			journal, err := aguiRecoveryJournal(ctx, f.store, writer.run)
			require.NoError(t, err)
			results := 0
			for _, raw := range journal {
				var event map[string]interface{}
				require.NoError(t, json.Unmarshal(raw, &event))
				if event["type"] == "TOOL_CALL_RESULT" {
					results++
					require.Equal(t, publicCall, event["toolCallId"])
					require.Equal(t, "approval-tool-0", event["messageId"])
					require.Equal(t, result, event["content"])
				}
			}
			require.Equal(t, 1, results)
			require.EqualValues(t, 1, f.registry.calls.Load(), "projection must neither execute the effect again nor fabricate an assistant answer")
		})
	}
}

type approvalHistoryRegistry struct {
	*approvalCountingRegistry
	arguments json.RawMessage
}

func (r *approvalHistoryRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	r.arguments = rawAGUI(args)
	return r.approvalCountingRegistry.Execute(ctx, name, args)
}

func TestNativeQueuedToolMessageUsesCanonicalPendingStatus(t *testing.T) {
	f := newCoordinatedApprovalFixture(t, 1, false)
	for _, behavior := range []llm.ApprovalQueueBehavior{llm.ApprovalQueueBehaviorWait, ""} {
		name := string(behavior)
		if name == "" {
			name = "default-detach"
		}
		t.Run(name, func(t *testing.T) {
			ctx := recoveryContext()
			turn := conversation.NewTurn()
			turn.SetId("native-queue-" + name)
			turn.SetConversationID("approval-thread")
			turn.SetStatus("running")
			require.NoError(t, f.client.conv.PatchTurn(ctx, turn))
			assistant := conversation.NewMessage()
			assistant.SetId(turn.Id + "-assistant")
			assistant.SetConversationID("approval-thread")
			assistant.SetTurnID(turn.Id)
			assistant.SetRole("assistant")
			assistant.SetType("text")
			assistant.SetContent("requesting the protected tool")
			require.NoError(t, f.client.conv.PatchMessage(ctx, assistant))
			ctx = requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "approval-thread", TurnID: turn.Id, ParentMessageID: assistant.Id})
			ctx = requestctx.WithModelMessageID(ctx, assistant.Id)
			ctx = approvalqueue.WithState(ctx)
			approvalqueue.MarkTool(ctx, "native/queued", &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue, QueueBehavior: behavior})
			call, _, err := toolexec.ExecuteToolStep(ctx, f.registry, toolexec.StepInfo{ID: "native-queue-op-" + name, Name: "native/queued", Args: map[string]interface{}{"names": []string{"HOME", "PATH"}}}, f.client.conv)
			status, messageStatus := "completed", "completed"
			if behavior == llm.ApprovalQueueBehaviorWait {
				require.ErrorIs(t, err, toolexec.ErrQueued)
				status, messageStatus = "queued", "pending"
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, "queued for user approval", call.Result)
			require.Zero(t, f.registry.calls.Load(), "no approval path may execute the effect before consent")
			stored, err := f.client.conv.GetMessage(ctx, call.ResultMessageID, conversation.WithIncludeToolCall(true))
			require.NoError(t, err)
			require.Equal(t, messageStatus, *stored.Status)
			require.Equal(t, status, stored.MessageToolCall.Status)
			pending, err := f.client.conv.(toolApprovalQueueLister).ListToolApprovalQueues(ctx, &queue.QueueRowsInput{UserId: "owner", ConversationId: "approval-thread", TurnId: turn.Id, QueueStatus: "pending", Has: &queue.QueueRowsInputHas{UserId: true, ConversationId: true, TurnId: true, QueueStatus: true}})
			require.NoError(t, err)
			require.Len(t, pending, 1)
		})
	}
}
