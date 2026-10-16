package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	receiptstore "github.com/viant/agently-core/app/store/elicitationreceipt"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/service/elicitation"
	"github.com/viant/datly/exec"
)

// resolveElicitationAndContinue retains the atomic answer receipt, but also
// admits a same-turn continuation when a timed-out askUser no longer has a
// waiter. AG-UI has its own durable coordinator and uses ResolveChecked directly.
func (s *Service) resolveElicitationAndContinue(ctx context.Context, conversationID, id, action string, payload map[string]interface{}) error {
	if _, native := s.conversation.(interface{ NativeComponentInvoker() exec.ComponentInvoker }); !native {
		return s.elicitation.Resolve(ctx, conversationID, id, action, payload, "")
	}
	resolution, err := s.elicitation.ResolveChecked(ctx, conversationID, id, action, payload, "")
	if err != nil {
		return err
	}
	if resolution.Disposition == elicitation.ResolutionWokeExisting {
		return nil
	}
	settleCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		err = s.continueResolvedElicitation(ctx, resolution.Receipt)
		if !errors.Is(err, errElicitationSettling) {
			return err
		}
		select {
		case <-settleCtx.Done():
			return fmt.Errorf("answer saved; original execution is still settling, retry this answer to resume: %w", settleCtx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

var errElicitationSettling = errors.New("elicitation execution is settling")

func (s *Service) continueResolvedElicitation(ctx context.Context, receipt *receiptstore.Receipt) error {
	request, err := s.conversation.GetMessage(ctx, receipt.RequestMessageID, apiconv.WithIncludeToolCall(true))
	if err != nil {
		return err
	}
	if request == nil || request.ConversationId != receipt.AuthoritativeConversationID || request.TurnId == nil {
		return fmt.Errorf("accepted elicitation request has no owning turn")
	}
	conv, err := s.conversation.GetConversation(ctx, request.ConversationId, apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true), apiconv.WithIncludeModelCall(true))
	if err != nil {
		return err
	}
	if conv == nil {
		return fmt.Errorf("elicitation conversation unavailable")
	}
	var turn *apiconv.Turn
	for _, candidate := range conv.GetTranscript() {
		if candidate != nil && candidate.Id == *request.TurnId {
			turn = candidate
			break
		}
	}
	if turn == nil {
		return fmt.Errorf("elicitation turn unavailable")
	}
	if isTerminalArtifactStatus(turn.Status) {
		return nil
	}
	// Tie the receipt to the original failed call, never to a newer wait in the
	// same turn. The accepted answer replaces the expired wait result before
	// binding the next model iteration; its old payload remains in the audit store.
	iteration := 0
	var answeredTool *apiconv.Message
	for _, row := range indexTurnToolCalls(turn) {
		tool, err := s.conversation.GetMessage(ctx, row.messageID, apiconv.WithIncludeToolCall(true))
		if err != nil {
			return err
		}
		call := messageToolCall(tool)
		if tool == nil || call == nil || tool.ParentMessageId == nil || *tool.ParentMessageId != request.Id {
			continue
		}
		if call.ToolName != "message/askUser" && call.ToolName != "message-askUser" {
			continue
		}
		if !isTerminalArtifactStatus(call.Status) {
			return errElicitationSettling
		}
		if call.Status != "failed" || !strings.Contains(valueOrEmpty(call.ErrorMessage), "context deadline exceeded") {
			return nil
		}
		iteration = row.iteration
		answeredTool = tool
	}
	if iteration < 1 {
		return nil
	}
	if s.dataService == nil {
		return fmt.Errorf("elicitation continuation run store unavailable")
	}
	run, err := s.dataService.GetRun(ctx, turn.Id, nil)
	if err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("elicitation native run unavailable")
	}
	if run.Iteration > iteration {
		return nil
	}
	for _, message := range turn.Message {
		if modelCallIteration((*apiconv.Message)(message)) > iteration {
			return nil
		}
	}
	if run.Iteration != iteration || run.Status != "completed" || turn.Status != "waiting_for_user" {
		return errElicitationSettling
	}
	input := &QueryInput{ConversationID: request.ConversationId, UserId: receipt.Principal}
	// Claim before acknowledging the callback. The admitted execution has its
	// own cancellation registry and run lease, not the browser's 30s deadline.
	execute, err := s.prepareContinuation(context.WithoutCancel(ctx), input, turn.Id, iteration+1, &QueryOutput{}, func(runCtx context.Context) error { return s.completeResolvedAskUser(runCtx, answeredTool, receipt) })
	if err != nil {
		return err
	}
	go func() {
		if err := execute(); err != nil {
			logx.Errorf("conversation", "elicitation continuation convo=%q turn=%q: %v", request.ConversationId, turn.Id, err)
		}
	}()
	return nil
}

// completeResolvedAskUser materializes the committed receipt as the original
// tool result. Leaving the timeout in model history makes the model execute
// the skill's failed-elicitation fallback even though a selection was accepted.
func (s *Service) completeResolvedAskUser(ctx context.Context, message *apiconv.Message, receipt *receiptstore.Receipt) error {
	raw, err := json.Marshal(struct {
		Action  string          `json:"action"`
		Payload json.RawMessage `json:"payload,omitempty"`
	}{receipt.Action, receipt.Payload})
	if err != nil {
		return err
	}
	payload := apiconv.NewPayload()
	payload.SetId(uuid.NewString())
	payload.SetKind("tool_response")
	payload.SetMimeType("application/json")
	payload.SetStorage("inline")
	payload.SetInlineBody(raw)
	payload.SetSizeBytes(len(raw))
	if err = s.conversation.PatchPayload(ctx, payload); err != nil {
		return err
	}
	update := apiconv.NewMessage()
	update.SetId(message.Id)
	update.SetContent(string(raw))
	update.SetStatus("completed")
	if err = s.conversation.PatchMessage(ctx, update); err != nil {
		return err
	}
	call := apiconv.NewToolCall()
	call.SetMessageID(message.Id)
	call.SetStatus("completed")
	call.SetCompletedAt(time.Now())
	call.ResponsePayloadID = &payload.Id
	call.Has.ResponsePayloadID = true
	call.Has.ErrorMessage = true
	call.Has.ErrorCode = true
	return s.conversation.PatchToolCall(ctx, call)
}
