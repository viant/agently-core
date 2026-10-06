package approvalqueue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	toolapprovalqueuemodel "github.com/viant/agently-core/model/toolapprovalqueue"

	"github.com/viant/agently-core/sdk/api"
)

type fakeTimeoutStore struct {
	mu   sync.Mutex
	rows map[string]*toolapprovalqueuemodel.ToolApprovalQueue
}

func newFakeTimeoutStore() *fakeTimeoutStore {
	return &fakeTimeoutStore{rows: map[string]*toolapprovalqueuemodel.ToolApprovalQueue{}}
}

func (f *fakeTimeoutStore) seed(rec *toolapprovalqueuemodel.ToolApprovalQueue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *rec
	f.rows[rec.Id] = &cp
}

func (f *fakeTimeoutStore) ListToolApprovalQueues(_ context.Context, in *toolapprovalqueuemodel.QueueRowsInput) ([]*toolapprovalqueuemodel.QueueRowView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*toolapprovalqueuemodel.QueueRowView
	for _, r := range f.rows {
		if in != nil && in.Has != nil {
			if in.Has.Id && r.Id != in.Id {
				continue
			}
			if in.Has.QueueStatus && r.Status != in.QueueStatus {
				continue
			}
			if in.Has.UserId && r.UserId != in.UserId {
				continue
			}
			if in.Has.ConversationId {
				if r.ConversationId == nil || *r.ConversationId != in.ConversationId {
					continue
				}
			}
		}
		row := &toolapprovalqueuemodel.QueueRowView{
			Id:         r.Id,
			UserId:     r.UserId,
			ToolName:   r.ToolName,
			Status:     r.Status,
			Decision:   r.Decision,
			ExpiresAt:  r.ExpiresAt,
			TimedOutAt: r.TimedOutAt,
			Arguments:  append([]byte(nil), r.Arguments...),
		}
		if r.ConversationId != nil {
			cid := *r.ConversationId
			row.ConversationId = &cid
		}
		if r.TurnId != nil {
			tid := *r.TurnId
			row.TurnId = &tid
		}
		if r.MessageId != nil {
			mid := *r.MessageId
			row.MessageId = &mid
		}
		if r.ErrorMessage != nil {
			em := *r.ErrorMessage
			row.ErrorMessage = &em
		}
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeTimeoutStore) PatchToolApprovalQueue(_ context.Context, q *toolapprovalqueuemodel.ToolApprovalQueue) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.rows[q.Id]
	if !ok {
		cp := *q
		f.rows[q.Id] = &cp
		return nil
	}
	if q.Has == nil {
		return nil
	}
	if q.Has.Status {
		cur.Status = q.Status
	}
	if q.Has.Decision {
		cur.Decision = q.Decision
	}
	if q.Has.ExpiresAt {
		cur.ExpiresAt = q.ExpiresAt
	}
	if q.Has.TimedOutAt {
		cur.TimedOutAt = q.TimedOutAt
	}
	if q.Has.ErrorMessage {
		cur.ErrorMessage = q.ErrorMessage
	}
	if q.Has.UpdatedAt {
		cur.UpdatedAt = q.UpdatedAt
	}
	return nil
}

func mustTimeoutQueueRow(id, userID string, expiresAt *time.Time) *toolapprovalqueuemodel.ToolApprovalQueue {
	row := &toolapprovalqueuemodel.ToolApprovalQueue{
		Id:        id,
		UserId:    userID,
		ToolName:  "system/os/getEnv",
		Arguments: []byte(`{"names":["LOGNAME"]}`),
		Status:    "pending",
		Has:       &toolapprovalqueuemodel.ToolApprovalQueueHas{},
	}
	if expiresAt != nil {
		row.ExpiresAt = expiresAt
	}
	return row
}

func TestSweeper_TransitionsExpiredPendingRowsAndEmitsCanonicalOutcome(t *testing.T) {
	store := newFakeTimeoutStore()
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	past := now.Add(-30 * time.Second)
	store.seed(mustTimeoutQueueRow("expired-1", "user-1", &past))

	sweeper, err := NewSweeper(store, store, func() time.Time { return now })
	require.NoError(t, err)

	out, err := sweeper.Sweep(context.Background(), &SweepInput{UserID: "user-1"})
	require.NoError(t, err)
	require.Len(t, out.Outcomes, 1)

	o := out.Outcomes[0]
	require.Equal(t, "expired-1", o.ApprovalID)
	require.Equal(t, api.ApprovalTimeoutOutcomeAction, o.Action)
	require.Equal(t, api.ApprovalTimeoutOutcomeStatus, o.Status)
	require.Equal(t, api.ApprovalTimeoutOutcomeDecision, o.Decision)
	require.Equal(t, api.ApprovalTimeoutErrorMessage, o.ErrorMessage)
	require.Equal(t, api.ApprovalTimeoutErrorMessage, o.Result)
	require.Equal(t, "system/os/getEnv", o.ToolName)
	require.NotNil(t, o.ExpiresAt)
	require.True(t, o.ExpiresAt.Equal(past))
	require.NotNil(t, o.TimedOutAt)
	require.True(t, o.TimedOutAt.Equal(now))

	rows, err := store.ListToolApprovalQueues(context.Background(), &toolapprovalqueuemodel.QueueRowsInput{
		Id:  "expired-1",
		Has: &toolapprovalqueuemodel.QueueRowsInputHas{Id: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, api.ApprovalTimeoutOutcomeStatus, rows[0].Status)
	require.NotNil(t, rows[0].Decision)
	require.Equal(t, api.ApprovalTimeoutOutcomeDecision, *rows[0].Decision)
	require.NotNil(t, rows[0].TimedOutAt)
	require.True(t, rows[0].TimedOutAt.Equal(now))
}

func TestSweeper_LeavesPendingRowWithFutureDeadlineAlone(t *testing.T) {
	store := newFakeTimeoutStore()
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	future := now.Add(5 * time.Minute)
	store.seed(mustTimeoutQueueRow("future-1", "user-1", &future))

	sweeper, err := NewSweeper(store, store, func() time.Time { return now })
	require.NoError(t, err)

	out, err := sweeper.Sweep(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, out.Outcomes)

	rows, err := store.ListToolApprovalQueues(context.Background(), &toolapprovalqueuemodel.QueueRowsInput{
		Id:  "future-1",
		Has: &toolapprovalqueuemodel.QueueRowsInputHas{Id: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "pending", rows[0].Status)
	require.Nil(t, rows[0].TimedOutAt)
}

func TestSweeper_LeavesPendingRowWithoutDeadlineAlone(t *testing.T) {
	store := newFakeTimeoutStore()
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	store.seed(mustTimeoutQueueRow("no-deadline-1", "user-1", nil))

	sweeper, err := NewSweeper(store, store, func() time.Time { return now })
	require.NoError(t, err)

	out, err := sweeper.Sweep(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, out.Outcomes)

	rows, err := store.ListToolApprovalQueues(context.Background(), &toolapprovalqueuemodel.QueueRowsInput{
		Id:  "no-deadline-1",
		Has: &toolapprovalqueuemodel.QueueRowsInputHas{Id: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "pending", rows[0].Status)
	require.Nil(t, rows[0].TimedOutAt)
}

func TestSweeper_LeavesResolvedRowsAlone(t *testing.T) {
	store := newFakeTimeoutStore()
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	past := now.Add(-30 * time.Second)
	for _, status := range []string{"approved", "rejected", "canceled", "executed", "failed", "timed_out"} {
		row := mustTimeoutQueueRow("resolved-"+status, "user-1", &past)
		row.Status = status
		store.seed(row)
	}

	sweeper, err := NewSweeper(store, store, func() time.Time { return now })
	require.NoError(t, err)

	out, err := sweeper.Sweep(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, out.Outcomes)
}

func TestSweeper_ScopesByUserAndConversation(t *testing.T) {
	store := newFakeTimeoutStore()
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	rowA := mustTimeoutQueueRow("a-1", "user-a", &past)
	convA := "conv-a"
	rowA.ConversationId = &convA
	rowB := mustTimeoutQueueRow("b-1", "user-b", &past)
	convB := "conv-b"
	rowB.ConversationId = &convB
	store.seed(rowA)
	store.seed(rowB)

	sweeper, err := NewSweeper(store, store, func() time.Time { return now })
	require.NoError(t, err)

	out, err := sweeper.Sweep(context.Background(), &SweepInput{UserID: "user-a"})
	require.NoError(t, err)
	require.Len(t, out.Outcomes, 1)
	require.Equal(t, "a-1", out.Outcomes[0].ApprovalID)

	rows, err := store.ListToolApprovalQueues(context.Background(), &toolapprovalqueuemodel.QueueRowsInput{
		Id:  "b-1",
		Has: &toolapprovalqueuemodel.QueueRowsInputHas{Id: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "pending", rows[0].Status, "out-of-scope row must remain pending")
}

func (f *fakeTimeoutStore) ClaimToolApprovalDecision(ctx context.Context, previous *toolapprovalqueuemodel.QueueRowView, principal, action string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := f.rows[previous.Id]
	if row == nil || row.UserId != principal || row.Status != "pending" || action != "timeout" {
		return errors.New("conditional timeout conflict")
	}
	row.Status = "timed_out"
	return nil
}

type lostTimeoutClaimStore struct {
	*fakeTimeoutStore
	patches int
}

func (s *lostTimeoutClaimStore) ClaimToolApprovalDecision(context.Context, *toolapprovalqueuemodel.QueueRowView, string, string) error {
	return errors.New("lost conditional timeout claim")
}
func (s *lostTimeoutClaimStore) PatchToolApprovalQueue(ctx context.Context, row *toolapprovalqueuemodel.ToolApprovalQueue) error {
	s.patches++
	return s.fakeTimeoutStore.PatchToolApprovalQueue(ctx, row)
}
func TestSweeperLostClaimProducesNoTimeoutWriteOrOutcome(t *testing.T) {
	now := time.Now().UTC()
	expiry := now.Add(-time.Second)
	store := &lostTimeoutClaimStore{fakeTimeoutStore: newFakeTimeoutStore()}
	store.seed(&toolapprovalqueuemodel.ToolApprovalQueue{Id: "lost", UserId: "owner", Status: "pending", ExpiresAt: &expiry})
	sweeper, err := NewSweeper(store, store, func() time.Time { return now })
	require.NoError(t, err)
	outcome, err := sweeper.Sweep(context.Background(), &SweepInput{UserID: "owner"})
	require.Error(t, err)
	require.Nil(t, outcome)
	require.Zero(t, store.patches)
}
