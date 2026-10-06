// Package toolapprovalclaim conditionally reserves a personal approval decision
// through the generated Datly writer before any external tool effect occurs.
package toolapprovalclaim

import (
	"context"
	"fmt"
	"strings"
	"time"

	generated "github.com/viant/agently-core/internal/datly/toolapprovalqueue/claim"
	approval "github.com/viant/agently-core/model/toolapprovalqueue"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/handler"
)

func Claim(ctx context.Context, invoker dexec.ComponentInvoker, previous *approval.QueueRowView, principal, action string) (handler.Outcome, error) {
	var outcome handler.Outcome
	if invoker == nil || previous == nil || previous.Id == "" || principal == "" || previous.UserId != principal {
		return outcome, fmt.Errorf("approval claim requires authenticated queue ownership")
	}
	if !strings.EqualFold(previous.Status, "pending") {
		return outcome, fmt.Errorf("approval is no longer pending: %s", previous.Status)
	}
	var status string
	switch action {
	case "approve":
		status = "approved"
	case "reject":
		status = "rejected"
	case "cancel":
		status = "canceled"
	case "timeout":
		status = "timed_out"
	default:
		return outcome, fmt.Errorf("unsupported approval decision")
	}
	now := time.Now().UTC()
	row := &generated.QueueClaim{Id: previous.Id, UserId: principal, Status: status, Decision: &action, ApprovedByUserId: &principal, ApprovedAt: &now, UpdatedAt: &now, Has: &generated.QueueClaimHas{Id: true, UserId: true, Status: true, Decision: true, ApprovedByUserId: true, ApprovedAt: true, UpdatedAt: true}}
	if previous.Metadata != nil {
		text := string(*previous.Metadata)
		row.SetMetadata(&text)
	}
	input := &generated.Input{Principal: principal, ExpectedStatus: "pending", ExpectedUpdatedAt: previous.UpdatedAt, Queues: []*generated.QueueClaim{row}, Has: &generated.InputHas{Principal: true, ExpectedStatus: true, ExpectedUpdatedAt: previous.UpdatedAt != nil, Queues: true}}
	if action == "timeout" {
		if previous.ExpiresAt == nil {
			return outcome, fmt.Errorf("timeout requires a deadline")
		}
		input.DeadlineElapsedClock = &now
		input.Has.DeadlineElapsedClock = true
		row.SetTimedOutAt(&now)
	} else if previous.ExpiresAt != nil {
		input.DeadlineClock = &now
		input.Has.DeadlineClock = true
	}
	_, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: "github.com/viant/agently-core/internal/datly/toolapprovalqueue/claim", Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/toolapprovalqueue/claim"}}, Input: input, Completion: func(actual handler.Outcome) { outcome = actual.Clone() }})
	if err != nil {
		return outcome, err
	}
	if !outcome.CommitConfirmed() {
		return outcome, fmt.Errorf("approval claim did not commit; external execution remains blocked")
	}
	return outcome, nil
}
