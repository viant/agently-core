package data

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	native "github.com/viant/agently-core/internal/store/conversationmaintenance"
	lease "github.com/viant/agently-core/internal/store/maintenancelease"
)

// ConversationMaintenanceKind separates independently configurable cleanup
// policies. The operation verifies the kind again inside its transaction.
type ConversationMaintenanceKind string

const (
	ConversationMaintenanceInteractive       ConversationMaintenanceKind = "interactive"
	ConversationMaintenanceScheduled         ConversationMaintenanceKind = "scheduled"
	ConversationMaintenanceScheduledFallback ConversationMaintenanceKind = "scheduled_fallback"
)

// ConversationMaintenanceMode is explicit so a caller cannot accidentally
// turn a dry run into a deletion by omitting a boolean option.
type ConversationMaintenanceMode string

const (
	ConversationMaintenanceDryRun ConversationMaintenanceMode = "dry_run"
	ConversationMaintenanceDelete ConversationMaintenanceMode = "delete"
)

// ConversationMaintenanceReason is a stable classification for candidates
// that are intentionally skipped rather than failed by an infrastructure
// error.
type ConversationMaintenanceReason string

const (
	ConversationMaintenanceEligible             ConversationMaintenanceReason = "eligible"
	ConversationMaintenanceDeleted              ConversationMaintenanceReason = "deleted"
	ConversationMaintenanceNotFound             ConversationMaintenanceReason = "not_found"
	ConversationMaintenanceNotRoot              ConversationMaintenanceReason = "not_root"
	ConversationMaintenanceOwnerMissing         ConversationMaintenanceReason = "owner_missing"
	ConversationMaintenanceOwnerMismatch        ConversationMaintenanceReason = "owner_mismatch"
	ConversationMaintenanceRelatedOwnerMismatch ConversationMaintenanceReason = "related_owner_mismatch"
	ConversationMaintenanceKindMismatch         ConversationMaintenanceReason = "kind_mismatch"
	ConversationMaintenanceRecentActivity       ConversationMaintenanceReason = "recent_activity"
	ConversationMaintenanceActivityUnknown      ConversationMaintenanceReason = "activity_unknown"
	ConversationMaintenanceRunPresent           ConversationMaintenanceReason = "run_present"
	ConversationMaintenanceLiveRun              ConversationMaintenanceReason = "live_run"
	ConversationMaintenanceLiveSchedule         ConversationMaintenanceReason = "live_schedule"
	ConversationMaintenanceActiveReportExport   ConversationMaintenanceReason = "active_report_export"
	ConversationMaintenanceGraphReferenced      ConversationMaintenanceReason = "graph_referenced"
	ConversationMaintenanceScheduleReferenced   ConversationMaintenanceReason = "schedule_referenced"
	ConversationMaintenanceScheduleMissing      ConversationMaintenanceReason = "schedule_missing"
	ConversationMaintenanceInternalSchedule     ConversationMaintenanceReason = "internal_schedule"
	ConversationMaintenanceGraphTooLarge        ConversationMaintenanceReason = "graph_too_large"
)

// ConversationMaintenanceRequest describes one root-graph evaluation.
// ExpectedOwnerID is retained as candidate diagnostics; system maintenance
// does not use historical owner metadata as an authorization boundary.
type ConversationMaintenanceRequest struct {
	RootID          string
	ExpectedOwnerID string
	Kind            ConversationMaintenanceKind
	InactiveBefore  time.Time
	Mode            ConversationMaintenanceMode
	Lease           MaintenanceLease
}

type ConversationMaintenanceResult struct {
	RootID            string
	Kind              ConversationMaintenanceKind
	Mode              ConversationMaintenanceMode
	Eligible          bool
	Deleted           bool
	Reason            ConversationMaintenanceReason
	ConversationCount int
	LastActivity      time.Time
}

func (s *datlyService) MaintainConversationTree(ctx context.Context, request ConversationMaintenanceRequest) (*ConversationMaintenanceResult, error) {
	request.RootID = strings.TrimSpace(request.RootID)
	request.ExpectedOwnerID = strings.TrimSpace(request.ExpectedOwnerID)
	request.InactiveBefore = request.InactiveBefore.UTC()
	if err := validateConversationMaintenanceRequest(request); err != nil {
		return nil, err
	}
	output, err := (&native.Store{Invoker: s.native}).Maintain(ctx, &native.Input{RootID: request.RootID, Kind: string(request.Kind), InactiveBefore: request.InactiveBefore, Mode: string(request.Mode), Lease: lease.Lease{Key: request.Lease.Key, OwnerID: request.Lease.OwnerID, Token: request.Lease.Token, LeaseUntil: request.Lease.LeaseUntil}})
	if errors.Is(err, lease.ErrLeaseLost) {
		return nil, ErrMaintenanceLeaseLost
	}
	if err != nil {
		return nil, mapScheduleDeleteError(err)
	}
	return &ConversationMaintenanceResult{RootID: output.RootID, Kind: ConversationMaintenanceKind(output.Kind), Mode: ConversationMaintenanceMode(output.Mode), Eligible: output.Eligible, Deleted: output.Deleted, Reason: ConversationMaintenanceReason(output.Reason), ConversationCount: output.ConversationCount, LastActivity: output.LastActivity}, nil
}

func validateConversationMaintenanceRequest(request ConversationMaintenanceRequest) error {
	switch {
	case request.RootID == "":
		return fmt.Errorf("%w: root id is required", ErrInvalidConversationMaintenanceRequest)
	case request.InactiveBefore.IsZero():
		return fmt.Errorf("%w: inactivity cutoff is required", ErrInvalidConversationMaintenanceRequest)
	case request.Kind != ConversationMaintenanceInteractive && request.Kind != ConversationMaintenanceScheduled && request.Kind != ConversationMaintenanceScheduledFallback:
		return fmt.Errorf("%w: unsupported kind %q", ErrInvalidConversationMaintenanceRequest, request.Kind)
	case request.Mode != ConversationMaintenanceDryRun && request.Mode != ConversationMaintenanceDelete:
		return fmt.Errorf("%w: unsupported mode %q", ErrInvalidConversationMaintenanceRequest, request.Mode)
	case request.Mode == ConversationMaintenanceDelete:
		if err := validateMaintenanceLease(normalizeMaintenanceLease(request.Lease)); err != nil {
			return fmt.Errorf("%w: delete mode requires maintenance lease: %v", ErrInvalidConversationMaintenanceRequest, err)
		}
	default:
		return nil
	}
	return nil
}
