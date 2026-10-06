package conversation

import (
	"context"
	"fmt"

	conv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/toolapprovalclaim"
	model "github.com/viant/agently-core/model/toolapprovalqueue"
	dexec "github.com/viant/datly/exec"
)

func (s *Service) ClaimToolApprovalDecision(ctx context.Context, row *model.QueueRowView, principal, action string) error {
	if s == nil || s.native == nil {
		return fmt.Errorf("atomic approval claim is unavailable")
	}
	_, err := toolapprovalclaim.Claim(ctx, s.native, row, principal, action)
	return err
}
func (s *Service) NativeComponentInvoker() dexec.ComponentInvoker {
	if s == nil {
		return nil
	}
	return s.native
}

// BindComponentInvoker keeps dependent message/payload writers on the root's
// transaction. This quiet scoped clone cannot publish before its owner commits.
func (s *Service) BindComponentInvoker(invoker dexec.ComponentInvoker) conv.Client {
	return &Service{native: invoker}
}
