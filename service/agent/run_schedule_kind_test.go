package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

type capturePatchRunsService struct {
	data.Service
	rows []*agrunwrite.MutableRunView
}

func (s *capturePatchRunsService) PatchRuns(_ context.Context, rows []*agrunwrite.MutableRunView) ([]*agrunwrite.MutableRunView, error) {
	s.rows = append(s.rows, rows...)
	return rows, nil
}

func TestEnsureRunRecordPreservesScheduledKind(t *testing.T) {
	store := &capturePatchRunsService{}
	svc := &Service{dataService: store}
	turn := memory.TurnMeta{TurnID: "run-1", ConversationID: "conv-1"}

	err := svc.ensureRunRecord(context.Background(), turn, "running", "sched-1")
	require.NoError(t, err)
	require.Len(t, store.rows, 1)

	row := store.rows[0]
	require.Equal(t, "run-1", row.Id)
	require.Equal(t, "running", row.Status)
	require.NotNil(t, row.ScheduleID)
	require.Equal(t, "sched-1", *row.ScheduleID)
	require.NotNil(t, row.ConversationKind)
	require.Equal(t, "scheduled", *row.ConversationKind)
	require.NotNil(t, row.StartedAt)
	require.NotNil(t, row.Iteration)
	require.Equal(t, 1, *row.Iteration)
	require.Nil(t, row.CreatedAt)
}

func TestEnsureRunRecordDefaultsToInteractive(t *testing.T) {
	store := &capturePatchRunsService{}
	svc := &Service{
		dataService:             store,
		runWorkerHost:           "host-a",
		runLeaseOwner:           "host-a:123:lease",
		runHeartbeatIntervalSec: 60,
	}
	turn := memory.TurnMeta{TurnID: "run-2", ConversationID: "conv-2"}

	err := svc.ensureRunRecord(context.Background(), turn, "running", "")
	require.NoError(t, err)
	require.Len(t, store.rows, 1)

	row := store.rows[0]
	require.Equal(t, "run-2", row.Id)
	require.Equal(t, "running", row.Status)
	require.Nil(t, row.ScheduleID)
	require.NotNil(t, row.ConversationKind)
	require.Equal(t, "interactive", *row.ConversationKind)
	require.NotNil(t, row.StartedAt)
	require.NotNil(t, row.CreatedAt)
	require.NotNil(t, row.WorkerHost)
	require.Equal(t, "host-a", *row.WorkerHost)
	require.NotNil(t, row.LeaseOwner)
	require.Equal(t, "host-a:123:lease", *row.LeaseOwner)
	require.NotNil(t, row.LastHeartbeatAt)
	require.NotNil(t, row.HeartbeatIntervalSec)
	require.Equal(t, 60, *row.HeartbeatIntervalSec)
	require.NotNil(t, row.LeaseUntil)
}

func TestStartRunHeartbeatPatchesInteractiveRunLiveness(t *testing.T) {
	store := &capturePatchRunsService{}
	svc := &Service{
		dataService:             store,
		runWorkerHost:           "host-a",
		runLeaseOwner:           "host-a:123:lease",
		runHeartbeatIntervalSec: 2,
		runHeartbeatEvery:       50 * time.Millisecond,
	}
	turn := memory.TurnMeta{TurnID: "run-heartbeat", ConversationID: "conv-heartbeat"}

	stop := svc.startRunHeartbeat(context.Background(), turn)
	defer stop()

	require.Eventually(t, func() bool {
		return len(store.rows) > 0
	}, 3*time.Second, 50*time.Millisecond)

	last := store.rows[len(store.rows)-1]
	require.Equal(t, "run-heartbeat", last.Id)
	require.NotNil(t, last.LastHeartbeatAt)
	require.NotNil(t, last.LeaseUntil)
	require.NotNil(t, last.LeaseOwner)
	require.Equal(t, "host-a:123:lease", *last.LeaseOwner)
}
