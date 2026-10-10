package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	cancelstore "github.com/viant/agently-core/app/store/conversation/cancel"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	runmodel "github.com/viant/agently-core/model/run"
	"github.com/viant/agently-core/protocol/agent/execution"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/elicitation"
	"github.com/viant/agently-core/service/elicitation/router"
	"github.com/viant/agently-core/service/reactor"
	"github.com/viant/mcp-protocol/schema"
)

// Reproduce the expired message/askUser waiter against the native answer receipt
// writer, then submit the answer through the same entry point as the HTTP API.
func TestResolveElicitationResumesExpiredAskUserOnce(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "user"})
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	svc, _, _, finder := continuationFixture(t)
	svc.conversation = conv
	svc.cancelReg = cancelstore.NewMemory()
	svc.dataService = data.NewService(server)
	requests := make(chan *llm.GenerateRequest, 1)
	svc.llm = core.New(elicitationCaptureFinder{base: finder, requests: requests}, nil, conv)
	svc.orchestrator = reactor.New(svc.llm, nil, conv, nil, nil)
	root := apiconv.NewConversation()
	root.SetId("resume-conv")
	root.SetCreatedByUserID("user")
	root.SetStatus("waiting_for_user")
	require.NoError(t, conv.PatchConversations(ctx, root))
	turn := apiconv.NewTurn()
	turn.SetId("resume-turn")
	turn.SetConversationID("resume-conv")
	turn.SetStatus("waiting_for_user")
	require.NoError(t, conv.PatchTurn(ctx, turn))
	run := &runmodel.MutableRunView{}
	run.SetId("resume-turn")
	run.SetTurnID("resume-turn")
	run.SetConversationID("resume-conv")
	run.SetAgentID("agent")
	run.SetEffectiveUserID("user")
	run.SetStatus("completed")
	run.SetIteration(2)
	run.SetAttempt(1)
	run.SetLeaseOwner("original-owner")
	run.SetCompletedAt(time.Now())
	_, err = svc.dataService.PatchRuns(ctx, []*runmodel.MutableRunView{run})
	require.NoError(t, err)
	model := apiconv.NewMessage()
	model.SetId("expired-model")
	model.SetConversationID("resume-conv")
	model.SetTurnID("resume-turn")
	model.SetRole("assistant")
	model.SetType("text")
	model.SetContent("Choose your segment")
	require.NoError(t, conv.PatchMessage(ctx, model))
	modelCall := apiconv.NewModelCall()
	modelCall.SetMessageID("expired-model")
	modelCall.SetTurnID("resume-turn")
	modelCall.SetProvider("mock")
	modelCall.SetModel("mock")
	modelCall.SetModelKind("chat")
	modelCall.SetStatus("completed")
	modelCall.SetIteration(2)
	require.NoError(t, conv.PatchModelCall(ctx, modelCall))
	tool := apiconv.NewMessage()
	tool.SetId("expired-tool")
	tool.SetConversationID("resume-conv")
	tool.SetTurnID("resume-turn")
	tool.SetParentMessageID("expired-model")
	tool.SetRole("tool")
	tool.SetType("tool_op")
	tool.SetContent("elicitation failed: context deadline exceeded")
	require.NoError(t, conv.PatchMessage(ctx, tool))
	toolCall := apiconv.NewToolCall()
	toolCall.SetMessageID("expired-tool")
	toolCall.SetTurnID("resume-turn")
	toolCall.SetOpID("ask-op")
	toolCall.SetToolName("message/askUser")
	toolCall.SetToolKind("general")
	toolCall.SetIteration(2)
	timeoutBody := []byte(`{"error":"elicitation failed: context deadline exceeded"}`)
	timeoutPayload := apiconv.NewPayload()
	timeoutPayload.SetId("original-timeout-output")
	timeoutPayload.SetKind("tool_response")
	timeoutPayload.SetMimeType("application/json")
	timeoutPayload.SetStorage("inline")
	timeoutPayload.SetSizeBytes(len(timeoutBody))
	timeoutPayload.SetInlineBody(timeoutBody)
	require.NoError(t, conv.PatchPayload(ctx, timeoutPayload))
	toolCall.ResponsePayloadID = func() *string { value := "original-timeout-output"; return &value }()
	toolCall.Has.ResponsePayloadID = true
	toolCall.SetStatus("failed")
	toolCall.SetErrorMessage("elicitation failed: context deadline exceeded")
	require.NoError(t, conv.PatchToolCall(ctx, toolCall))
	svc.elicitation = elicitation.New(conv, nil, router.New(), elicitation.NoopAwaiterFactory())
	request := &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{Message: "Choose your segment", ElicitationId: "expired-ask"}}
	_, err = svc.elicitation.Record(requestctx.WithModelMessageID(ctx, "expired-model"), &requestctx.TurnMeta{ConversationID: "resume-conv", TurnID: "resume-turn"}, "assistant", request)
	require.NoError(t, err)
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	_, _, err = svc.elicitation.Wait(waitCtx, "resume-conv", "expired-ask")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, svc.elicitation.HasLiveWaiter("resume-conv", "expired-ask"))
	payload := map[string]interface{}{"segmentId": "viant.taxonomy:1489641"}
	wrongOwner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "other"})
	require.Error(t, svc.ResolveElicitation(wrongOwner, "resume-conv", "expired-ask", "accept", payload))
	require.EqualValues(t, 0, finder.calls.Load())
	resolveCtx, cancelRequest := context.WithCancel(ctx)
	require.NoError(t, svc.ResolveElicitation(resolveCtx, "resume-conv", "expired-ask", "accept", payload))
	cancelRequest() // Closing the HTTP request must not cancel the admitted continuation.
	require.Eventually(t, func() bool { return finder.calls.Load() == 1 }, time.Second, 10*time.Millisecond, "accepted answer must resume the expired execution")
	receipt, err := svc.elicitation.InspectResolution(ctx, "resume-conv", "expired-ask", "accept", payload, "")
	require.NoError(t, err)
	require.NotNil(t, receipt)
	var saved map[string]interface{}
	require.NoError(t, json.Unmarshal(receipt.Payload, &saved))
	require.Equal(t, payload, saved)
	select {
	case observed := <-requests:
		raw, marshalErr := json.Marshal(observed)
		require.NoError(t, marshalErr)
		require.Contains(t, string(raw), "viant.taxonomy:1489641", "resumed model must receive the saved selection")
		foundToolResult := false
		for _, message := range observed.Messages {
			if message.Role != llm.RoleTool {
				continue
			}
			require.NotContains(t, llm.MessageText(message), "context deadline exceeded", "active model tool result must reflect the accepted answer")
			var result map[string]interface{}
			if json.Unmarshal([]byte(llm.MessageText(message)), &result) == nil && result["action"] == "accept" {
				require.Equal(t, payload, result["payload"])
				foundToolResult = true
			}
		}
		require.True(t, foundToolResult, "resumed model must receive canonical successful askUser output")
	case <-time.After(time.Second):
		t.Fatal("resumed model request not captured")
	}
	require.Eventually(t, func() bool {
		current, err := conv.GetConversation(ctx, "resume-conv", apiconv.WithIncludeTranscript(true))
		return err == nil && len(current.GetTranscript()) == 1 && current.GetTranscript()[0].Status == "succeeded"
	}, 2*time.Second, 10*time.Millisecond)
	actual, err := conv.GetConversation(ctx, "resume-conv", apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Len(t, actual.GetTranscript(), 1)
	require.Equal(t, "resume-turn", actual.GetTranscript()[0].Id)
	require.Equal(t, "succeeded", actual.GetTranscript()[0].Status)
	require.NoError(t, svc.ResolveElicitation(ctx, "resume-conv", "expired-ask", "accept", payload))
	require.EqualValues(t, 1, finder.calls.Load(), "duplicate answer must not start another continuation")
	require.Error(t, svc.ResolveElicitation(ctx, "resume-conv", "expired-ask", "accept", map[string]interface{}{"segmentId": "other"}))
	require.EqualValues(t, 1, finder.calls.Load())
	auditedTool, err := conv.GetMessage(ctx, "expired-tool", apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "completed", messageToolCall(auditedTool).Status)
	require.Empty(t, valueOrEmpty(messageToolCall(auditedTool).ErrorMessage))
	require.NotNil(t, messageToolCall(auditedTool).ResponsePayloadId)
	require.NotEqual(t, "original-timeout-output", *messageToolCall(auditedTool).ResponsePayloadId)
	response, err := conv.GetPayload(ctx, *messageToolCall(auditedTool).ResponsePayloadId)
	require.NoError(t, err)
	require.NotNil(t, response.InlineBody)
	expectedBody, err := json.Marshal(map[string]interface{}{"action": "accept", "payload": payload})
	require.NoError(t, err)
	require.JSONEq(t, string(expectedBody), string(*response.InlineBody))
	require.JSONEq(t, string(expectedBody), auditedTool.GetContent())
	originalTimeout, err := conv.GetPayload(ctx, "original-timeout-output")
	require.NoError(t, err)
	require.Equal(t, timeoutBody, *originalTimeout.InlineBody)
}

func TestResolveElicitationWakesLiveWaiterWithoutContinuation(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "user"})
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	root := apiconv.NewConversation()
	root.SetId("resume-conv")
	root.SetCreatedByUserID("user")
	require.NoError(t, conv.PatchConversations(ctx, root))
	turn := apiconv.NewTurn()
	turn.SetId("resume-turn")
	turn.SetConversationID("resume-conv")
	turn.SetStatus("waiting_for_user")
	require.NoError(t, conv.PatchTurn(ctx, turn))
	svc, _, _, finder := continuationFixture(t)
	svc.conversation = conv
	svc.cancelReg = cancelstore.NewMemory()
	r := router.New()
	svc.elicitation = elicitation.New(conv, nil, r, elicitation.NoopAwaiterFactory())
	request := &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{Message: "Choose", ElicitationId: "live-ask"}}
	_, err = svc.elicitation.Record(ctx, &requestctx.TurnMeta{ConversationID: "resume-conv", TurnID: "resume-turn"}, "assistant", request)
	require.NoError(t, err)
	wake := make(chan *schema.ElicitResult, 1)
	r.RegisterByElicitationID("resume-conv", "live-ask", wake)
	defer r.RemoveByElicitation("resume-conv", "live-ask")
	payload := map[string]interface{}{"segmentId": "viant.taxonomy:1489641"}
	require.NoError(t, svc.ResolveElicitation(ctx, "resume-conv", "live-ask", "accept", payload))
	select {
	case answer := <-wake:
		require.Equal(t, payload, answer.Content)
	case <-time.After(time.Second):
		t.Fatal("accepted answer did not wake the live execution")
	}
	require.EqualValues(t, 0, finder.calls.Load(), "live execution must consume its own answer")
}

type elicitationCaptureFinder struct {
	base     *singleResponseFinder
	requests chan *llm.GenerateRequest
}

func (f elicitationCaptureFinder) Find(context.Context, string) (llm.Model, error) {
	return elicitationCaptureModel{base: singleResponseModel{finder: f.base}, requests: f.requests}, nil
}

type elicitationCaptureModel struct {
	base     singleResponseModel
	requests chan *llm.GenerateRequest
}

func (m elicitationCaptureModel) Generate(ctx context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.requests <- request
	return m.base.Generate(ctx, request)
}
func (elicitationCaptureModel) Implements(string) bool { return false }
