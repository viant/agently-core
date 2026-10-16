package agui_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	store "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/native"
)

func TestPreparedAppendPreservesOriginalIdentityLeaseAndRevisionGuards(t *testing.T) {
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	repository := store.New(server)
	for _, kind := range []string{"foreign-principal", "stale-run", "wrong-lease", "stale-thread", "prepare-error"} {
		t.Run(kind, func(t *testing.T) {
			a := admission("prepared-guards", kind)
			a.TurnID = "turn-" + kind
			run, _, err := repository.Admit(ctx, a)
			require.NoError(t, err)
			run, err = repository.Claim(ctx, "owner", run.ThreadID, run.RunID, run.Revision, "lease-owner", time.Minute)
			require.NoError(t, err)
			thread, err := repository.GetThread(ctx, "owner", run.ThreadID)
			require.NoError(t, err)
			principal, revision := "owner", run.Revision
			if kind == "foreign-principal" {
				principal = "foreign"
			}
			if kind == "stale-run" {
				revision++
			}
			preparationError := errors.New("fixture preparation failure")
			_, err = repository.AppendPrepared(ctx, principal, run.ThreadID, run.RunID, revision, func(current *store.Thread) ([]json.RawMessage, *store.Change, error) {
				if kind == "prepare-error" {
					return nil, nil, preparationError
				}
				change := &store.Change{LeaseOwner: "lease-owner", Messages: json.RawMessage(`[]`), ExpectedThreadRevision: current.Revision}
				if kind == "wrong-lease" {
					change.LeaseOwner = "wrong-owner"
				}
				if kind == "stale-thread" {
					change.ExpectedThreadRevision--
				}
				return []json.RawMessage{start(run.ThreadID, run.RunID)}, change, nil
			})
			if kind == "prepare-error" {
				require.ErrorIs(t, err, preparationError)
			} else if kind == "foreign-principal" {
				require.ErrorIs(t, err, store.ErrNotFound)
			} else {
				require.ErrorIs(t, err, store.ErrConflict)
			}
			latest, err := repository.GetRun(ctx, "owner", run.ThreadID, run.RunID)
			require.NoError(t, err)
			require.Equal(t, run.Revision, latest.Revision)
			require.Zero(t, latest.LastSequence)
			require.Equal(t, run.LeaseOwner, latest.LeaseOwner)
			latestThread, err := repository.GetThread(ctx, "owner", run.ThreadID)
			require.NoError(t, err)
			require.Equal(t, thread.Revision, latestThread.Revision)
			require.JSONEq(t, string(thread.State), string(latestThread.State))
			require.JSONEq(t, string(thread.Messages), string(latestThread.Messages))
		})
	}
}
