package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	"github.com/viant/agently-core/runtime/memory"
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
	svc := &Service{dataService: store}
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
}
