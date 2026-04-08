package recovery

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoveryModeContext(t *testing.T) {
	ctx := WithMode(context.Background(), ModeCompact)
	got, ok := ModeFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, ModeCompact, got)
}
