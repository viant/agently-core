package conversation

import (
	"context"

	convcli "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	read "github.com/viant/agently-core/internal/datly/conversation/read"
	store "github.com/viant/agently-core/internal/store/conversation"
)

func (s *Service) getConversationsNative(ctx context.Context, input *convcli.Input) ([]*convcli.Conversation, error) {
	query := &read.ConversationInput{}
	if input != nil && input.Has != nil {
		if input.Has.AgentId {
			query.SetAgentId(input.AgentId)
		}
		if input.Has.ParentId {
			query.SetParentId(input.ParentId)
		}
		if input.Has.ParentTurnId {
			query.SetParentTurnId(input.ParentTurnId)
		}
		if input.Has.ExcludeChildren {
			query.SetExcludeChildren(input.ExcludeChildren)
		}
		if input.Has.ExcludeScheduled {
			query.SetExcludeScheduled(input.ExcludeScheduled)
		}
		if input.Has.ScheduleId {
			query.SetScheduleId(input.ScheduleId)
		}
		if input.Has.ScheduleRunId {
			query.SetScheduleRunId(input.ScheduleRunId)
		}
		if input.Has.Query {
			query.SetQuery(input.Query)
		}
		if input.Has.StatusFilter {
			query.SetStatusFilter(input.StatusFilter)
		}
		if input.Has.HasScheduleId {
			query.SetHasScheduleId(input.HasScheduleId)
		}
	}
	rows, err := (&store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}).ListVisible(ctx, query)
	if err != nil {
		return nil, err
	}
	result := make([]*convcli.Conversation, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		result = append(result, &convcli.Conversation{
			LastTurnId: row.LastTurnId, Stage: row.Stage, Id: row.Id,
			Summary: row.Summary, LastActivity: row.LastActivity,
			UsageInputTokens: row.UsageInputTokens, UsageOutputTokens: row.UsageOutputTokens,
			UsageEmbeddingTokens: row.UsageEmbeddingTokens, CreatedAt: row.CreatedAt,
			UpdatedAt: row.UpdatedAt, CreatedByUserId: row.CreatedByUserId,
			AgentId: row.AgentId, DefaultModelProvider: row.DefaultModelProvider,
			DefaultModel: row.DefaultModel, DefaultModelParams: row.DefaultModelParams,
			Title: row.Title, ConversationParentId: row.ConversationParentId,
			ConversationParentTurnId: row.ConversationParentTurnId, Metadata: row.Metadata,
			Visibility: row.Visibility, Shareable: row.Shareable, Status: row.Status,
			Scheduled: row.Scheduled, ScheduleId: row.ScheduleId,
			ScheduleRunId: row.ScheduleRunId, ScheduleKind: row.ScheduleKind,
			ScheduleTimezone: row.ScheduleTimezone, ScheduleCronExpr: row.ScheduleCronExpr,
			ExternalTaskRef: row.ExternalTaskRef,
		})
	}
	return result, nil
}
