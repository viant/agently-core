package agent

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	runmodel "github.com/viant/agently-core/model/run"
	turnmodel "github.com/viant/agently-core/model/turn"
	"github.com/viant/agently-core/protocol/agent/execution"
	"github.com/viant/agently-core/runtime/clienttool"
	projection "github.com/viant/agently-core/runtime/projection"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/elicitation"
	"github.com/viant/agently-core/service/shared/toolexec"
	"github.com/viant/mcp-protocol/schema"
	"testing"
	"time"
)

func TestCancelWaitingTurnRepairsNativeElicitationReceiptAfterTurnWriteFailure(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "user"})
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	root := apiconv.NewConversation()
	root.SetId("cancel-native")
	root.SetCreatedByUserID("user")
	root.SetStatus("active")
	require.NoError(t, conv.PatchConversations(ctx, root))
	turn := apiconv.NewTurn()
	turn.SetId("cancel-native-turn")
	turn.SetConversationID("cancel-native")
	turn.SetStatus("waiting_for_user")
	require.NoError(t, conv.PatchTurn(ctx, turn))
	store := data.NewService(server)
	run := &runmodel.MutableRunView{}
	run.SetId("cancel-native-turn")
	run.SetTurnID("cancel-native-turn")
	run.SetConversationID("cancel-native")
	run.SetStatus("completed")
	run.SetAttempt(1)
	run.SetLeaseOwner("original-owner")
	run.SetEffectiveUserID("user")
	_, err = store.PatchRuns(ctx, []*runmodel.MutableRunView{run})
	require.NoError(t, err)
	elic := elicitation.New(conv, nil, nil, elicitation.NoopAwaiterFactory())
	session, err := clienttool.NewSession([]llm.ToolDefinition{{Name: "message/askUser"}})
	require.NoError(t, err)
	callCtx := clienttool.WithSession(requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "cancel-native", TurnID: "cancel-native-turn"}), session)
	_, _, err = toolexec.ExecuteToolStep(callCtx, nil, toolexec.StepInfo{ID: "ask-op", Name: "message/askUser", Args: map[string]interface{}{}}, conv)
	require.NoError(t, err)
	pending := session.Pending()[0]
	request := &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{Message: "Choose ID", ElicitationId: "pending-ask"}}
	_, err = elic.Record(ctx, &requestctx.TurnMeta{ConversationID: "cancel-native", TurnID: "cancel-native-turn"}, "assistant", request)
	require.NoError(t, err)
	svc := &Service{conversation: &failCancellationProjection{Client: conv, point: "turn"}, dataService: store, elicitation: elic}
	canceled, err := svc.CancelWaitingTurn(ctx, "cancel-native", "cancel-native-turn")
	require.Error(t, err)
	require.False(t, canceled)
	receipt, err := elic.InspectResolution(ctx, "cancel-native", "pending-ask", "cancel", nil, "")
	require.NoError(t, err)
	require.NotNil(t, receipt)
	restarted := &Service{conversation: conv, dataService: store, elicitation: elic}
	canceled, err = restarted.CancelWaitingTurn(ctx, "cancel-native", "cancel-native-turn")
	require.NoError(t, err)
	require.True(t, canceled)
	actual, err := conv.GetConversation(ctx, "cancel-native", apiconv.WithIncludeTranscript(true))
	require.NoError(t, err)
	require.Equal(t, "canceled", actual.GetTranscript()[0].Status)
	tool, err := conv.GetMessage(ctx, pending.ToolMessageID, apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "ask-op", messageToolCall(tool).OpId)
	require.Equal(t, "failed", messageToolCall(tool).Status)
	require.NotNil(t, messageToolCall(tool).ResponsePayloadId)
	require.Equal(t, "Tool cancelled by user", tool.GetContent())
	for _, message := range actual.GetTranscript()[0].Message {
		require.NotEqual(t, "user", message.Role)
		if message.ElicitationId != nil {
			require.Equal(t, "cancel", valueOrEmpty(message.Status))
		}
	}
	require.Error(t, func() error {
		_, e := elic.InspectResolution(ctx, "cancel-native", "pending-ask", "accept", nil, "")
		return e
	}())
}

func TestCancelWaitingTurnPreservesAcceptedNativeReceiptDuringCleanupRepair(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "user"})
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	root := apiconv.NewConversation()
	root.SetId("cancel-native")
	root.SetCreatedByUserID("user")
	root.SetStatus("active")
	require.NoError(t, conv.PatchConversations(ctx, root))
	turn := apiconv.NewTurn()
	turn.SetId("cancel-native-turn")
	turn.SetConversationID("cancel-native")
	turn.SetStatus("waiting_for_user")
	require.NoError(t, conv.PatchTurn(ctx, turn))
	store := data.NewService(server)
	run := &runmodel.MutableRunView{}
	run.SetId("cancel-native-turn")
	run.SetTurnID("cancel-native-turn")
	run.SetConversationID("cancel-native")
	run.SetStatus("completed")
	run.SetAttempt(1)
	run.SetLeaseOwner("original-owner")
	run.SetEffectiveUserID("user")
	_, err = store.PatchRuns(ctx, []*runmodel.MutableRunView{run})
	require.NoError(t, err)
	elic := elicitation.New(conv, nil, nil, elicitation.NoopAwaiterFactory())
	session, err := clienttool.NewSession([]llm.ToolDefinition{{Name: "message/askUser"}})
	require.NoError(t, err)
	callCtx := clienttool.WithSession(requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "cancel-native", TurnID: "cancel-native-turn"}), session)
	_, _, err = toolexec.ExecuteToolStep(callCtx, nil, toolexec.StepInfo{ID: "ask-op", Name: "message/askUser", Args: map[string]interface{}{}}, conv)
	require.NoError(t, err)
	pending := session.Pending()[0]
	request := &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{Message: "Choose ID", ElicitationId: "pending-ask"}}
	_, err = elic.Record(ctx, &requestctx.TurnMeta{ConversationID: "cancel-native", TurnID: "cancel-native-turn"}, "assistant", request)
	require.NoError(t, err)
	stalePending, err := conv.GetConversation(ctx, "cancel-native", apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	_, err = elic.ResolveChecked(ctx, "cancel-native", "pending-ask", "accept", map[string]interface{}{"orderId": 2659534}, "")
	require.NoError(t, err)
	before, err := conv.GetConversation(ctx, "cancel-native", apiconv.WithIncludeTranscript(true))
	require.NoError(t, err)
	userCount := 0
	originalAnswers := map[string]string{}
	for _, message := range before.GetTranscript()[0].Message {
		if message.Role == "user" {
			userCount++
			originalAnswers[message.Id] = (*apiconv.Message)(message).GetContent()
		}
	}
	svc := &Service{conversation: &failCancellationProjection{Client: &staleCancellationSnapshot{Client: conv, snapshot: stalePending}, point: "turn"}, dataService: store, elicitation: elic}
	canceled, err := svc.CancelWaitingTurn(ctx, "cancel-native", "cancel-native-turn")
	require.Error(t, err)
	require.False(t, canceled)
	receipt, err := elic.InspectResolution(ctx, "cancel-native", "pending-ask", "accept", map[string]interface{}{"orderId": 2659534}, "")
	require.NoError(t, err)
	require.NotNil(t, receipt)
	restarted := &Service{conversation: conv, dataService: store, elicitation: elic}
	canceled, err = restarted.CancelWaitingTurn(ctx, "cancel-native", "cancel-native-turn")
	require.NoError(t, err)
	require.True(t, canceled)
	actual, err := conv.GetConversation(ctx, "cancel-native", apiconv.WithIncludeTranscript(true))
	require.NoError(t, err)
	require.Equal(t, "canceled", actual.GetTranscript()[0].Status)
	tool, err := conv.GetMessage(ctx, pending.ToolMessageID, apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "ask-op", messageToolCall(tool).OpId)
	require.Equal(t, "failed", messageToolCall(tool).Status)
	require.NotNil(t, messageToolCall(tool).ResponsePayloadId)
	require.Equal(t, "Tool cancelled by user", tool.GetContent())
	afterUserCount := 0
	for _, message := range actual.GetTranscript()[0].Message {
		if message.Role == "user" {
			afterUserCount++
			require.Equal(t, originalAnswers[message.Id], (*apiconv.Message)(message).GetContent())
		}
		if message.ElicitationId != nil && message.Role != "user" {
			require.Equal(t, "accepted", valueOrEmpty(message.Status))
		}
	}
	require.Equal(t, userCount, afterUserCount)
	finalReceipt, err := elic.InspectResolution(ctx, "cancel-native", "pending-ask", "accept", map[string]interface{}{"orderId": 2659534}, "")
	require.NoError(t, err)
	require.Equal(t, receipt, finalReceipt)
	require.Error(t, func() error {
		_, e := elic.InspectResolution(ctx, "cancel-native", "pending-ask", "cancel", nil, "")
		return e
	}())
}

type staleCancellationSnapshot struct {
	apiconv.Client
	snapshot *apiconv.Conversation
}

func (s *staleCancellationSnapshot) GetConversation(ctx context.Context, id string, options ...apiconv.Option) (*apiconv.Conversation, error) {
	if s.snapshot != nil {
		snapshot := s.snapshot
		s.snapshot = nil
		return snapshot, nil
	}
	return s.Client.GetConversation(ctx, id, options...)
}

func TestCancelWaitingTurnClosesOriginalClientToolOutputAndRetryDoesNotDuplicate(t *testing.T) {
	svc, _, store, _ := continuationFixture(t)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "user"})
	session, err := clienttool.NewSession([]llm.ToolDefinition{{Name: "frontend"}})
	require.NoError(t, err)
	assistant := apiconv.NewMessage()
	assistant.SetId("assistant")
	assistant.SetConversationID("resume-conv")
	assistant.SetTurnID("resume-turn")
	assistant.SetRole("assistant")
	assistant.SetContent("Calling frontend")
	require.NoError(t, svc.conversation.PatchMessage(ctx, assistant))
	callCtx := clienttool.WithSession(requestctx.WithModelMessageID(requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "resume-conv", TurnID: "resume-turn"}), "assistant"), session)
	_, _, err = toolexec.ExecuteToolStep(callCtx, nil, toolexec.StepInfo{ID: "pending-call", Name: "frontend", Args: map[string]interface{}{}}, svc.conversation)
	require.NoError(t, err)
	pending := session.Pending()[0]
	canceled, err := svc.CancelWaitingTurn(ctx, "resume-conv", "resume-turn")
	require.NoError(t, err)
	require.True(t, canceled)
	message, err := svc.conversation.GetMessage(ctx, pending.ToolMessageID, apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	call := messageToolCall(message)
	require.NotNil(t, call)
	require.Equal(t, "failed", call.Status)
	require.NotNil(t, call.ResponsePayloadId)
	require.Equal(t, "Tool cancelled by user", valueOrEmpty(call.ErrorMessage))
	require.Equal(t, pending.ToolMessageID, message.Id)
	require.Equal(t, "Tool cancelled by user", message.GetContent())
	conv, err := svc.conversation.GetConversation(ctx, "resume-conv", apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	normalized, _ := svc.collectNormalizedMessages(ctx, conv.GetTranscript(), false, "resume-turn", false, nil, projection.ContextProjection{})
	found := false
	for _, entry := range normalized {
		if entry.msg != nil && entry.msg.Id == pending.ToolMessageID {
			found = true
			require.NotEmpty(t, entry.msg.GetContent())
			require.Equal(t, pending.ID, messageToolCall(entry.msg).OpId)
			require.Equal(t, "Tool cancelled by user", valueOrEmpty(messageToolCall(entry.msg).ErrorMessage))
		}
	}
	require.True(t, found)
	future, _ := svc.collectNormalizedMessages(ctx, conv.GetTranscript(), false, "next-turn", false, nil, projection.ContextProjection{})
	require.Empty(t, future)
	require.Equal(t, "canceled", store.row.Status)
	canceled, err = svc.CancelWaitingTurn(ctx, "resume-conv", "resume-turn")
	require.NoError(t, err)
	require.False(t, canceled)
	after, err := svc.conversation.GetMessage(ctx, pending.ToolMessageID, apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, call.ResponsePayloadId, messageToolCall(after).ResponsePayloadId)
}

type failCancellationProjection struct {
	apiconv.Client
	point  string
	failed bool
}

func (c *failCancellationProjection) PatchMessage(ctx context.Context, message *apiconv.MutableMessage) error {
	if !c.failed && ((c.point == "tool" && message.Status != nil && *message.Status == "failed") || (c.point == "starter" && message.Status != nil && *message.Status == "cancel")) {
		c.failed = true
		return fmt.Errorf("injected %s projection failure", c.point)
	}
	return c.Client.PatchMessage(ctx, message)
}
func (c *failCancellationProjection) PatchTurn(ctx context.Context, turn *apiconv.MutableTurn) error {
	if !c.failed && c.point == "turn" && turn.Status == "canceled" {
		c.failed = true
		return fmt.Errorf("injected turn projection failure")
	}
	return c.Client.PatchTurn(ctx, turn)
}
func TestCancelWaitingTurnRepairsPartialCleanupAfterServiceRestart(t *testing.T) {
	for _, point := range []string{"tool", "starter", "turn"} {
		t.Run(point, func(t *testing.T) {
			svc, _, store, _ := continuationFixture(t)
			ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "user"})
			starter := apiconv.NewMessage()
			starter.SetId("starter")
			starter.SetConversationID("resume-conv")
			starter.SetTurnID("resume-turn")
			starter.SetRole("user")
			starter.SetStatus("running")
			starter.SetContent("original")
			require.NoError(t, svc.conversation.PatchMessage(ctx, starter))
			session, err := clienttool.NewSession([]llm.ToolDefinition{{Name: "frontend"}})
			require.NoError(t, err)
			callCtx := clienttool.WithSession(requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "resume-conv", TurnID: "resume-turn"}), session)
			_, _, err = toolexec.ExecuteToolStep(callCtx, nil, toolexec.StepInfo{ID: "pending-call", Name: "frontend", Args: map[string]interface{}{}}, svc.conversation)
			require.NoError(t, err)
			pending := session.Pending()[0]
			original := svc.conversation
			svc.conversation = &failCancellationProjection{Client: original, point: point}
			canceled, err := svc.CancelWaitingTurn(ctx, "resume-conv", "resume-turn")
			require.Error(t, err)
			require.False(t, canceled)
			require.Equal(t, "canceled", store.row.Status)
			restarted := &Service{conversation: original, dataService: store}
			canceled, err = restarted.CancelWaitingTurn(ctx, "resume-conv", "resume-turn")
			require.NoError(t, err)
			require.True(t, canceled)
			message, err := original.GetMessage(ctx, pending.ToolMessageID, apiconv.WithIncludeToolCall(true))
			require.NoError(t, err)
			require.Equal(t, "failed", messageToolCall(message).Status)
			require.Equal(t, "failed", valueOrEmpty(message.Status))
			require.NotNil(t, messageToolCall(message).ResponsePayloadId)
			start, err := original.GetMessage(ctx, "starter")
			require.NoError(t, err)
			require.Equal(t, "cancel", valueOrEmpty(start.Status))
			canceled, err = restarted.CancelWaitingTurn(ctx, "resume-conv", "resume-turn")
			require.NoError(t, err)
			require.False(t, canceled)
		})
	}
}

func TestCancelWaitingTurnWinsAgainstStaleContinuationWithoutModelWork(t *testing.T) {
	svc, _, store, finder := continuationFixture(t)
	stale, err := store.GetRun(context.Background(), "resume-turn", nil)
	require.NoError(t, err)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "user"})
	canceled, err := svc.CancelWaitingTurn(ctx, "resume-conv", "resume-turn")
	require.NoError(t, err)
	require.True(t, canceled)
	require.Equal(t, "canceled", store.row.Status)
	_, err = svc.claimContinuationRun(ctx, stale)
	require.Error(t, err)
	conv, err := svc.conversation.GetConversation(ctx, "resume-conv", apiconv.WithIncludeTranscript(true))
	require.NoError(t, err)
	require.Equal(t, "canceled", conv.GetTranscript()[0].Status)
	require.Zero(t, finder.calls.Load())
	canceled, err = svc.CancelWaitingTurn(ctx, "resume-conv", "resume-turn")
	require.NoError(t, err)
	require.False(t, canceled)
}

func TestCancelWaitingTurnRejectsWrongOwnerAndLostClaim(t *testing.T) {
	for _, failure := range []string{"owner", "claim"} {
		t.Run(failure, func(t *testing.T) {
			svc, _, store, finder := continuationFixture(t)
			principal := "user"
			if failure == "owner" {
				principal = "other"
			} else {
				store.loseClaim = true
			}
			ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: principal})
			canceled, err := svc.CancelWaitingTurn(ctx, "resume-conv", "resume-turn")
			require.Error(t, err)
			require.False(t, canceled)
			require.Equal(t, "completed", store.row.Status)
			require.Zero(t, finder.calls.Load())
		})
	}
}

func TestCancelWaitingTurnUsesNativeDatlyCASAndFencesStaleResume(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "user"})
	native, err := data.NewThinServiceInMemory(ctx)
	require.NoError(t, err)
	conv := conversationmodel.NewMutableConversationView(conversationmodel.WithConversationID("resume-conv"))
	_, err = native.PatchConversations(ctx, []*conversationmodel.MutableConversationView{conv})
	require.NoError(t, err)
	turn := apiconv.NewTurn()
	turn.SetId("resume-turn")
	turn.SetConversationID("resume-conv")
	turn.SetStatus("waiting_for_user")
	_, err = native.PatchTurns(ctx, []*turnmodel.MutableTurnView{turn})
	require.NoError(t, err)
	run := &runmodel.MutableRunView{}
	run.SetId("resume-turn")
	run.SetTurnID("resume-turn")
	run.SetConversationID("resume-conv")
	run.SetStatus("completed")
	run.SetAttempt(1)
	run.SetLeaseOwner("old-owner")
	run.SetEffectiveUserID("user")
	run.SetCompletedAt(time.Now())
	_, err = native.PatchRuns(ctx, []*runmodel.MutableRunView{run})
	require.NoError(t, err)
	stale, err := native.GetRun(ctx, "resume-turn", nil)
	require.NoError(t, err)
	svc, _, _, _ := continuationFixture(t)
	svc.dataService = native
	canceled, err := svc.CancelWaitingTurn(ctx, "resume-conv", "resume-turn")
	require.NoError(t, err)
	require.True(t, canceled)
	_, err = svc.claimContinuationRun(ctx, stale)
	require.Error(t, err)
	actual, err := native.GetRun(ctx, "resume-turn", nil)
	require.NoError(t, err)
	require.Equal(t, "canceled", actual.Status)
}
func TestCancelWaitingTurnFencesIntermediateResumeClaimPhase(t *testing.T) {
	svc, _, store, finder := continuationFixture(t)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "user"})
	resumeOwner := "intermediate-resume-owner"
	store.row.LeaseOwner = &resumeOwner
	store.row.Attempt = 2
	canceled, err := svc.CancelWaitingTurn(ctx, "resume-conv", "resume-turn")
	require.NoError(t, err)
	require.True(t, canceled)
	phase := &runmodel.MutableRunView{}
	phase.SetId("resume-turn")
	phase.SetStatus("running")
	phase.SetCondition(runmodel.RunPatchCondition{LeaseOwner: &resumeOwner})
	_, err = store.PatchRuns(ctx, []*runmodel.MutableRunView{phase})
	require.NoError(t, err)
	require.Equal(t, "canceled", store.row.Status)
	require.Zero(t, finder.calls.Load())
}
