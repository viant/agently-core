package data

import (
	"context"
	"encoding/json"
	"fmt"

	write "github.com/viant/agently-core/internal/datly/run/write"
	store "github.com/viant/agently-core/internal/store/agentrun"

	datlypredicate "github.com/viant/agently-core/internal/datly/predicate"
	legacy "github.com/viant/agently-core/pkg/agently/run/write"
)

func nativeRunMutation(row *legacy.MutableRunView) (*write.MutableRunView, error) {
	if row == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return nil, fmt.Errorf("encode run mutation: %w", err)
	}
	var mapped write.MutableRunView
	if err := json.Unmarshal(encoded, &mapped); err != nil {
		return nil, fmt.Errorf("decode native run mutation: %w", err)
	}
	if row.Condition != nil {
		mapped.Condition = &datlypredicate.RunPatchCondition{
			Status: row.Condition.Status, LeaseOwner: row.Condition.LeaseOwner, Attempt: row.Condition.Attempt,
		}
	}
	if h := row.Has; h != nil {
		mapped.Has = &write.MutableRunViewHas{
			Id: h.Id, TurnId: h.TurnID, ScheduleId: h.ScheduleID,
			ConversationId: h.ConversationID, ConversationKind: h.ConversationKind,
			Attempt: h.Attempt, ResumedFromRunId: h.ResumedFromRunID,
			Status: h.Status, ErrorCode: h.ErrorCode, ErrorMessage: h.ErrorMessage,
			Iteration: h.Iteration, MaxIterations: h.MaxIterations,
			CheckpointResponseId: h.CheckpointResponseID, CheckpointMessageId: h.CheckpointMessageID,
			CheckpointData: h.CheckpointData, AgentId: h.AgentID,
			ModelProvider: h.ModelProvider, Model: h.Model,
			WorkerId: h.WorkerID, WorkerPid: h.WorkerPID, WorkerHost: h.WorkerHost,
			LeaseOwner: h.LeaseOwner, LeaseUntil: h.LeaseUntil, LastHeartbeatAt: h.LastHeartbeatAt,
			SecurityContext: h.SecurityContext, UserCredUrl: h.UserCredURL,
			EffectiveUserId: h.EffectiveUserID, HeartbeatIntervalSec: h.HeartbeatIntervalSec,
			ScheduledFor: h.ScheduledFor, PreconditionRanAt: h.PreconditionRanAt,
			PreconditionPassed: h.PreconditionPassed, PreconditionResult: h.PreconditionResult,
			UsagePromptTokens: h.UsagePromptTokens, UsageCompletionTokens: h.UsageCompletionTokens,
			UsageTotalTokens: h.UsageTotalTokens, UsageCost: h.UsageCost,
			CreatedAt: h.CreatedAt, UpdatedAt: h.UpdatedAt,
			StartedAt: h.StartedAt, CompletedAt: h.CompletedAt,
		}
	}
	return &mapped, nil
}

func legacyRunMutation(row *write.MutableRunView) (*legacy.MutableRunView, error) {
	if row == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return nil, fmt.Errorf("encode native run result: %w", err)
	}
	var mapped legacy.MutableRunView
	if err := json.Unmarshal(encoded, &mapped); err != nil {
		return nil, fmt.Errorf("decode run result contract: %w", err)
	}
	if row.Condition != nil {
		mapped.Condition = &legacy.RunPatchCondition{
			Status: row.Condition.Status, LeaseOwner: row.Condition.LeaseOwner, Attempt: row.Condition.Attempt,
		}
	}
	if h := row.Has; h != nil {
		mapped.Has = &legacy.RunHas{
			Id: h.Id, TurnID: h.TurnId, ScheduleID: h.ScheduleId,
			ConversationID: h.ConversationId, ConversationKind: h.ConversationKind,
			Attempt: h.Attempt, ResumedFromRunID: h.ResumedFromRunId,
			Status: h.Status, ErrorCode: h.ErrorCode, ErrorMessage: h.ErrorMessage,
			Iteration: h.Iteration, MaxIterations: h.MaxIterations,
			CheckpointResponseID: h.CheckpointResponseId, CheckpointMessageID: h.CheckpointMessageId,
			CheckpointData: h.CheckpointData, AgentID: h.AgentId,
			ModelProvider: h.ModelProvider, Model: h.Model,
			WorkerID: h.WorkerId, WorkerPID: h.WorkerPid, WorkerHost: h.WorkerHost,
			LeaseOwner: h.LeaseOwner, LeaseUntil: h.LeaseUntil, LastHeartbeatAt: h.LastHeartbeatAt,
			HeartbeatIntervalSec: h.HeartbeatIntervalSec, SecurityContext: h.SecurityContext,
			UserCredURL: h.UserCredUrl, EffectiveUserID: h.EffectiveUserId,
			ScheduledFor: h.ScheduledFor, PreconditionRanAt: h.PreconditionRanAt,
			PreconditionPassed: h.PreconditionPassed, PreconditionResult: h.PreconditionResult,
			UsagePromptTokens: h.UsagePromptTokens, UsageCompletionTokens: h.UsageCompletionTokens,
			UsageTotalTokens: h.UsageTotalTokens, UsageCost: h.UsageCost,
			CreatedAt: h.CreatedAt, UpdatedAt: h.UpdatedAt,
			StartedAt: h.StartedAt, CompletedAt: h.CompletedAt,
		}
	}
	return &mapped, nil
}

func (s *datlyService) patchRunsNative(ctx context.Context, rows []*legacy.MutableRunView) ([]*legacy.MutableRunView, error) {
	mutations := make([]*write.MutableRunView, 0, len(rows))
	for _, row := range rows {
		mapped, err := nativeRunMutation(row)
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, mapped)
	}
	output, err := (&store.Store{Invoker: s.native}).PatchTrusted(ctx, mutations)
	if err != nil {
		return nil, err
	}
	result := make([]*legacy.MutableRunView, 0, len(output))
	for _, row := range output {
		mapped, err := legacyRunMutation(row)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
	}
	return result, nil
}
