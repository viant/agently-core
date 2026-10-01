package conversation

import (
	"context"
	convbase "github.com/viant/agently-core/internal/datly/conversation/base"
	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	msgbase "github.com/viant/agently-core/internal/datly/message/base"
	msgread "github.com/viant/agently-core/internal/datly/message/read"
)

func conversationFromBase(ctx context.Context, row *convbase.ConversationBaseView) *convread.ConversationView {
	if row == nil {
		return nil
	}
	result := &convread.ConversationView{
		CreatedAtRaw:             row.CreatedAtRaw,
		ActivityRaw:              row.ActivityRaw,
		ListMode:                 row.ListMode,
		LastTurnId:               row.LastTurnId,
		Status:                   row.Status,
		Stage:                    row.Stage,
		Id:                       row.Id,
		CreatedAt:                row.CreatedAt,
		Visibility:               row.Visibility,
		Shareable:                row.Shareable,
		Summary:                  row.Summary,
		LastActivity:             row.LastActivity,
		UsageInputTokens:         row.UsageInputTokens,
		UsageOutputTokens:        row.UsageOutputTokens,
		UsageEmbeddingTokens:     row.UsageEmbeddingTokens,
		UpdatedAt:                row.UpdatedAt,
		CreatedByUserId:          row.CreatedByUserId,
		AgentId:                  row.AgentId,
		DefaultModelProvider:     row.DefaultModelProvider,
		DefaultModel:             row.DefaultModel,
		DefaultModelParams:       row.DefaultModelParams,
		Title:                    row.Title,
		ConversationParentId:     row.ConversationParentId,
		ConversationParentTurnId: row.ConversationParentTurnId,
		Metadata:                 row.Metadata,
		Scheduled:                row.Scheduled,
		ScheduleId:               row.ScheduleId,
		ScheduleRunId:            row.ScheduleRunId,
		ScheduleKind:             row.ScheduleKind,
		ScheduleTimezone:         row.ScheduleTimezone,
		ScheduleCronExpr:         row.ScheduleCronExpr,
		ExternalTaskRef:          row.ExternalTaskRef,
	}
	result.OnRelation(ctx)
	return result
}

func messageFromBase(row *msgbase.MessageBaseView) *msgread.MessageView {
	if row == nil {
		return nil
	}
	return &msgread.MessageView{
		ConversationId:       row.ConversationId,
		CreatedAt:            row.CreatedAt,
		Id:                   row.Id,
		Interim:              row.Interim,
		Role:                 row.Role,
		Type:                 row.Type,
		Narration:            row.Narration,
		TurnId:               row.TurnId,
		Archived:             row.Archived,
		Sequence:             row.Sequence,
		UpdatedAt:            row.UpdatedAt,
		CreatedByUserId:      row.CreatedByUserId,
		Status:               row.Status,
		Mode:                 row.Mode,
		Content:              row.Content,
		RawContent:           row.RawContent,
		Summary:              row.Summary,
		ContextSummary:       row.ContextSummary,
		Tags:                 row.Tags,
		ElicitationId:        row.ElicitationId,
		ParentMessageId:      row.ParentMessageId,
		SupersededBy:         row.SupersededBy,
		LinkedConversationId: row.LinkedConversationId,
		AttachmentPayloadId:  row.AttachmentPayloadId,
		ElicitationPayloadId: row.ElicitationPayloadId,
		ToolName:             row.ToolName,
		EmbeddingIndex:       row.EmbeddingIndex,
		Iteration:            row.Iteration,
		Phase:                row.Phase,
	}
}
