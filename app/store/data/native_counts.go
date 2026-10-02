package data

import (
	"context"
	"fmt"
	"strings"

	msgcube "github.com/viant/agently-core/internal/datly/message/cube"
	approvalcube "github.com/viant/agently-core/internal/datly/toolapprovalqueue/cube"
	turncube "github.com/viant/agently-core/internal/datly/turn/cube"
)

func (s *datlyService) authorizeCountScope(ctx context.Context, id string, opts *options) error {
	if opts != nil && opts.principal != "" && !opts.isAdmin && strings.TrimSpace(id) == "" {
		return ErrPermissionDenied
	}
	return s.authorizeConversationID(ctx, id, opts, nil)
}
func (s *datlyService) countTurnNative(ctx context.Context, id, field string, filter bool, opts *options) (int, error) {
	if err := s.authorizeCountScope(ctx, id, opts); err != nil {
		return 0, err
	}
	input := &turncube.TurnReportInput{}
	if filter {
		input.SetConversationId(id)
	}
	column := "queued_count"
	if field == "ControllerCount" {
		column = "controller_count"
	}
	input.SetFields([]string{column})
	value, err := s.readDataNative(ctx, input, "/v1/internal/agently/turn/report", "turnaccess", "", opts)
	if err != nil {
		return 0, err
	}
	out, ok := value.(*turncube.TurnReportOutput)
	if !ok {
		return 0, fmt.Errorf("turn cube returned %T", value)
	}
	result := 0
	for _, row := range out.Data {
		if row != nil {
			if field == "QueuedCount" {
				result += row.QueuedCount
			} else {
				result += row.ControllerCount
			}
		}
	}
	return result, nil
}
func (s *datlyService) countPendingNative(ctx context.Context, id string, elicitation bool, opts *options) (int, error) {
	if err := s.authorizeCountScope(ctx, id, opts); err != nil {
		return 0, err
	}
	if elicitation {
		input := &msgcube.MessageReportInput{}
		input.SetConversationId(id)
		input.SetFields([]string{"pending_count"})
		value, err := s.readDataNative(ctx, input, "/v1/internal/agently/message/report", "messageaccess", "", opts)
		if err != nil {
			return 0, err
		}
		out, ok := value.(*msgcube.MessageReportOutput)
		if !ok {
			return 0, fmt.Errorf("message cube returned %T", value)
		}
		total := 0
		for _, row := range out.Data {
			if row != nil {
				total += row.PendingCount
			}
		}
		return total, nil
	}
	input := &approvalcube.ApprovalReportInput{}
	input.SetConversationId(id)
	input.SetFields([]string{"pending_count"})
	value, err := s.readDataNative(ctx, input, "/v1/internal/agently/tool-approval/report", "approvalaccess", "", opts)
	if err != nil {
		return 0, err
	}
	out, ok := value.(*approvalcube.ApprovalReportOutput)
	if !ok {
		return 0, fmt.Errorf("approval cube returned %T", value)
	}
	total := 0
	for _, row := range out.Data {
		if row != nil {
			total += row.PendingCount
		}
	}
	return total, nil
}
