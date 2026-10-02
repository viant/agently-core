package data

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/sqlitewrite"
	maintenance "github.com/viant/agently-core/internal/store/maintenancelease"
	"github.com/viant/agently-core/internal/store/orphanmaintenance"
)

type OrphanMaintenanceReason string

const (
	OrphanMaintenanceDeletedReason          OrphanMaintenanceReason = "deleted"
	OrphanMaintenanceDetachedReason         OrphanMaintenanceReason = "detached"
	OrphanMaintenanceReportOnlyReason       OrphanMaintenanceReason = "report_only"
	OrphanMaintenanceNoLongerEligibleReason OrphanMaintenanceReason = "no_longer_eligible"
)

// OrphanMaintenanceRequest identifies one previously reported orphan. The
// rule, age and missing-parent condition are all re-evaluated in a serializable
// transaction before any mutation is allowed.
type OrphanMaintenanceRequest struct {
	RuleID    string
	RecordID  string
	OlderThan time.Time
	Lease     MaintenanceLease
}

type OrphanMaintenanceResult struct {
	RuleID   string
	RecordID string
	Action   OrphanMaintenanceAction
	Eligible bool
	Mutated  bool
	Deleted  bool
	Detached bool
	Reason   OrphanMaintenanceReason
}

func (s *datlyService) MaintainOrphanCandidate(ctx context.Context, request OrphanMaintenanceRequest) (*OrphanMaintenanceResult, error) {
	result, err := sqlitewrite.Do(ctx, s.writeGate, func() (*orphanmaintenance.Result, error) {
		return (&orphanmaintenance.Store{Invoker: s.native}).Apply(ctx, orphanmaintenance.Request{RuleID: request.RuleID, RecordID: request.RecordID, OlderThan: request.OlderThan, Lease: maintenance.Lease{Key: request.Lease.Key, OwnerID: request.Lease.OwnerID, Token: request.Lease.Token, LeaseUntil: request.Lease.LeaseUntil}})
	})
	if err != nil {
		return nil, mapOrphanMaintenanceError(err)
	}
	return &OrphanMaintenanceResult{RuleID: result.RuleID, RecordID: result.RecordID, Action: OrphanMaintenanceAction(result.Action), Eligible: result.Eligible, Mutated: result.Mutated, Deleted: result.Deleted, Detached: result.Detached, Reason: OrphanMaintenanceReason(result.Reason)}, nil
}
func mapOrphanMaintenanceError(err error) error {
	if errors.Is(err, orphanmaintenance.ErrInvalidRequest) {
		return fmt.Errorf("%w%s", ErrInvalidConversationMaintenanceRequest, strings.TrimPrefix(err.Error(), orphanmaintenance.ErrInvalidRequest.Error()))
	}
	if errors.Is(err, maintenance.ErrLeaseLost) {
		return ErrMaintenanceLeaseLost
	}
	return err
}
