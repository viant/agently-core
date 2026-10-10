package conversationtree

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	conversationmeta "github.com/viant/agently-core/internal/datly/conversation/cleanup/read"
	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	runmeta "github.com/viant/agently-core/internal/datly/run/cleanup/read"
	runread "github.com/viant/agently-core/internal/datly/run/read"
	dexec "github.com/viant/datly/exec"
)

func TestCleanupMetadataBatchesFreshReadsAndLockForwarding(t *testing.T) {
	for _, kind := range []string{"conversation", "run"} {
		t.Run(kind, func(t *testing.T) {
			var ids []string
			for i := 0; i < 401; i++ {
				ids = append(ids, fmt.Sprintf("id-%03d", i))
			}
			calls := 0
			invoker := mutationTestInvoker(func(_ context.Context, request dexec.ComponentRequest) (any, error) {
				calls++
				require.NotNil(t, request.ReaderOptions)
				require.NotEmpty(t, request.ReaderOptions.ForUpdate)
				require.Len(t, request.Providers, 1)
				want := 400
				if calls%2 == 0 {
					want = 1
				}
				if kind == "conversation" {
					q := request.Input.(*conversationmeta.Input)
					require.Len(t, q.IDs, want)
					require.True(t, q.Has.IDs)
					return &conversationmeta.Output{Data: []*conversationmeta.Conversation{{Id: fmt.Sprint(calls)}}}, nil
				}
				q := request.Input.(*runmeta.Input)
				require.Len(t, q.IDs, want)
				require.True(t, q.Has.IDs)
				return &runmeta.Output{Data: []*runmeta.Run{{Id: fmt.Sprint(calls)}}}, nil
			})
			for phase := 0; phase < 2; phase++ {
				if kind == "conversation" {
					q := &convread.ConversationInput{}
					q.SetIds(ids)
					rows, err := ReadCleanupConversations(context.Background(), invoker, q, true)
					require.NoError(t, err)
					require.Equal(t, fmt.Sprint(phase*2+1), rows[0].Id)
				} else {
					q := &runread.RunRowsInput{}
					q.SetIds(ids)
					rows, err := ReadCleanupRuns(context.Background(), invoker, q, true)
					require.NoError(t, err)
					require.Equal(t, fmt.Sprint(phase*2+1), rows[0].Id)
				}
			}
			require.Equal(t, 4, calls, "post-lock reads must execute again, not reuse phase metadata")
		})
	}
}

func TestCleanupMetadataStopsOnCancellationAndPartialFailure(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if cancelled {
			cancel()
		}
		calls := 0
		invoker := mutationTestInvoker(func(_ context.Context, request dexec.ComponentRequest) (any, error) {
			calls++
			if calls == 2 {
				return nil, fmt.Errorf("second read failed")
			}
			return &conversationmeta.Output{}, nil
		})
		q := &convread.ConversationInput{}
		var ids []string
		for i := 0; i < 801; i++ {
			ids = append(ids, fmt.Sprint(i))
		}
		q.SetIds(ids)
		_, err := ReadCleanupConversations(ctx, invoker, q, false)
		require.Error(t, err)
		if cancelled {
			require.ErrorIs(t, err, context.Canceled)
			require.Zero(t, calls)
		} else {
			require.ErrorContains(t, err, "second read failed")
			require.Equal(t, 2, calls)
		}
	}
}
