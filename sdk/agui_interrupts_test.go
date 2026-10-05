package sdk

import (
	"context"
	"encoding/json"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	toolapprovalqueuemodel "github.com/viant/agently-core/model/toolapprovalqueue"
	turnmodel "github.com/viant/agently-core/model/turn"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/agui"
)

func TestAGUIInterruptPreflightRejectsCoverageExpiryAndSchemaBeforeMutation(t *testing.T) {
	schema := map[string]json.RawMessage{"type": json.RawMessage(`"object"`), "properties": json.RawMessage(`{"answer":{"type":"string","enum":["yes","no"]}}`), "required": json.RawMessage(`["answer"]`), "additionalProperties": json.RawMessage(`false`)}
	pending := aguiPending{Interrupts: []agui.WireInterrupt{{ID: "ask", Reason: "elicitation", ResponseSchema: &schema}}}
	valid := &agui.RunAgentInput{ThreadID: "thread", Resume: json.RawMessage(`[{"interruptId":"ask","status":"resolved","payload":{"answer":"yes"}}]`)}
	require.NoError(t, preflightAGUIResume(context.Background(), nil, valid, pending))
	for _, wire := range []string{`[]`, `[{"interruptId":"unknown","status":"resolved"}]`, `[{"interruptId":"ask","status":"resolved"},{"interruptId":"ask","status":"cancelled"}]`, `[{"interruptId":"ask","status":"resolved","payload":{"answer":"maybe"}}]`, `[{"interruptId":"ask","status":"resolved","payload":{"answer":"yes","extra":true}}]`, `[{"interruptId":"ask","status":"resolved","payload":null}]`} {
		invalid := *valid
		invalid.Resume = json.RawMessage(wire)
		require.Error(t, preflightAGUIResume(context.Background(), nil, &invalid, pending), wire)
	}
	expiry := time.Now().Add(-time.Second).Format(time.RFC3339Nano)
	pending.Interrupts[0].ExpiresAt = &expiry
	require.ErrorContains(t, preflightAGUIResume(context.Background(), nil, valid, pending), "expired")
	cancelled := *valid
	cancelled.Resume = json.RawMessage(`[{"interruptId":"ask","status":"cancelled"}]`)
	require.NoError(t, preflightAGUIResume(context.Background(), nil, &cancelled, pending))
}
func TestAGUIApprovalInterruptRetainsOriginalCallPolicyAndEditsContract(t *testing.T) {
	expiry := time.Now().Add(time.Hour)
	approval := &PendingToolApproval{ID: "approval", ConversationID: "thread", TurnID: "turn", MessageID: "assistant", ToolName: "system/exec:run", Arguments: map[string]interface{}{"command": "ls"}, Metadata: map[string]interface{}{"opId": "original-call", "review": map[string]interface{}{"schema": map[string]interface{}{"type": "object"}}}, ExpiresAt: &expiry}
	interrupt, err := aguiApprovalInterrupt(approval)
	require.NoError(t, err)
	require.Equal(t, "approval", interrupt.Reason)
	require.Equal(t, "original-call", *interrupt.ToolCallID)
	require.NoError(t, agui.ValidateEvent(rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": "thread", "runId": "run", "outcome": map[string]any{"type": "interrupt", "interrupts": []agui.WireInterrupt{interrupt}}})))
	require.Contains(t, string((*interrupt.Metadata)["agently"]), "system/exec:run")
	require.Contains(t, string((*interrupt.Metadata)["agently"]), "original-call")
	wire := json.RawMessage(`{"action":"approve","editedArgs":{"command":"pwd"}}`)
	answer, err := decodeAGUIApprovalAnswer(agui.WireResumeEntry{InterruptId: "approval", Status: "resolved", Payload: &wire})
	require.NoError(t, err)
	require.Equal(t, "pwd", answer.EditedArgs["command"])
	_, err = decodeAGUIApprovalAnswer(agui.WireResumeEntry{InterruptId: "approval", Status: "cancelled", Payload: &wire})
	require.Error(t, err)
	_, err = aguiApprovalInterrupt(&PendingToolApproval{ID: "without-call"})
	require.Error(t, err)
}
func TestAGUIResponseSchemaSupportsLocalDefinitionsWithoutRemoteFetch(t *testing.T) {
	schema := map[string]json.RawMessage{"$defs": json.RawMessage(`{"value":{"type":"integer","minimum":0}}`), "$ref": json.RawMessage(`"#/$defs/value"`)}
	require.NoError(t, aguiValidateResponseSchema(schema, json.Number("9007199254740991")))
	require.Error(t, aguiValidateResponseSchema(schema, json.Number("-1")))
	require.Error(t, aguiValidateResponseSchema(map[string]json.RawMessage{"$ref": json.RawMessage(`"https://example.invalid/schema"`)}, nil))
}

func TestAGUIApprovalCompletesOriginalCallAndReplaysReceipt(t *testing.T) {
	ctx := recoveryContext()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	c := &backendClient{data: data.NewService(server), conv: conv}
	root := conversation.NewConversation()
	root.SetId("approval-thread")
	root.SetCreatedByUserID("owner")
	root.SetStatus("active")
	require.NoError(t, conv.PatchConversations(ctx, root))
	turn := conversation.NewTurn()
	turn.SetId("approval-turn")
	turn.SetConversationID("approval-thread")
	turn.SetStatus("waiting_for_user")
	require.NoError(t, conv.PatchTurn(ctx, turn))
	for _, id := range []string{"approval-assistant", "approval-tool"} {
		m := conversation.NewMessage()
		m.SetId(id)
		m.SetConversationID("approval-thread")
		m.SetTurnID("approval-turn")
		m.SetRole("assistant")
		m.SetType("text")
		if id == "approval-tool" {
			m.SetRole("tool")
			m.SetType("tool_call")
			m.SetParentMessageID("approval-assistant")
		}
		require.NoError(t, conv.PatchMessage(ctx, m))
	}
	payload := conversation.NewPayload()
	payload.SetId("approval-args")
	payload.SetKind("tool_request")
	payload.SetMimeType("application/json")
	payload.SetStorage("inline")
	payload.SetSizeBytes(2)
	payload.SetInlineBody([]byte(`{}`))
	require.NoError(t, conv.PatchPayload(ctx, payload))
	call := conversation.NewToolCall()
	call.SetMessageID("approval-tool")
	call.SetTurnID("approval-turn")
	call.SetOpID("backend-op")
	call.SetToolName("backend")
	call.SetToolKind("function")
	call.SetStatus("waiting_for_user")
	request := "approval-args"
	call.RequestPayloadID = &request
	call.Has.RequestPayloadID = true
	require.NoError(t, conv.PatchToolCall(ctx, call))
	queue := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
	queue.SetId("approval-q")
	queue.SetUserId("owner")
	queue.SetToolName("backend")
	queue.SetArguments([]byte(`{}`))
	queue.SetStatus("pending")
	queue.SetConversationId("approval-thread")
	queue.SetTurnId("approval-turn")
	queue.SetMessageId("approval-assistant")
	queue.SetMetadata([]byte(`{"opId":"backend-op"}`))
	require.NoError(t, conv.PatchToolApprovalQueue(ctx, queue))
	record := &aguistore.Run{ThreadID: "approval-thread", ConversationID: "approval-thread", TurnID: "approval-turn", RunID: "wire", Principal: "owner", Input: json.RawMessage(`{"threadId":"approval-thread","runId":"wire","messages":[],"tools":[{"name":"backend","description":"collision","parameters":{}}]}`)}
	inspected, err := c.aguiInspectRun(ctx, record)
	require.NoError(t, err)
	require.Empty(t, inspected.Pending.ClientTools)
	require.Len(t, inspected.Pending.Interrupts, 1)
	interrupt := inspected.Pending.Interrupts[0]
	raw := json.RawMessage(`{"action":"reject","reason":"deny"}`)
	entry := agui.WireResumeEntry{InterruptId: interrupt.ID, Status: "resolved", Payload: &raw}
	require.NoError(t, c.aguiPreflightApproval(ctx, record.ThreadID, interrupt, entry))
	disposition, err := c.aguiApplyApproval(ctx, record, interrupt, entry)
	require.NoError(t, err)
	require.Equal(t, aguiInterruptDeferred, disposition)
	original, err := conv.GetMessage(ctx, "approval-tool", conversation.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "backend-op", original.MessageToolCall.OpId)
	require.Equal(t, "completed", original.MessageToolCall.Status)
	nativeTurn, err := c.data.GetTurnByID(ctx, &turnmodel.TurnLookupInput{ID: record.TurnID, ConversationID: record.ThreadID, Has: &turnmodel.TurnLookupInputHas{ID: true, ConversationID: true}}, principalDataOpts(ctx)...)
	require.NoError(t, err)
	require.Equal(t, "waiting_for_user", nativeTurn.Status)
	_, err = c.aguiApplyApproval(ctx, record, interrupt, entry)
	require.NoError(t, err)
	conflicting := json.RawMessage(`{"action":"approve"}`)
	entry.Payload = &conflicting
	require.ErrorContains(t, c.aguiPreflightApproval(ctx, record.ThreadID, interrupt, entry), "different answer")
}

func TestAGUIApprovalExecutesOnceAndReplaysReceipt(t *testing.T) {
	ctx := recoveryContext()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	executor := &approvalCountingRegistry{stubRegistry: stubRegistry{result: "approved result"}}
	c := &backendClient{data: data.NewService(server), conv: conv, registry: executor}
	root := conversation.NewConversation()
	root.SetId("approval-thread")
	root.SetCreatedByUserID("owner")
	root.SetStatus("active")
	require.NoError(t, conv.PatchConversations(ctx, root))
	turn := conversation.NewTurn()
	turn.SetId("approval-turn")
	turn.SetConversationID("approval-thread")
	turn.SetStatus("waiting_for_user")
	require.NoError(t, conv.PatchTurn(ctx, turn))
	for _, id := range []string{"approval-assistant", "approval-tool"} {
		m := conversation.NewMessage()
		m.SetId(id)
		m.SetConversationID("approval-thread")
		m.SetTurnID("approval-turn")
		m.SetRole("assistant")
		m.SetType("text")
		if id == "approval-tool" {
			m.SetRole("tool")
			m.SetType("tool_call")
			m.SetParentMessageID("approval-assistant")
		}
		require.NoError(t, conv.PatchMessage(ctx, m))
	}
	payload := conversation.NewPayload()
	payload.SetId("approval-args")
	payload.SetKind("tool_request")
	payload.SetMimeType("application/json")
	payload.SetStorage("inline")
	payload.SetSizeBytes(2)
	payload.SetInlineBody([]byte(`{}`))
	require.NoError(t, conv.PatchPayload(ctx, payload))
	call := conversation.NewToolCall()
	call.SetMessageID("approval-tool")
	call.SetTurnID("approval-turn")
	call.SetOpID("backend-op")
	call.SetToolName("backend")
	call.SetToolKind("function")
	call.SetStatus("waiting_for_user")
	request := "approval-args"
	call.RequestPayloadID = &request
	call.Has.RequestPayloadID = true
	require.NoError(t, conv.PatchToolCall(ctx, call))
	queue := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
	queue.SetId("approval-q")
	queue.SetUserId("owner")
	queue.SetToolName("backend")
	queue.SetArguments([]byte(`{}`))
	queue.SetStatus("pending")
	queue.SetConversationId("approval-thread")
	queue.SetTurnId("approval-turn")
	queue.SetMessageId("approval-assistant")
	queue.SetMetadata([]byte(`{"opId":"backend-op"}`))
	require.NoError(t, conv.PatchToolApprovalQueue(ctx, queue))
	record := &aguistore.Run{ThreadID: "approval-thread", ConversationID: "approval-thread", TurnID: "approval-turn", RunID: "wire", Principal: "owner", Input: json.RawMessage(`{"threadId":"approval-thread","runId":"wire","messages":[],"tools":[{"name":"backend","description":"collision","parameters":{}}]}`)}
	inspected, err := c.aguiInspectRun(ctx, record)
	require.NoError(t, err)
	require.Empty(t, inspected.Pending.ClientTools)
	require.Len(t, inspected.Pending.Interrupts, 1)
	interrupt := inspected.Pending.Interrupts[0]
	raw := json.RawMessage(`{"action":"approve"}`)
	entry := agui.WireResumeEntry{InterruptId: interrupt.ID, Status: "resolved", Payload: &raw}
	require.NoError(t, c.aguiPreflightApproval(ctx, record.ThreadID, interrupt, entry))
	ready := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { <-ready; _, e := c.aguiApplyApproval(ctx, record, interrupt, entry); results <- e }()
	}
	close(ready)
	firstErr, secondErr := <-results, <-results
	require.True(t, firstErr == nil || secondErr == nil, "both claims failed: %v / %v", firstErr, secondErr)
	require.EqualValues(t, 1, executor.calls.Load())
	original, err := conv.GetMessage(ctx, "approval-tool", conversation.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "backend-op", original.MessageToolCall.OpId)
	require.Equal(t, "completed", original.MessageToolCall.Status)
	nativeTurn, err := c.data.GetTurnByID(ctx, &turnmodel.TurnLookupInput{ID: record.TurnID, ConversationID: record.ThreadID, Has: &turnmodel.TurnLookupInputHas{ID: true, ConversationID: true}}, principalDataOpts(ctx)...)
	require.NoError(t, err)
	require.Equal(t, "waiting_for_user", nativeTurn.Status)
	_, err = c.aguiApplyApproval(ctx, record, interrupt, entry)
	require.NoError(t, err)
	require.EqualValues(t, 1, executor.calls.Load())
	conflicting := json.RawMessage(`{"action":"reject"}`)
	entry.Payload = &conflicting
	require.ErrorContains(t, c.aguiPreflightApproval(ctx, record.ThreadID, interrupt, entry), "different answer")
	entry.Payload = &raw
	stored, err := c.aguiApprovalRow(ctx, record.ThreadID, interrupt)
	require.NoError(t, err)
	metadata := aguiApprovalMetadata(stored)
	delete(metadata, "aguiOutcome")
	partial := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
	partial.SetId(stored.Id)
	partial.SetUserId(stored.UserId)
	partial.SetMetadata(rawAGUI(metadata))
	require.NoError(t, conv.PatchToolApprovalQueue(ctx, partial))
	_, err = c.aguiApplyApproval(ctx, record, interrupt, entry)
	require.ErrorContains(t, err, "uncertain")
	require.EqualValues(t, 1, executor.calls.Load())
}

type approvalCountingRegistry struct {
	stubRegistry
	calls atomic.Int64
}

func (s *approvalCountingRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	s.calls.Add(1)
	return s.stubRegistry.Execute(ctx, name, args)
}
