package goal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseStatus(t *testing.T) {
	for _, valid := range []Status{StatusActive, StatusPaused, StatusBlocked, StatusComplete, StatusBudgetLimited, StatusUsageLimited} {
		got, err := ParseStatus(string(valid))
		require.NoError(t, err)
		require.Equal(t, valid, got)
	}

	_, err := ParseStatus("retired")
	require.Error(t, err)

	_, err = ParseStatus("")
	require.Error(t, err)
}

func TestStatusPredicates(t *testing.T) {
	require.True(t, StatusActive.IsActive())
	require.False(t, StatusPaused.IsActive())

	require.True(t, StatusComplete.IsTerminal())
	require.True(t, StatusBlocked.IsTerminal())
	require.True(t, StatusBudgetLimited.IsTerminal())
	require.True(t, StatusUsageLimited.IsTerminal())
	require.False(t, StatusActive.IsTerminal())
	require.False(t, StatusPaused.IsTerminal())
}
