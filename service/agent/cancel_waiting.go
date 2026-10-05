package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	runmodel "github.com/viant/agently-core/model/run"
	"github.com/viant/agently-core/runtime/clienttool"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/shared/toolexec"
)

// CancelWaitingTurn terminates a persisted wait after its execution left the
// live cancellation registry. The run CAS competes with continuation admission.
func (s *Service) CancelWaitingTurn(ctx context.Context, conversationID, turnID string) (bool, error) {
	if s == nil || s.conversation == nil || s.dataService == nil {
		return false, fmt.Errorf("persisted cancellation is unavailable")
	}
	principal := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if principal == "" {
		return false, fmt.Errorf("persisted cancellation principal is required")
	}
	conv, err := s.conversation.GetConversation(ctx, conversationID, apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true), apiconv.WithIncludeModelCall(true))
	if err != nil {
		return false, err
	}
	if conv == nil {
		return false, fmt.Errorf("cancellation conversation not found")
	}
	if conv.CreatedByUserId != nil && strings.TrimSpace(*conv.CreatedByUserId) != "" && *conv.CreatedByUserId != principal {
		return false, fmt.Errorf("cancellation conversation owner mismatch")
	}
	var turn *apiconv.Turn
	for _, candidate := range conv.GetTranscript() {
		if candidate != nil && candidate.Id == turnID && candidate.ConversationId == conversationID {
			turn = candidate
			break
		}
	}
	if turn == nil {
		return false, fmt.Errorf("cancellation turn scope mismatch")
	}
	if turn.Status != "waiting_for_user" && turn.Status != "canceled" {
		return false, nil
	}
	run, err := s.dataService.GetRun(ctx, turnID, nil)
	if err != nil {
		return false, err
	}
	if run == nil || run.ConversationId == nil || *run.ConversationId != conversationID || run.EffectiveUserId == nil || *run.EffectiveUserId != principal {
		return false, fmt.Errorf("cancellation native run owner or scope mismatch")
	}
	owner := s.newRunLeaseOwner()
	if turn.Status == "canceled" && run.Status != "canceled" {
		return false, fmt.Errorf("canceled turn repair requires its canceled native run")
	}
	if run.Status == "completed" {
		attempt := run.Attempt
		if run.LeaseOwner == nil || *run.LeaseOwner == "" {
			return false, fmt.Errorf("cancellation native run lacks ownership token")
		}
		patch := &runmodel.MutableRunView{}
		patch.SetId(turnID)
		patch.SetLeaseOwner(owner)
		patch.SetAttempt(attempt + 1)
		patch.SetLeaseUntil(runLeaseTimestamp(time.Now()).Add(s.runLeaseDuration()))
		patch.SetLastHeartbeatAt(runLeaseTimestamp(time.Now()))
		patch.SetCondition(runmodel.RunPatchCondition{Status: "completed", Attempt: &attempt, LeaseOwner: run.LeaseOwner})
		if _, err = s.dataService.PatchRuns(ctx, []*runmodel.MutableRunView{patch}); err != nil {
			return false, err
		}
		confirmed, e := s.dataService.GetRun(ctx, turnID, nil)
		if e != nil {
			return false, e
		}
		if confirmed == nil || confirmed.Status != "completed" || confirmed.LeaseOwner == nil || *confirmed.LeaseOwner != owner {
			return false, fmt.Errorf("cancellation lost native run claim to continuation")
		}
		phase := &runmodel.MutableRunView{}
		phase.SetId(turnID)
		phase.SetStatus("canceled")
		phase.SetCompletedAt(time.Now())
		phase.SetCondition(runmodel.RunPatchCondition{LeaseOwner: &owner})
		if _, err = s.dataService.PatchRuns(ctx, []*runmodel.MutableRunView{phase}); err != nil {
			return false, err
		}
		confirmed, err = s.dataService.GetRun(ctx, turnID, nil)
		if err != nil {
			return false, err
		}
		if confirmed == nil || confirmed.Status != "canceled" || confirmed.LeaseOwner == nil || *confirmed.LeaseOwner != owner {
			return false, fmt.Errorf("cancellation lost native ownership during terminal phase")
		}
	} else if run.Status != "canceled" {
		return false, fmt.Errorf("waiting cancellation native run is not at a wait boundary")
	}
	// A canceled run cannot be claimed by continuation. Retrying after a lost
	// turn-write response repairs this projection without another run transition.
	meta := requestctx.TurnMeta{ConversationID: conversationID, TurnID: turnID}
	ctx = requestctx.WithTurnMeta(requestctx.WithConversationID(ctx, conversationID), meta)
	// Close the original tool messages before emitting terminal turn state. These
	// are paired outputs, not new model/user messages or a resumed execution.
	seenElicitations := map[string]bool{}
	closeElicitation := func(message *apiconv.Message) error {
		if message == nil || message.ElicitationId == nil || (valueOrEmpty(message.Status) != "pending" && valueOrEmpty(message.Status) != "waiting_for_user") {
			return nil
		}
		id := strings.TrimSpace(*message.ElicitationId)
		if id == "" || seenElicitations[id] {
			return nil
		}
		seenElicitations[id] = true
		if s.elicitation == nil {
			return fmt.Errorf("cancellation pending elicitation resolver unavailable")
		}
		_, e := s.elicitation.ResolveChecked(ctx, conversationID, id, "cancel", nil, "")
		if e != nil {
			// Preserve a concurrently committed answer rather than replace it.
			resolved, readErr := s.conversation.GetMessage(ctx, message.Id)
			if readErr == nil && resolved != nil && resolved.ConversationId == conversationID && resolved.ElicitationId != nil && *resolved.ElicitationId == id {
				switch strings.ToLower(valueOrEmpty(resolved.Status)) {
				case "accepted", "rejected", "cancel", "canceled", "cancelled":
					return nil
				}
			}
		}
		return e
	}
	for _, message := range turn.Message {
		if e := closeElicitation((*apiconv.Message)(message)); e != nil {
			return false, e
		}
		if message != nil {
			for _, child := range message.ToolMessage {
				if child == nil {
					continue
				}
				full, e := s.conversation.GetMessage(ctx, child.Id, apiconv.WithIncludeToolCall(true))
				if e != nil {
					return false, e
				}
				if e := closeElicitation(full); e != nil {
					return false, e
				}
			}
		}
	}
	for _, row := range indexTurnToolCalls(turn) {
		if row.status != "waiting_for_user" && row.status != "failed" {
			continue
		}
		message, e := s.conversation.GetMessage(ctx, row.messageID, apiconv.WithIncludeToolCall(true))
		if e != nil {
			return false, e
		}
		call := messageToolCall(message)
		if call == nil {
			return false, fmt.Errorf("cancellation pending tool identity missing")
		}
		if row.status == "failed" {
			if valueOrEmpty(call.ErrorMessage) == "Tool cancelled by user" && message.GetContent() == "Tool cancelled by user" {
				repair := apiconv.NewMessage()
				repair.SetId(message.Id)
				repair.SetConversationID(conversationID)
				repair.SetTurnID(turnID)
				repair.SetStatus("failed")
				if e = s.conversation.PatchMessage(ctx, repair); e != nil {
					return false, e
				}
			}
			continue
		}
		pending := clienttool.PendingCall{ID: row.opID, Name: call.ToolName, ToolMessageID: row.messageID, ConversationID: conversationID, TurnID: turnID}
		if e = toolexec.CompleteClientToolResult(ctx, s.conversation, pending, json.RawMessage(`"Tool cancelled by user"`), "Tool cancelled by user"); e != nil {
			return false, e
		}
	}
	if s.asyncManager != nil {
		s.asyncManager.CancelTurnPollers(ctx, conversationID, turnID)
	}
	visited := map[string]struct{}{conversationID: {}}
	for _, message := range turn.Message {
		if message == nil || message.LinkedConversationId == nil {
			continue
		}
		if childID := strings.TrimSpace(*message.LinkedConversationId); childID != "" {
			if err = s.terminateConversationTree(ctx, childID, visited); err != nil {
				return false, err
			}
		}
	}
	for _, message := range turn.Message {
		if message != nil && message.Role == "user" && message.ElicitationId == nil {
			starter := apiconv.NewMessage()
			starter.SetId(message.Id)
			starter.SetConversationID(conversationID)
			starter.SetTurnID(turnID)
			starter.SetStatus("cancel")
			if err = s.conversation.PatchMessage(ctx, starter); err != nil {
				return false, err
			}
			break
		}
	}
	update := apiconv.NewTurn()
	update.SetId(turnID)
	update.SetConversationID(conversationID)
	update.SetStatus("canceled")
	if err = s.conversation.PatchTurn(ctx, update); err != nil {
		return false, err
	}
	return turn.Status == "waiting_for_user", nil
}
