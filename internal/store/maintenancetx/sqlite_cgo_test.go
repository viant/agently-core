//go:build cgo

package maintenancetx

import (
	"testing"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestCGOSQLiteContention(t *testing.T) {
	for _, err := range []error{sqlite3.Error{Code: sqlite3.ErrBusy}, sqlite3.Error{Code: sqlite3.ErrLocked}, &sqlite3.Error{Code: sqlite3.ErrBusy}} {
		require.True(t, onlyRetryable(err))
	}
	require.False(t, onlyRetryable(sqlite3.Error{Code: sqlite3.ErrConstraint}))
}
