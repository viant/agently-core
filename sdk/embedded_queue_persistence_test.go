package sdk

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	authctx "github.com/viant/agently-core/internal/auth"
	toolapprovalqueuemodel "github.com/viant/agently-core/model/toolapprovalqueue"
	turnmodel "github.com/viant/agently-core/model/turn"
)

type queueRetryStore struct {
	status  string
	patch   *toolapprovalqueuemodel.ToolApprovalQueue
	patches int
	err     error
	ignore  bool
}

func (s *queueRetryStore) PatchToolApprovalQueue(_ context.Context, row *toolapprovalqueuemodel.ToolApprovalQueue) error {
	s.patch = row
	s.patches++
	if s.err != nil {
		return s.err
	}
	if !s.ignore && row.Has != nil && row.Has.Status {
		s.status = row.Status
	}
	return nil
}
func (s *queueRetryStore) ListToolApprovalQueues(_ context.Context, _ *toolapprovalqueuemodel.QueueRowsInput) ([]*toolapprovalqueuemodel.QueueRowView, error) {
	return []*toolapprovalqueuemodel.QueueRowView{{Id: "approval", Status: s.status}}, nil
}
func TestToolApprovalCanonicalFallbackRetriesAndVerifies(t *testing.T) {
	ctx := context.Background()
	store := &queueRetryStore{status: "approved"}
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	err := ensureToolApprovalStatus(ctx, store, "approval", "pending", func() error {
		return fallbackToolApprovalUpdate(ctx, store, " approval ", map[string]interface{}{"status": "pending", "decision": nil, "approved_by_user_id": " ", "approved_at": nil, "executed_at": nil, "updated_at": now, "error_message": "execution failed"})
	})
	require.NoError(t, err)
	require.Equal(t, 1, store.patches)
	require.Equal(t, "approval", store.patch.Id)
	require.Equal(t, "pending", store.patch.Status)
	require.True(t, store.patch.Has.Decision)
	require.Nil(t, store.patch.Decision)
	require.True(t, store.patch.Has.ApprovedByUserId)
	require.Nil(t, store.patch.ApprovedByUserId)
	require.True(t, store.patch.Has.ApprovedAt)
	require.Nil(t, store.patch.ApprovedAt)
	require.True(t, store.patch.Has.ExecutedAt)
	require.Nil(t, store.patch.ExecutedAt)
	require.True(t, store.patch.Has.UpdatedAt)
	require.Equal(t, now, *store.patch.UpdatedAt)
	require.False(t, store.patch.Has.UserId)
	require.False(t, store.patch.Has.Arguments)
	err = ensureToolApprovalStatus(ctx, store, "approval", "pending", func() error { return errors.New("unexpected retry") })
	require.NoError(t, err, "already persisted status must skip retry")
	store.ignore = true
	err = ensureToolApprovalStatus(ctx, store, "approval", "executed", func() error {
		return fallbackToolApprovalUpdate(ctx, store, "approval", map[string]interface{}{"status": "executed", "executed_at": now})
	})
	require.ErrorContains(t, err, "did not transition")
	denied := errors.New("denied")
	store.err = denied
	err = ensureToolApprovalStatus(ctx, store, "approval", "executed", func() error {
		return fallbackToolApprovalUpdate(ctx, store, "approval", map[string]interface{}{"status": "executed"})
	})
	require.ErrorIs(t, err, denied)
}

type queueTurnData struct {
	data.Service
	get func(context.Context, *turnmodel.TurnLookupInput, []data.Option) (*turnmodel.TurnLookupView, error)
}

func (s *queueTurnData) GetTurnByID(ctx context.Context, input *turnmodel.TurnLookupInput, opts ...data.Option) (*turnmodel.TurnLookupView, error) {
	return s.get(ctx, input, opts)
}
func TestQueueTurnAgentUsesScopedNativeDataRead(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	agent := " agent-id "
	client := &backendClient{data: &queueTurnData{get: func(got context.Context, input *turnmodel.TurnLookupInput, opts []data.Option) (*turnmodel.TurnLookupView, error) {
		require.Equal(t, "owner", authctx.EffectiveUserID(got))
		require.Equal(t, "turn-id", input.ID)
		require.True(t, input.Has.ID)
		require.NotEmpty(t, opts)
		return &turnmodel.TurnLookupView{AgentIdUsed: &agent}, nil
	}}}
	value, err := lookupQueueTurnAgentID(ctx, client, " turn-id ")
	require.NoError(t, err)
	require.Equal(t, "agent-id", value)
	denied := errors.New("permission denied")
	client.data = &queueTurnData{get: func(context.Context, *turnmodel.TurnLookupInput, []data.Option) (*turnmodel.TurnLookupView, error) {
		return nil, denied
	}}
	_, err = lookupQueueTurnAgentID(ctx, client, "turn-id")
	require.ErrorIs(t, err, denied)
}
