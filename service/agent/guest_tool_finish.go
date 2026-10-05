package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	turnmodel "github.com/viant/agently-core/model/turn"
)

// CompleteGuestToolCall closes only a persisted host-request turn. It does not
// advance model execution or cancel other work in the conversation.
func (s *Service) CompleteGuestToolCall(ctx context.Context, conversationID, turnID, status string, cause error) error {
	if s == nil || s.conversation == nil || (status != "succeeded" && status != "failed" && status != "canceled") {
		return fmt.Errorf("invalid guest completion")
	}
	patchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	parent, err := s.conversation.GetMessage(patchCtx, turnID)
	if err != nil {
		return err
	}
	if parent == nil || parent.ConversationId != conversationID || parent.Type != "host_request" {
		return fmt.Errorf("guest completion requires its original host-request identity")
	}
	turn := apiconv.NewTurn()
	turn.SetId(turnID)
	turn.SetConversationID(conversationID)
	turn.SetStatus(status)
	if cause != nil {
		turn.SetErrorMessage(cause.Error())
	}
	if err = s.conversation.PatchTurn(patchCtx, turn); err != nil {
		return err
	}
	if s.dataService == nil {
		return nil
	}
	active, err := s.hasActiveRun(patchCtx, conversationID)
	if err != nil || active {
		return err
	}
	other, err := s.dataService.GetActiveTurn(patchCtx, &turnmodel.ActiveTurnsInput{ConversationID: conversationID, Has: &turnmodel.ActiveTurnsInputHas{ConversationID: true}}, data.WithAdminPrincipal("guest-completion"))
	if err != nil || other != nil && strings.TrimSpace(other.Id) != "" {
		return err
	}
	queued, err := s.dataService.CountQueuedTurns(patchCtx, &turnmodel.QueuedTotalInput{ConversationID: conversationID, Has: &turnmodel.QueuedTotalInputHas{ConversationID: true}}, data.WithAdminPrincipal("guest-completion"))
	if err != nil || queued > 0 {
		return err
	}
	return s.patchConversationStatus(patchCtx, conversationID, status)
}
