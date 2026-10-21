package maintenance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCleanupPassTimeIsStableAcrossPolicies(t *testing.T) {
	start := time.Date(2026, 10, 21, 12, 0, 0, 123, time.UTC)
	calls := 0
	now := func() time.Time { calls++; return start.Add(time.Duration(calls) * time.Hour) }
	ctx := context.WithValue(context.Background(), passTimeKey{}, start)
	for i := 0; i < 5; i++ {
		require.Equal(t, start, cleanupPassTime(ctx, now))
	}
	require.Zero(t, calls)
	require.Equal(t, start.Add(time.Hour), cleanupPassTime(context.Background(), now))
}
