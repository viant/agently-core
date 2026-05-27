package approvalqueue

import (
	"context"
	"errors"
	"strings"
	"time"

	queueread "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	queuew "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/write"
	"github.com/viant/agently-core/sdk/api"
)

// TimeoutLister enumerates approval-queue rows. It mirrors the canonical
// queue-read surface so timeout production stays attached to approvalqueue
// semantics instead of depending on higher-level SDK wiring.
type TimeoutLister interface {
	ListToolApprovalQueues(ctx context.Context, in *queueread.QueueRowsInput) ([]*queueread.QueueRowView, error)
}

// TimeoutPatcher applies queue-row patches.
type TimeoutPatcher interface {
	PatchToolApprovalQueue(ctx context.Context, queue *queuew.ToolApprovalQueue) error
}

// TimeoutNow is a clock factory used by Sweeper.
type TimeoutNow func() time.Time

// SweepInput scopes a timeout sweep. All fields are optional.
type SweepInput struct {
	UserID         string
	ConversationID string
}

// SweepOutput reports canonical timed_out outcomes produced by a sweep.
type SweepOutput struct {
	Outcomes []*api.DecideToolApprovalOutcome
}

// Sweeper transitions due pending approvals into the canonical timed_out state.
type Sweeper struct {
	lister  TimeoutLister
	patcher TimeoutPatcher
	now     TimeoutNow
}

// NewSweeper constructs a Sweeper.
func NewSweeper(lister TimeoutLister, patcher TimeoutPatcher, now TimeoutNow) (*Sweeper, error) {
	if lister == nil {
		return nil, errors.New("approvalqueue: lister is required")
	}
	if patcher == nil {
		return nil, errors.New("approvalqueue: patcher is required")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Sweeper{lister: lister, patcher: patcher, now: now}, nil
}

// Sweep transitions all due pending approvals in scope and returns one
// canonical outcome per transitioned row.
func (s *Sweeper) Sweep(ctx context.Context, in *SweepInput) (*SweepOutput, error) {
	if s == nil {
		return nil, errors.New("approvalqueue: sweeper is nil")
	}
	now := s.now()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	query := &queueread.QueueRowsInput{
		QueueStatus: "pending",
		Has:         &queueread.QueueRowsInputHas{QueueStatus: true},
	}
	if in != nil {
		if id := strings.TrimSpace(in.UserID); id != "" {
			query.UserId = id
			query.Has.UserId = true
		}
		if id := strings.TrimSpace(in.ConversationID); id != "" {
			query.ConversationId = id
			query.Has.ConversationId = true
		}
	}
	rows, err := s.lister.ListToolApprovalQueues(ctx, query)
	if err != nil {
		return nil, err
	}
	outcomes := make([]*api.DecideToolApprovalOutcome, 0)
	for _, row := range rows {
		if !IsTimedOut(row, now) {
			continue
		}
		if err := s.patcher.PatchToolApprovalQueue(ctx, NewTimedOutPatch(row, now)); err != nil {
			return nil, err
		}
		outcomes = append(outcomes, NewTimedOutOutcome(row, now))
	}
	return &SweepOutput{Outcomes: outcomes}, nil
}

// IsTimedOut reports whether a queue row is still pending and its expires_at
// deadline has elapsed at the given instant. Rows without expires_at are never
// treated as timed out by the canonical producer.
func IsTimedOut(row *queueread.QueueRowView, now time.Time) bool {
	if row == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(row.Status), "pending") {
		return false
	}
	if row.ExpiresAt == nil || row.ExpiresAt.IsZero() {
		return false
	}
	return !row.ExpiresAt.After(now)
}

// NewTimedOutPatch builds the canonical queue-row patch for a timeout
// transition.
func NewTimedOutPatch(row *queueread.QueueRowView, now time.Time) *queuew.ToolApprovalQueue {
	upd := &queuew.ToolApprovalQueue{Has: &queuew.ToolApprovalQueueHas{}}
	if row == nil {
		return upd
	}
	upd.SetId(row.Id)
	upd.SetUserId(row.UserId)
	upd.SetToolName(row.ToolName)
	upd.SetArguments(row.Arguments)
	upd.SetStatus(api.ApprovalTimeoutOutcomeStatus)
	upd.SetDecision(api.ApprovalTimeoutOutcomeDecision)
	upd.SetTimedOutAt(now)
	upd.SetErrorMessage(api.ApprovalTimeoutErrorMessage)
	upd.SetUpdatedAt(now)
	return upd
}

// NewTimedOutOutcome builds the canonical timeout outcome for a queue row.
func NewTimedOutOutcome(row *queueread.QueueRowView, now time.Time) *api.DecideToolApprovalOutcome {
	if row == nil {
		return nil
	}
	outcome := &api.DecideToolApprovalOutcome{
		ApprovalID:   row.Id,
		Action:       api.ApprovalTimeoutOutcomeAction,
		Status:       api.ApprovalTimeoutOutcomeStatus,
		Decision:     api.ApprovalTimeoutOutcomeDecision,
		ToolName:     row.ToolName,
		Result:       api.ApprovalTimeoutErrorMessage,
		ErrorMessage: api.ApprovalTimeoutErrorMessage,
	}
	if row.ConversationId != nil {
		outcome.ConversationID = strings.TrimSpace(*row.ConversationId)
	}
	if row.TurnId != nil {
		outcome.TurnID = strings.TrimSpace(*row.TurnId)
	}
	if row.MessageId != nil {
		outcome.MessageID = strings.TrimSpace(*row.MessageId)
	}
	if row.ExpiresAt != nil {
		t := *row.ExpiresAt
		outcome.ExpiresAt = &t
	}
	timedOut := now
	outcome.TimedOutAt = &timedOut
	return outcome
}
