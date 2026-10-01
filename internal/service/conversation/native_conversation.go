package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	convcli "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	read "github.com/viant/agently-core/internal/datly/conversation/read"
	write "github.com/viant/agently-core/internal/datly/conversation/write"
	store "github.com/viant/agently-core/internal/store/conversation"
)

func (s *Service) patchConversationNative(ctx context.Context, mutation *convcli.MutableConversation) error {
	if mutation == nil {
		return fmt.Errorf("conversation mutation is required")
	}
	row := &write.MutableConversationView{}
	row.SetId(strings.TrimSpace(mutation.Id))
	if h := mutation.Has; h != nil {
		if h.Summary {
			row.SetSummary(mutation.Summary)
		}
		if h.LastActivity {
			row.SetLastActivity(mutation.LastActivity)
		}
		if h.UsageInputTokens {
			row.SetUsageInputTokens(mutation.UsageInputTokens)
		}
		if h.UsageOutputTokens {
			row.SetUsageOutputTokens(mutation.UsageOutputTokens)
		}
		if h.UsageEmbeddingTokens {
			row.SetUsageEmbeddingTokens(mutation.UsageEmbeddingTokens)
		}
		if h.CreatedAt {
			row.SetCreatedAt(mutation.CreatedAt)
		}
		if h.UpdatedAt {
			row.SetUpdatedAt(mutation.UpdatedAt)
		}
		if h.CreatedByUserID {
			row.SetCreatedByUserId(mutation.CreatedByUserID)
		}
		if h.AgentId {
			row.SetAgentId(mutation.AgentId)
		}
		if h.DefaultModelProvider {
			row.SetDefaultModelProvider(mutation.DefaultModelProvider)
		}
		if h.DefaultModel {
			row.SetDefaultModel(mutation.DefaultModel)
		}
		if h.DefaultModelParams {
			row.SetDefaultModelParams(mutation.DefaultModelParams)
		}
		if h.Title {
			row.SetTitle(mutation.Title)
		}
		if h.ConversationParentId {
			row.SetConversationParentId(mutation.ConversationParentId)
		}
		if h.ConversationParentTurnId {
			row.SetConversationParentTurnId(mutation.ConversationParentTurnId)
		}
		if h.Metadata {
			row.SetMetadata(mutation.Metadata)
		}
		if h.Visibility {
			row.SetVisibility(mutation.Visibility)
		}
		if h.Shareable {
			row.SetShareable(mutation.Shareable)
		}
		if h.Status {
			row.SetStatus(mutation.Status)
		}
		if h.Scheduled {
			row.SetScheduled(mutation.Scheduled)
		}
		if h.ScheduleId {
			row.SetScheduleId(mutation.ScheduleId)
		}
		if h.ScheduleRunId {
			row.SetScheduleRunId(mutation.ScheduleRunId)
		}
		if h.ScheduleKind {
			row.SetScheduleKind(mutation.ScheduleKind)
		}
		if h.ScheduleTimezone {
			row.SetScheduleTimezone(mutation.ScheduleTimezone)
		}
		if h.ScheduleCronExpr {
			row.SetScheduleCronExpr(mutation.ScheduleCronExpr)
		}
		if h.ExternalTaskRef {
			row.SetExternalTaskRef(mutation.ExternalTaskRef)
		}
	}
	initial := nativePresence(row)
	output, err := (&store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}).PatchTrustedResult(ctx, row)
	if err != nil {
		return err
	}
	return applyNativeMutation(mutation, output.Data, initial)
}

func (s *Service) getConversationNative(ctx context.Context, id string, options ...convcli.Option) (*convcli.Conversation, error) {
	input := &convcli.Input{}
	for _, option := range options {
		if option != nil {
			option(input)
		}
	}
	query := nativeConversationQuery(input)
	component := &store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
	row, err := component.GetInternal(ctx, id, query)
	if errors.Is(err, store.ErrNotFound) && (input.IncludeTranscript || input.IncludeModelCal || input.IncludeToolCall) {
		row, err = component.GetInternal(ctx, id, nil)
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return nil, fmt.Errorf("encode native conversation: %w", err)
	}
	var result convcli.Conversation
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, fmt.Errorf("decode conversation contract: %w", err)
	}
	pruneBlankAssistantPlaceholders(result.Transcript)
	return &result, nil
}

func nativeConversationQuery(input *convcli.Input) *read.ConversationInput {
	query := &read.ConversationInput{}
	if input == nil || input.Has == nil {
		return query
	}
	h := input.Has
	if h.Since {
		query.SetSince(input.Since)
	}
	if h.IncludeTranscript {
		query.SetIncludeTranscript(input.IncludeTranscript)
	}
	if h.IncludeModelCal {
		query.SetIncludeModelCal(input.IncludeModelCal)
	}
	if h.IncludeToolCall {
		query.SetIncludeToolCall(input.IncludeToolCall)
	}
	if h.AgentId {
		query.SetAgentId(input.AgentId)
	}
	if h.ParentId {
		query.SetParentId(input.ParentId)
	}
	if h.ParentTurnId {
		query.SetParentTurnId(input.ParentTurnId)
	}
	if h.ExcludeChildren {
		query.SetExcludeChildren(input.ExcludeChildren)
	}
	if h.ExcludeScheduled {
		query.SetExcludeScheduled(input.ExcludeScheduled)
	}
	if h.ScheduleId {
		query.SetScheduleId(input.ScheduleId)
	}
	if h.ScheduleRunId {
		query.SetScheduleRunId(input.ScheduleRunId)
	}
	if h.Query {
		query.SetQuery(input.Query)
	}
	if h.StatusFilter {
		query.SetStatusFilter(input.StatusFilter)
	}
	if h.HasScheduleId {
		query.SetHasScheduleId(input.HasScheduleId)
	}
	return query
}
