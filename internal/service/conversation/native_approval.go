package conversation

import (
	"context"
	"fmt"

	authctx "github.com/viant/agently-core/internal/auth"
	cube "github.com/viant/agently-core/internal/datly/toolapprovalqueue/cube"
	read "github.com/viant/agently-core/internal/datly/toolapprovalqueue/read"
	write "github.com/viant/agently-core/internal/datly/toolapprovalqueue/write"
	store "github.com/viant/agently-core/internal/store/conversation"
	toolapprovalqueuemodel "github.com/viant/agently-core/model/toolapprovalqueue"

	"github.com/viant/xdatly/state"
)

func (s *Service) approvalStore() *store.ApprovalStore {
	return &store.ApprovalStore{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
}

func (s *Service) patchApprovalNative(ctx context.Context, queue *toolapprovalqueuemodel.ToolApprovalQueue) error {
	if queue == nil {
		return fmt.Errorf("approval mutation is required")
	}
	row := &write.ToolApprovalQueue{
		Id: queue.Id, UserId: queue.UserId, ConversationId: queue.ConversationId,
		TurnId: queue.TurnId, MessageId: queue.MessageId, ToolName: queue.ToolName,
		Title: queue.Title, Arguments: queue.Arguments, Metadata: queue.Metadata,
		Status: queue.Status, Decision: queue.Decision, ExpiresAt: queue.ExpiresAt,
		TimedOutAt: queue.TimedOutAt, ApprovedByUserId: queue.ApprovedByUserId,
		ApprovedAt: queue.ApprovedAt, ExecutedAt: queue.ExecutedAt,
		ErrorMessage: queue.ErrorMessage, CreatedAt: queue.CreatedAt, UpdatedAt: queue.UpdatedAt,
	}
	if queue.Has != nil {
		h := queue.Has
		row.Has = &write.ToolApprovalQueueHas{
			Id: h.Id, UserId: h.UserId, ConversationId: h.ConversationId,
			TurnId: h.TurnId, MessageId: h.MessageId, ToolName: h.ToolName,
			Title: h.Title, Arguments: h.Arguments, Metadata: h.Metadata,
			Status: h.Status, Decision: h.Decision, ExpiresAt: h.ExpiresAt,
			TimedOutAt: h.TimedOutAt, ApprovedByUserId: h.ApprovedByUserId,
			ApprovedAt: h.ApprovedAt, ExecutedAt: h.ExecutedAt,
			ErrorMessage: h.ErrorMessage, CreatedAt: h.CreatedAt, UpdatedAt: h.UpdatedAt,
		}
	}
	initial := nativePresence(row)
	output, err := s.approvalStore().PatchTrustedResult(ctx, row)
	if err != nil {
		return err
	}
	return applyNativeMutation(queue, output.Data, initial)
}

func approvalReadInput(in *toolapprovalqueuemodel.QueueRowsInput) *read.ApprovalRowsInput {
	query := &read.ApprovalRowsInput{}
	if in == nil {
		return query
	}
	query.Id, query.UserId, query.ConversationId = in.Id, in.UserId, in.ConversationId
	query.TurnId, query.MessageId, query.ToolName, query.QueueStatus = in.TurnId, in.MessageId, in.ToolName, in.QueueStatus
	if in.Has != nil {
		h := in.Has
		query.Has = &read.ApprovalRowsInputHas{
			Id: h.Id, UserId: h.UserId, ConversationId: h.ConversationId,
			TurnId: h.TurnId, MessageId: h.MessageId, ToolName: h.ToolName,
			QueueStatus: h.QueueStatus,
		}
	}
	return query
}

func approvalSelectors(selectors []*state.NamedSelector) state.Selectors {
	result := make(state.Selectors, 0, len(selectors))
	for _, item := range selectors {
		if item == nil {
			continue
		}
		result = append(result, item.Clone())
	}
	return result
}

func approvalBaseRow(row *read.ApprovalView) toolapprovalqueuemodel.QueueRowView {
	return toolapprovalqueuemodel.QueueRowView{
		Id: row.Id, UserId: row.UserId, ConversationId: row.ConversationId,
		TurnId: row.TurnId, MessageId: row.MessageId, ToolName: row.ToolName,
		Title: row.Title, Arguments: row.Arguments, Metadata: row.Metadata,
		Status: row.Status, Decision: row.Decision, ExpiresAt: row.ExpiresAt,
		TimedOutAt: row.TimedOutAt, ApprovedByUserId: row.ApprovedByUserId,
		ApprovedAt: row.ApprovedAt, ExecutedAt: row.ExecutedAt,
		ErrorMessage: row.ErrorMessage, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func (s *Service) listApprovalNative(ctx context.Context, in *toolapprovalqueuemodel.QueueRowsInput, selectors []*state.NamedSelector) ([]*toolapprovalqueuemodel.QueueRowView, error) {
	rows, err := s.approvalStore().List(ctx, "rows", approvalReadInput(in), approvalSelectors(selectors))
	if err != nil {
		return nil, err
	}
	result := make([]*toolapprovalqueuemodel.QueueRowView, 0, len(rows))
	for _, row := range rows {
		if row != nil {
			mapped := approvalBaseRow(row)
			result = append(result, &mapped)
		}
	}
	return result, nil
}

func (s *Service) countApprovalNative(ctx context.Context, in *toolapprovalqueuemodel.QueueTotalInput) (int, error) {
	query := &cube.ApprovalReportInput{}
	if in != nil {
		query.Id, query.UserId, query.ConversationId = in.Id, in.UserId, in.ConversationId
		query.TurnId, query.MessageId, query.ToolName, query.QueueStatus = in.TurnId, in.MessageId, in.ToolName, in.QueueStatus
		if in.Has != nil {
			h := in.Has
			query.Has = &cube.ApprovalReportInputHas{
				Id: h.Id, UserId: h.UserId, ConversationId: h.ConversationId,
				TurnId: h.TurnId, MessageId: h.MessageId, ToolName: h.ToolName,
				QueueStatus: h.QueueStatus,
			}
		}
	}
	return s.approvalStore().Count(ctx, query)
}

func (s *Service) listApprovalOutcomesNative(ctx context.Context, in *toolapprovalqueuemodel.OutcomeRowsInput) ([]*toolapprovalqueuemodel.OutcomeRowView, error) {
	query := &read.ApprovalRowsInput{}
	if in != nil {
		query.UserId, query.ConversationId, query.Since, query.Until = in.UserId, in.ConversationId, in.Since, in.Until
		if in.Has != nil {
			query.Has = &read.ApprovalRowsInputHas{
				UserId: in.Has.UserId, ConversationId: in.Has.ConversationId,
				Since: in.Has.Since, Until: in.Has.Until,
			}
		}
	}
	rows, err := s.approvalStore().List(ctx, "outcome", query, nil)
	if err != nil {
		return nil, err
	}
	result := make([]*toolapprovalqueuemodel.OutcomeRowView, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		result = append(result, &toolapprovalqueuemodel.OutcomeRowView{
			Id: row.Id, UserId: row.UserId, ConversationId: row.ConversationId,
			TurnId: row.TurnId, MessageId: row.MessageId, ToolName: row.ToolName,
			Title: row.Title, Arguments: row.Arguments, Metadata: row.Metadata,
			Status: row.Status, Decision: row.Decision, ExpiresAt: row.ExpiresAt,
			TimedOutAt: row.TimedOutAt, ApprovedByUserId: row.ApprovedByUserId,
			ApprovedAt: row.ApprovedAt, ExecutedAt: row.ExecutedAt,
			ErrorMessage: row.ErrorMessage, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			TransitionAt: row.TransitionAt,
		})
	}
	return result, nil
}
