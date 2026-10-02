package data

import (
	"context"
	"fmt"
	native "github.com/viant/agently-core/internal/store/conversationmaintenance"
	"strings"
	"time"
)

// ConversationMaintenanceCandidateRequest defines a bounded, keyset-paged
// maintenance scan. It is an internal maintenance contract and does not use a
// request principal or user impersonation.
type ConversationMaintenanceCandidateRequest struct {
	Kind           ConversationMaintenanceKind
	InactiveBefore time.Time
	AfterActivity  time.Time
	AfterRootID    string
	Limit          int
}

// ConversationMaintenanceCandidate contains only metadata needed to evaluate
// a graph. In particular, the scan never reads conversation text or payloads.
type ConversationMaintenanceCandidate struct {
	RootID          string
	ExpectedOwnerID string
	ActivityAt      time.Time
}

func (s *datlyService) ListConversationMaintenanceCandidates(ctx context.Context, request ConversationMaintenanceCandidateRequest) ([]ConversationMaintenanceCandidate, error) {
	request.InactiveBefore = request.InactiveBefore.UTC()
	request.AfterActivity = request.AfterActivity.UTC()
	request.AfterRootID = strings.TrimSpace(request.AfterRootID)
	if err := validateConversationMaintenanceCandidateRequest(request); err != nil {
		return nil, err
	}
	rows, err := (&native.Store{Invoker: s.native}).Candidates(ctx, native.CandidateRequest{Kind: string(request.Kind), InactiveBefore: request.InactiveBefore, AfterActivity: request.AfterActivity, AfterRootID: request.AfterRootID, Limit: request.Limit})
	if err != nil {
		return nil, err
	}
	result := make([]ConversationMaintenanceCandidate, 0, len(rows))
	for _, row := range rows {
		result = append(result, ConversationMaintenanceCandidate{RootID: row.RootID, ExpectedOwnerID: row.ExpectedOwnerID, ActivityAt: row.ActivityAt})
	}
	return result, nil
}

func validateConversationMaintenanceCandidateRequest(request ConversationMaintenanceCandidateRequest) error {
	switch {
	case request.Kind != ConversationMaintenanceInteractive && request.Kind != ConversationMaintenanceScheduled && request.Kind != ConversationMaintenanceScheduledFallback:
		return fmt.Errorf("%w: unsupported candidate kind %q", ErrInvalidConversationMaintenanceRequest, request.Kind)
	case request.InactiveBefore.IsZero():
		return fmt.Errorf("%w: candidate inactivity cutoff is required", ErrInvalidConversationMaintenanceRequest)
	case request.Limit <= 0:
		return fmt.Errorf("%w: candidate limit must be positive", ErrInvalidConversationMaintenanceRequest)
	case request.AfterActivity.IsZero() != (request.AfterRootID == ""):
		return fmt.Errorf("%w: candidate cursor requires both activity and root id", ErrInvalidConversationMaintenanceRequest)
	default:
		return nil
	}
}
