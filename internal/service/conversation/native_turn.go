package conversation

import (
	"context"
	"fmt"
	"strings"

	convcli "github.com/viant/agently-core/app/store/conversation"
	write "github.com/viant/agently-core/internal/datly/turn/write"
	store "github.com/viant/agently-core/internal/store/conversation"
)

func (s *Service) patchTurnNative(ctx context.Context, turn *convcli.MutableTurn) error {
	if turn == nil {
		return fmt.Errorf("turn mutation is required")
	}
	row := &write.Turn{}
	row.SetId(strings.TrimSpace(turn.Id))
	if h := turn.Has; h != nil {
		if h.ConversationID {
			row.SetConversationId(turn.ConversationID)
		}
		if h.CreatedAt {
			row.SetCreatedAt(turn.CreatedAt)
		}
		if h.QueueSeq {
			row.SetQueueSeq(turn.QueueSeq)
		}
		if h.Origin {
			row.SetOrigin(turn.Origin)
		}
		if h.GoalID {
			row.SetGoalId(turn.GoalID)
		}
		if h.StatusReason {
			row.SetStatusReason(turn.StatusReason)
		}
		if h.Status {
			row.SetStatus(turn.Status)
		}
		if h.StartedByMessageID {
			row.SetStartedByMessageId(turn.StartedByMessageID)
		}
		if h.RetryOf {
			row.SetRetryOf(turn.RetryOf)
		}
		if h.AgentIDUsed {
			row.SetAgentIdUsed(turn.AgentIDUsed)
		}
		if h.AgentConfigUsedID {
			row.SetAgentConfigUsedId(turn.AgentConfigUsedID)
		}
		if h.ModelOverrideProvider {
			row.SetModelOverrideProvider(turn.ModelOverrideProvider)
		}
		if h.ModelOverride {
			row.SetModelOverride(turn.ModelOverride)
		}
		if h.ModelParamsOverride {
			row.SetModelParamsOverride(turn.ModelParamsOverride)
		}
		if h.RunID {
			row.SetRunId(turn.RunID)
		}
		if h.ErrorMessage {
			row.SetErrorMessage(turn.ErrorMessage)
		}
	}
	initial := nativePresence(row)
	output, err := (&store.TurnStore{Invoker: s.native}).PatchTrustedResult(ctx, row)
	if err != nil {
		return err
	}
	return applyNativeMutation(turn, output.Data, initial)
}
