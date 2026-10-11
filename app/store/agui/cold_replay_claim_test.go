package agui_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	store "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/native"
)

// The first replay and claim used to consume both SQLite pool connections while
// replay's lazy reader materialization needed a third connection for discovery.
func TestColdNativeConcurrentReplayAndClaim(t *testing.T) {
	for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(context.Background())
	repository := store.New(server)
	run, fresh, err := repository.Admit(ctx, admission("cold-thread", "cold-run"))
	require.NoError(t, err)
	require.True(t, fresh)
	ready := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-ready
		_, err := repository.Replay(ctx, "owner", run.ThreadID, run.RunID, 0, 256)
		results <- err
	}()
	go func() {
		<-ready
		_, err := repository.Claim(ctx, "owner", run.ThreadID, run.RunID, run.Revision, "observer", time.Minute)
		results <- err
	}()
	close(ready)
	for i := 0; i < 2; i++ {
		require.NoError(t, <-results)
	}
	claimed, err := repository.GetRun(ctx, "owner", run.ThreadID, run.RunID)
	require.NoError(t, err)
	require.Equal(t, "observer", claimed.LeaseOwner)
	require.Equal(t, int64(1), claimed.LeaseRevision)
	require.Equal(t, int64(0), claimed.LastSequence, "claim/replay must not synthesize execution events")
}
