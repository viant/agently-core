package agents

import (
	"context"
	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/runtime/requestctx"
	agentsvc "github.com/viant/agently-core/service/agent"
	"testing"
)

type invocationRuntime struct {
	status string
	calls  int
	before bool
}

func (r *invocationRuntime) Finder() agentmdl.Finder { return nil }
func (r *invocationRuntime) Query(ctx context.Context, input *agentsvc.QueryInput, output *agentsvc.QueryOutput) error {
	if !r.before {
		panic("child Query ran before observation")
	}
	r.calls++
	if requestctx.InvocationIDFromContext(ctx) != input.MessageID {
		panic("child invocation identity missing")
	}
	output.ExecutionStatus = r.status
	output.Content = "answer"
	output.ConversationID = input.ConversationID
	return nil
}
func TestInvocationSourceRegistersBeforeQueryAndKeepsQueuedReturnNonterminal(t *testing.T) {
	for _, status := range []string{"succeeded", "queued", "waiting_for_user"} {
		t.Run(status, func(t *testing.T) {
			runtime := &invocationRuntime{status: status}
			service := &Service{agent: runtime}
			var starts []requestctx.Invocation
			var returns []requestctx.InvocationResult
			observer := requestctx.InvocationObserverFuncs{Before: func(_ context.Context, inv requestctx.Invocation) error {
				starts = append(starts, inv)
				runtime.before = true
				return nil
			}, Returned: func(_ context.Context, result requestctx.InvocationResult) error {
				returns = append(returns, result)
				return nil
			}}
			ctx := requestctx.WithInvocationObserver(context.Background(), observer)
			parent := requestctx.TurnMeta{ConversationID: "parent", TurnID: "parent-turn"}
			for _, turn := range []string{"one", "two"} {
				input := &agentsvc.QueryInput{AgentID: "child-agent", ConversationID: "reused-child", MessageID: turn}
				result := service.executeChildRun(ctx, input, &agentsvc.QueryOutput{}, linkedRun{parent: parent, childConversationID: "reused-child"})
				require.NoError(t, result.err)
			}
			require.Len(t, starts, 2)
			require.Equal(t, "one", starts[0].ID)
			require.Equal(t, "two", starts[1].ID)
			require.Equal(t, starts[0].ConversationID, starts[1].ConversationID)
			require.Len(t, returns, 2)
			require.Equal(t, status, returns[0].NativeStatus)
			require.Equal(t, 2, runtime.calls)
		})
	}
}
