package delete

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPayloadModeDefaultValidationAndPinning(t *testing.T) {
	require.Equal(t, BulkMode, PinnedMode(context.Background()))
	for _, setting := range []string{"", "row", "bulk", "invalid"} {
		t.Run(setting, func(t *testing.T) {
			t.Setenv(ModeEnvironment, setting)
			ctx, err := PinMode(context.Background())
			if setting == "invalid" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			want := BulkMode
			if setting == "row" {
				want = RowMode
			}
			require.Equal(t, want, PinnedMode(ctx))
			t.Setenv(ModeEnvironment, "invalid")
			nested, err := PinMode(ctx)
			require.NoError(t, err)
			require.Equal(t, want, PinnedMode(nested))
		})
	}
}
