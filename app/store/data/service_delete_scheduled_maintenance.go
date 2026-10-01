package data

import (
	"context"
	"errors"
	"fmt"
	lease "github.com/viant/agently-core/internal/store/maintenancelease"
	native "github.com/viant/agently-core/internal/store/scheduledmaintenance"
	"strings"
	"time"
)

// ScheduledRunMaintenanceSource identifies the persisted scheduler-run
// representation. MySQL installations can still contain legacy schedule_run
// rows; SQLite uses only run.
type ScheduledRunMaintenanceSource string

const (
	ScheduledRunMaintenanceCurrent ScheduledRunMaintenanceSource = "run"
	ScheduledRunMaintenanceLegacy  ScheduledRunMaintenanceSource = "schedule_run"
)

type ScheduledRunMaintenanceCandidateRequest struct {
	InactiveBefore time.Time
	AfterActivity  time.Time
	AfterRunID     string
	Limit          int
}

// ScheduledRunMaintenanceCandidate contains only bounded metadata used by the
// cleanup worker. It deliberately excludes prompts, messages and payloads.
type ScheduledRunMaintenanceCandidate struct {
	RunID string
	// ExpectedOwnerID is a diagnostic snapshot. System retention does not use
	// historical owner columns as an authorization boundary.
	ExpectedOwnerID string
	ActivityAt      time.Time
}

type ScheduledRunMaintenanceRequest struct {
	RunID string
	// ExpectedOwnerID is retained for caller compatibility and diagnostics.
	// It is intentionally not enforced by system scheduled-run retention.
	ExpectedOwnerID string
	InactiveBefore  time.Time
	Mode            ConversationMaintenanceMode
	Lease           MaintenanceLease
}

type ScheduledRunMaintenanceResult struct {
	RunID             string
	ScheduleID        string
	Source            ScheduledRunMaintenanceSource
	Mode              ConversationMaintenanceMode
	Eligible          bool
	Deleted           bool
	Reason            ConversationMaintenanceReason
	ConversationCount int
	LastActivity      time.Time
}

func (s *datlyService) ListScheduledRunMaintenanceCandidates(ctx context.Context, request ScheduledRunMaintenanceCandidateRequest) ([]ScheduledRunMaintenanceCandidate, error) {
	request.InactiveBefore = request.InactiveBefore.UTC()
	request.AfterActivity = request.AfterActivity.UTC()
	request.AfterRunID = strings.TrimSpace(request.AfterRunID)
	if err := validateScheduledRunMaintenanceCandidateRequest(request); err != nil {
		return nil, err
	}
	rows, err := (&native.Store{Invoker: s.native}).Candidates(ctx, native.CandidateRequest{InactiveBefore: request.InactiveBefore, AfterActivity: request.AfterActivity, AfterRunID: request.AfterRunID, Limit: request.Limit})
	if err != nil {
		return nil, err
	}
	result := make([]ScheduledRunMaintenanceCandidate, 0, len(rows))
	for _, row := range rows {
		result = append(result, ScheduledRunMaintenanceCandidate{RunID: row.RunID, ExpectedOwnerID: row.ExpectedOwnerID, ActivityAt: row.ActivityAt})
	}
	return result, nil
}

func validateScheduledRunMaintenanceCandidateRequest(request ScheduledRunMaintenanceCandidateRequest) error {
	switch {
	case request.InactiveBefore.IsZero():
		return fmt.Errorf("%w: scheduled-run candidate inactivity cutoff is required", ErrInvalidConversationMaintenanceRequest)
	case request.Limit <= 0:
		return fmt.Errorf("%w: scheduled-run candidate limit must be positive", ErrInvalidConversationMaintenanceRequest)
	case request.AfterActivity.IsZero() != (request.AfterRunID == ""):
		return fmt.Errorf("%w: scheduled-run candidate cursor requires both activity and run id", ErrInvalidConversationMaintenanceRequest)
	default:
		return nil
	}
}

// MaintainScheduledRun evaluates scheduler retention and, in delete mode,
// rechecks and deletes the run in the same fenced transaction.
func (s *datlyService) MaintainScheduledRun(ctx context.Context, request ScheduledRunMaintenanceRequest) (*ScheduledRunMaintenanceResult, error) {
	request.RunID = strings.TrimSpace(request.RunID)
	request.ExpectedOwnerID = strings.TrimSpace(request.ExpectedOwnerID)
	request.InactiveBefore = request.InactiveBefore.UTC()
	if request.Mode == "" {
		request.Mode = ConversationMaintenanceDryRun
	}
	if err := validateScheduledRunMaintenanceRequest(request); err != nil {
		return nil, err
	}
	output, err := (&native.Store{Invoker: s.native}).Maintain(ctx, &native.Input{RunID: request.RunID, InactiveBefore: request.InactiveBefore, Mode: string(request.Mode), Lease: lease.Lease{Key: request.Lease.Key, OwnerID: request.Lease.OwnerID, Token: request.Lease.Token, LeaseUntil: request.Lease.LeaseUntil}})
	if errors.Is(err, lease.ErrLeaseLost) {
		return nil, ErrMaintenanceLeaseLost
	}
	if err != nil {
		return nil, mapScheduleDeleteError(err)
	}
	return &ScheduledRunMaintenanceResult{RunID: output.RunID, ScheduleID: output.ScheduleID, Source: ScheduledRunMaintenanceSource(output.Source), Mode: ConversationMaintenanceMode(output.Mode), Eligible: output.Eligible, Deleted: output.Deleted, Reason: ConversationMaintenanceReason(output.Reason), ConversationCount: output.ConversationCount, LastActivity: output.LastActivity}, nil
}

func validateScheduledRunMaintenanceRequest(request ScheduledRunMaintenanceRequest) error {
	switch {
	case request.RunID == "" || request.InactiveBefore.IsZero():
		return fmt.Errorf("%w: scheduled-run id and inactivity cutoff are required", ErrInvalidConversationMaintenanceRequest)
	case request.Mode != ConversationMaintenanceDryRun && request.Mode != ConversationMaintenanceDelete:
		return fmt.Errorf("%w: unsupported scheduled-run maintenance mode %q", ErrInvalidConversationMaintenanceRequest, request.Mode)
	case request.Mode == ConversationMaintenanceDelete:
		if err := validateMaintenanceLease(normalizeMaintenanceLease(request.Lease)); err != nil {
			return fmt.Errorf("%w: delete mode requires maintenance lease: %v", ErrInvalidConversationMaintenanceRequest, err)
		}
	}
	return nil
}

func laterTime(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}
