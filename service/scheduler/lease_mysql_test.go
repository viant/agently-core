package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	storedata "github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/datly/bootstrap/connector"
)

// Independent native hosts exercise the database CAS rather than a shared
// process mutex. The service Store must preserve the winner on release too.
func TestSchedulerLeaseMySQLIndependentHosts(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	ctx := context.Background()
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	prefix := fmt.Sprintf("lease-mysql-%d-", time.Now().UnixNano())
	scheduleID, runID := prefix+"schedule", prefix+"run"
	_, err = db.Exec("INSERT INTO schedule(id,name,agent_ref,enabled,created_at) VALUES(?,?,?,1,?)", scheduleID, scheduleID, "fixture", time.Now().UTC())
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := db.Exec("DELETE FROM run WHERE id=?", runID)
		require.NoError(t, e)
		_, e = db.Exec("DELETE FROM schedule WHERE id=?", scheduleID)
		require.NoError(t, e)
	})
	_, err = db.Exec("INSERT INTO run(id,schedule_id,conversation_kind,status,created_at) VALUES(?,?,'scheduled','queued',?)", runID, scheduleID, time.Now().UTC())
	require.NoError(t, err)
	_, file, _, _ := runtime.Caller(0)
	stores := make([]Store, 2)
	for i := range stores {
		host, e := native.New(ctx, native.Options{SourceRoot: filepath.Join(filepath.Dir(file), "..", ".."), Connectors: []connector.Config{{Name: "agently", Driver: "mysql", DSN: dsn}}})
		require.NoError(t, e)
		t.Cleanup(func() { require.NoError(t, host.Shutdown(ctx)) })
		stores[i], e = NewDatlyStore(ctx, host, storedata.NewService(host))
		require.NoError(t, e)
	}
	for _, kind := range []string{"schedule", "run"} {
		t.Run(kind, func(t *testing.T) {
			claim := func(i int, owner string) (bool, error) {
				until := time.Now().UTC().Add(time.Minute)
				if kind == "schedule" {
					return stores[i].TryClaimSchedule(ctx, scheduleID, owner, until)
				}
				return stores[i].TryClaimRun(ctx, runID, owner, until)
			}
			release := func(i int, owner string) (bool, error) {
				if kind == "schedule" {
					return stores[i].ReleaseScheduleLease(ctx, scheduleID, owner)
				}
				return stores[i].ReleaseRunLease(ctx, runID, owner)
			}
			start := make(chan struct{})
			type result struct {
				index int
				won   bool
				err   error
			}
			results := make(chan result, 2)
			for i := 0; i < 2; i++ {
				go func(i int) { <-start; won, e := claim(i, fmt.Sprintf("worker-%d", i)); results <- result{i, won, e} }(i)
			}
			close(start)
			winner, winners := -1, 0
			for i := 0; i < 2; i++ {
				r := <-results
				require.NoError(t, r.err)
				if r.won {
					winner = r.index
					winners++
				}
			}
			require.Equal(t, 1, winners)
			loser := 1 - winner
			assertOwner := func(owner string) {
				t.Helper()
				id := scheduleID
				if kind == "run" {
					id = runID
				}
				var persisted sql.NullString
				require.NoError(t, db.QueryRow("SELECT lease_owner FROM "+kind+" WHERE id=?", id).Scan(&persisted))
				require.Equal(t, owner, persisted.String)
			}
			assertOwner(fmt.Sprintf("worker-%d", winner))
			won, e := release(loser, fmt.Sprintf("worker-%d", loser))
			require.NoError(t, e)
			require.False(t, won)
			assertOwner(fmt.Sprintf("worker-%d", winner))
			won, e = claim(loser, fmt.Sprintf("worker-%d", loser))
			require.NoError(t, e)
			require.False(t, won)
			won, e = release(winner, fmt.Sprintf("worker-%d", winner))
			require.NoError(t, e)
			require.True(t, won)
			won, e = claim(loser, fmt.Sprintf("worker-%d", loser))
			require.NoError(t, e)
			require.True(t, won)
			assertOwner(fmt.Sprintf("worker-%d", loser))
			won, e = release(winner, fmt.Sprintf("worker-%d", winner))
			require.NoError(t, e)
			require.False(t, won)
			won, e = release(loser, fmt.Sprintf("worker-%d", loser))
			require.NoError(t, e)
			require.True(t, won)
			assertOwner("")
		})
	}
}
