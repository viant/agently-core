package conversationtree

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	conversationmeta "github.com/viant/agently-core/internal/datly/conversation/cleanup/read"
	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	goalread "github.com/viant/agently-core/internal/datly/goal/read"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

func TestRetentionPreparationReusesOnlyPrivateUnlockedMetadata(t *testing.T) {
	calls := 0
	invoker := mutationTestInvoker(func(_ context.Context, request dexec.ComponentRequest) (any, error) {
		calls++
		return &conversationmeta.Output{Data: []*conversationmeta.Conversation{{Id: fmt.Sprint(calls)}}}, nil
	})
	d := NewSystemDiscoverer(invoker, nil)
	read := func(lock bool) string {
		q := &convread.ConversationInput{}
		q.SetIds([]string{"b", "a"})
		rows, err := ReadCleanupConversations(context.Background(), d.Invoker, q, lock)
		require.NoError(t, err)
		return rows[0].Id
	}
	require.Equal(t, "1", read(false))
	require.Equal(t, "1", read(false))
	require.Equal(t, "2", read(true), "locking/manual revalidation cannot reuse metadata")
	d = NewSystemDiscoverer(invoker, nil)
	require.Equal(t, "3", read(false), "a new attempt must rebuild its snapshot")
	for _, method := range []string{"GET", "PATCH"} {
		request := dexec.ComponentRequest{Target: dexec.ComponentTarget{Route: spec.RouteRef{Method: method, Path: "/v1/api/agently/conversation"}}}
		_, err := d.Invoker.InvokeComponent(context.Background(), request)
		require.NoError(t, err)
		_, err = d.Invoker.InvokeComponent(context.Background(), request)
		require.NoError(t, err)
	}
	require.Equal(t, 7, calls)
}

func TestRetentionPreparationDoesNotCacheFailuresOrIgnoreCancellation(t *testing.T) {
	calls := 0
	invoker := mutationTestInvoker(func(_ context.Context, request dexec.ComponentRequest) (any, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("temporary read failure")
		}
		return &conversationmeta.Output{}, nil
	})
	d := NewSystemDiscoverer(invoker, nil)
	read := func(ctx context.Context) error {
		q := &convread.ConversationInput{}
		q.SetId("a")
		_, err := ReadCleanupConversations(ctx, d.Invoker, q, false)
		return err
	}
	require.ErrorContains(t, read(context.Background()), "temporary")
	require.NoError(t, read(context.Background()))
	require.NoError(t, read(context.Background()))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, read(ctx), context.Canceled)
	require.Equal(t, 2, calls)
}

func TestRootRetentionCannotRelaxManualDiscovery(t *testing.T) {
	manual := &Discoverer{}
	manual.QualifyRootRetention()
	require.False(t, manual.rootQualified)
	system := NewSystemDiscoverer(nil, nil)
	require.False(t, system.rootQualified)
	system.QualifyRootRetention()
	require.True(t, system.rootQualified)
	_, err := NewSystemDiscoverer(nil, nil).Discover(context.Background(), "root")
	require.ErrorContains(t, err, "not configured")
}

type planningSchema struct{}

func (planningSchema) HasTable(context.Context, string, string) (bool, error) { return true, nil }

func TestRetentionGoalsAreReadOncePerAttemptButManualReadsStayFresh(t *testing.T) {
	calls := 0
	invoker := mutationTestInvoker(func(context.Context, dexec.ComponentRequest) (any, error) {
		calls++
		return &goalread.GoalOutput{}, nil
	})
	d := NewSystemDiscoverer(invoker, planningSchema{})
	for i := 0; i < 3; i++ {
		_, err := d.goalRows(context.Background(), []string{"b", "a"})
		require.NoError(t, err)
	}
	require.Equal(t, 1, calls)
	d = &Discoverer{Invoker: invoker, Schema: planningSchema{}}
	for i := 0; i < 3; i++ {
		_, err := d.goalRows(context.Background(), []string{"a", "b"})
		require.NoError(t, err)
	}
	require.Equal(t, 4, calls)
}
