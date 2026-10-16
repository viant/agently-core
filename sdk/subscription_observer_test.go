package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
)

// Poll the native journal only when an observer has committed another event.
// This avoids a second native transaction every 10ms starving the writer under
// race instrumentation. Existing Eventually deadlines still cover all waiting
// and the actual native Replay assertions; no fake events or extra timeout.
type subscriptionCommitStore struct {
	aguistore.Store
	committed chan struct{}
}

func observeSubscriptionCommits(store aguistore.Store) *subscriptionCommitStore {
	return &subscriptionCommitStore{Store: store, committed: make(chan struct{}, 1)}
}
func (s *subscriptionCommitStore) Append(ctx context.Context, principal, thread, run string, revision int64, events []json.RawMessage, change *aguistore.Change) (*aguistore.Run, error) {
	value, err := s.Store.Append(ctx, principal, thread, run, revision, events, change)
	if err == nil {
		select {
		case s.committed <- struct{}{}:
		default:
		}
	}
	return value, err
}
func (s *subscriptionCommitStore) ReadFeedActivationFacts(ctx context.Context, principal, thread, feed string) ([]agui.FeedLifecycleFact, error) {
	reader, ok := s.Store.(aguistore.FeedActivationJournalReader)
	if !ok {
		return nil, fmt.Errorf("native complete lifecycle reader is unavailable")
	}
	return reader.ReadFeedActivationFacts(ctx, principal, thread, feed)
}
func (s *subscriptionCommitStore) afterCommit(check func() bool) bool {
	select {
	case <-s.committed:
		return check()
	default:
		return false
	}
}

// Join before fixture cleanup shuts down native persistence or restores global
// workspace configuration, including when an assertion aborts the test.
func startSubscriptionObserver(t *testing.T, cancel context.CancelFunc, observe func() error) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); done <- observe() }()
	t.Cleanup(func() { cancel(); <-finished })
	return done
}

func TestSubscriptionCommitNotificationRequiresNativeSuccess(t *testing.T) {
	store := &commitFixtureStore{}
	observed := observeSubscriptionCommits(store)
	checked := 0
	check := func() bool { checked++; return true }
	require.False(t, observed.afterCommit(check))
	require.Zero(t, checked)
	store.fail = true
	_, err := observed.Append(context.Background(), "owner", "thread", "run", 1, nil, nil)
	require.Error(t, err)
	require.False(t, observed.afterCommit(check))
	store.fail = false
	_, err = observed.Append(context.Background(), "owner", "thread", "run", 1, nil, nil)
	require.NoError(t, err)
	require.True(t, observed.afterCommit(check))
	require.Equal(t, 1, checked)
}

type commitFixtureStore struct {
	aguistore.Store
	fail bool
}

func (s *commitFixtureStore) Append(context.Context, string, string, string, int64, []json.RawMessage, *aguistore.Change) (*aguistore.Run, error) {
	if s.fail {
		return nil, fmt.Errorf("rolled back fixture")
	}
	return &aguistore.Run{}, nil
}
