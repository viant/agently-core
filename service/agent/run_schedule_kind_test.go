package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	runmodel "github.com/viant/agently-core/model/run"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

type capturePatchRunsService struct {
	data.Service
	mu   sync.Mutex
	rows []*runmodel.MutableRunView
}

func (s *capturePatchRunsService) PatchRuns(_ context.Context, rows []*runmodel.MutableRunView) ([]*runmodel.MutableRunView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range rows {
		b, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		var snapshot runmodel.MutableRunView
		if err = json.Unmarshal(b, &snapshot); err != nil {
			return nil, err
		}
		s.rows = append(s.rows, &snapshot)
	}
	return rows, nil
}

func (s *capturePatchRunsService) snapshot() []*runmodel.MutableRunView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*runmodel.MutableRunView(nil), s.rows...)
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

func TestUpdateRunIterationPreservesScheduledLease(t *testing.T) {
	store := &capturePatchRunsService{}
	svc := &Service{
		dataService:             store,
		runLeaseOwner:           "host-a:123:lease",
		runHeartbeatIntervalSec: 60,
	}
	turn := memory.TurnMeta{TurnID: "run-scheduled", ConversationID: "conv-scheduled"}

	svc.updateRunIteration(context.Background(), turn, 3, "sched-1")
	require.Len(t, store.rows, 1)

	row := store.rows[0]
	require.Equal(t, "run-scheduled", row.Id)
	require.Equal(t, "running", row.Status)
	require.NotNil(t, row.Iteration)
	require.Equal(t, 3, *row.Iteration)
	require.Nil(t, row.LeaseOwner)
	require.Nil(t, row.LeaseUntil)
	require.Nil(t, row.LastHeartbeatAt)
	require.Nil(t, row.HeartbeatIntervalSec)
}

func TestUpdateRunIterationTouchesInteractiveRunHeartbeat(t *testing.T) {
	store := &capturePatchRunsService{}
	svc := &Service{
		dataService:             store,
		runLeaseOwner:           "host-a:123:lease",
		runHeartbeatIntervalSec: 60,
	}
	turn := memory.TurnMeta{TurnID: "run-interactive", ConversationID: "conv-interactive"}

	svc.updateRunIteration(context.Background(), turn, 2, "")
	require.Len(t, store.rows, 1)

	row := store.rows[0]
	require.Equal(t, "run-interactive", row.Id)
	require.Equal(t, "running", row.Status)
	require.NotNil(t, row.Iteration)
	require.Equal(t, 2, *row.Iteration)
	require.NotNil(t, row.LeaseOwner)
	require.Equal(t, "host-a:123:lease", *row.LeaseOwner)
	require.NotNil(t, row.LeaseUntil)
	require.NotNil(t, row.LastHeartbeatAt)
	require.NotNil(t, row.HeartbeatIntervalSec)
	require.Equal(t, 60, *row.HeartbeatIntervalSec)
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
		return len(store.snapshot()) > 0
	}, 3*time.Second, 50*time.Millisecond)

	rows := store.snapshot()
	last := rows[len(rows)-1]
	require.Equal(t, "run-heartbeat", last.Id)
	require.NotNil(t, last.LastHeartbeatAt)
	require.NotNil(t, last.LeaseUntil)
	require.NotNil(t, last.LeaseOwner)
	require.Equal(t, "host-a:123:lease", *last.LeaseOwner)
}

func TestStopRunHeartbeatThen_StopsBeforeFinalize(t *testing.T) {
	order := make([]string, 0, 2)

	err := stopRunHeartbeatThen(func() {
		order = append(order, "stop")
	}, func() error {
		order = append(order, "finalize")
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, []string{"stop", "finalize"}, order)
}

func TestStopRunHeartbeatThen_PropagatesFinalizeError(t *testing.T) {
	want := errors.New("boom")

	err := stopRunHeartbeatThen(func() {}, func() error {
		return want
	})

	require.ErrorIs(t, err, want)
}
