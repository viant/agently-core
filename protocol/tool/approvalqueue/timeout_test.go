package approvalqueue

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	queueread "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	queuew "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/write"
	"github.com/viant/agently-core/sdk/api"
)

type fakeTimeoutStore struct {
	mu   sync.Mutex
	rows map[string]*queuew.ToolApprovalQueue
}

func newFakeTimeoutStore() *fakeTimeoutStore {
	return &fakeTimeoutStore{rows: map[string]*queuew.ToolApprovalQueue{}}
}

func (f *fakeTimeoutStore) seed(rec *queuew.ToolApprovalQueue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *rec
	f.rows[rec.Id] = &cp
}

func (f *fakeTimeoutStore) ListToolApprovalQueues(_ context.Context, in *queueread.QueueRowsInput) ([]*queueread.QueueRowView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*queueread.QueueRowView
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
		row := &queueread.QueueRowView{
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

func (f *fakeTimeoutStore) PatchToolApprovalQueue(_ context.Context, q *queuew.ToolApprovalQueue) error {
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

func mustTimeoutQueueRow(id, userID string, expiresAt *time.Time) *queuew.ToolApprovalQueue {
	row := &queuew.ToolApprovalQueue{
		Id:        id,
		UserId:    userID,
		ToolName:  "system/os/getEnv",
		Arguments: []byte(`{"names":["LOGNAME"]}`),
		Status:    "pending",
		Has:       &queuew.ToolApprovalQueueHas{},
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

	rows, err := store.ListToolApprovalQueues(context.Background(), &queueread.QueueRowsInput{
		Id:  "expired-1",
		Has: &queueread.QueueRowsInputHas{Id: true},
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

	rows, err := store.ListToolApprovalQueues(context.Background(), &queueread.QueueRowsInput{
		Id:  "future-1",
		Has: &queueread.QueueRowsInputHas{Id: true},
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

	rows, err := store.ListToolApprovalQueues(context.Background(), &queueread.QueueRowsInput{
		Id:  "no-deadline-1",
		Has: &queueread.QueueRowsInputHas{Id: true},
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

	rows, err := store.ListToolApprovalQueues(context.Background(), &queueread.QueueRowsInput{
		Id:  "b-1",
		Has: &queueread.QueueRowsInputHas{Id: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "pending", rows[0].Status, "out-of-scope row must remain pending")
}
