package discovery

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiscoveryModeContext(t *testing.T) {
	ctx := WithMode(context.Background(), Mode{Scheduler: true, ScheduleID: "sched-1"})
	got, ok := ModeFromContext(ctx)
	require.True(t, ok)
	require.True(t, got.Scheduler)
	require.Equal(t, "sched-1", got.ScheduleID)
}

func TestMergeMode(t *testing.T) {
	ctx := WithMode(context.Background(), Mode{Scheduler: true, ScheduleID: "sched-1"})
	ctx = MergeMode(ctx, Mode{Strict: true, ScheduleRunID: "run-1"})
	got, ok := ModeFromContext(ctx)
	require.True(t, ok)
	require.True(t, got.Scheduler)
	require.True(t, got.Strict)
	require.Equal(t, "sched-1", got.ScheduleID)
	require.Equal(t, "run-1", got.ScheduleRunID)
}
