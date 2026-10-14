package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/runtime/requestctx"
)

type deepOrdinaryRecoveryStore struct {
	aguistore.Store
	root *aguistore.Run
}

func (s deepOrdinaryRecoveryStore) GetRun(ctx context.Context, principal, thread, id string) (*aguistore.Run, error) {
	if id == "initial" {
		run, err := s.Store.GetRun(ctx, principal, thread, id)
		if err != nil {
			return nil, err
		}
		copy := *run
		copy.PriorRunID = "history-065"
		return &copy, nil
	}
	if strings.HasPrefix(id, "history-") {
		var index int
		if _, err := fmt.Sscanf(id, "history-%03d", &index); err != nil {
			return nil, err
		}
		copy := *s.root
		copy.RunID = id
		copy.ParentRunID = ""
		copy.ClientMessageID = "original-user"
		copy.PriorRunID = ""
		if index > 0 {
			copy.PriorRunID = fmt.Sprintf("history-%03d", index-1)
		}
		var input map[string]json.RawMessage
		_ = json.Unmarshal(copy.Input, &input)
		input["runId"] = rawAGUI(id)
		copy.Input = rawAGUI(input)
		return &copy, nil
	}
	return s.Store.GetRun(ctx, principal, thread, id)
}

func TestAGUIDetachedOriginCheckAfterAcceptedResultSupportsLongOrdinaryContinuation(t *testing.T) {
	c, _, failed, _ := failedContinuationFixture(t)
	ctx := recoveryContext()
	root, err := c.store.GetRun(ctx, "owner", "thread", "initial")
	require.NoError(t, err)
	store := deepOrdinaryRecoveryStore{Store: c.store, root: root}
	successor, err := prepareFailedAGUIContinuationRecovery(ctx, c, c, store, failed)
	require.NoError(t, err)
	err = runFailedAGUIContinuationRecovery(ctx, c, c, store, successor)
	t.Logf("accepted-result boundary completions=%d nativeResumes=%d queries=%d", c.completions.Load(), c.resumes.Load(), c.queries.Load())
	require.NoError(t, err, "valid ordinary ancestry must reach native Resume after accepted result persistence")
	require.EqualValues(t, 1, c.completions.Load())
	require.EqualValues(t, 1, c.resumes.Load())
	require.Zero(t, c.queries.Load())
	c.mu.Lock()
	require.Equal(t, []string{"original-call"}, c.results)
	require.True(t, c.controlsPreserved)
	c.mu.Unlock()
}

func detachedOriginLineage(count int) (*controlLineageStore, *aguistore.Run) {
	runs := map[string]*aguistore.Run{}
	for index := 0; index <= count; index++ {
		id := fmt.Sprintf("origin-%04d", index)
		run := &aguistore.Run{RunID: id, Principal: "owner", ThreadID: "wire", ConversationID: "native", TurnID: "turn", ClientMessageID: "original-user"}
		if index > 0 {
			run.PriorRunID = fmt.Sprintf("origin-%04d", index-1)
		}
		runs[id] = run
	}
	return &controlLineageStore{runs: runs}, runs[fmt.Sprintf("origin-%04d", count)]
}

func TestAGUIDetachedOriginLineageChecksCyclesScopeAndCancellationWithoutDepthCeiling(t *testing.T) {
	for _, count := range []int{65, 256, 1024} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			store, run := detachedOriginLineage(count)
			ctx := context.Background()
			bound, err := aguiDetachedResumeContext(ctx, store, run)
			require.NoError(t, err)
			require.Equal(t, ctx, bound)
			require.Equal(t, count, store.calls)
		})
	}
	for _, mode := range []string{"cycle", "principal", "wire", "native", "turn", "wrongID", "nil", "cancelBefore", "cancelDuring"} {
		t.Run(mode, func(t *testing.T) {
			store, run := detachedOriginLineage(2)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "cycle":
				store.runs["origin-0001"].PriorRunID = run.RunID
			case "principal":
				store.runs["origin-0001"].Principal = "foreign"
			case "wire":
				store.runs["origin-0001"].ThreadID = "foreign"
			case "native":
				store.runs["origin-0001"].ConversationID = "foreign"
			case "turn":
				store.runs["origin-0001"].TurnID = "foreign"
			case "wrongID":
				store.runs["origin-0001"].RunID = "foreign"
			case "nil":
				store.runs["origin-0001"] = nil
			case "cancelBefore":
				cancel()
			case "cancelDuring":
				store.onGet = func(context.Context, string) { cancel() }
			}
			bound, err := aguiDetachedResumeContext(ctx, store, run)
			require.Error(t, err)
			require.Nil(t, bound)
			if mode == "cancelBefore" {
				require.Zero(t, store.calls)
			}
			if mode == "cancelDuring" {
				require.Equal(t, 1, store.calls)
			}
		})
	}
}

type detachedOriginResolverStore struct {
	*controlLineageStore
	parentThread *aguistore.Thread
}

func (s detachedOriginResolverStore) GetThreadByConversationID(context.Context, string, string) (*aguistore.Thread, error) {
	return s.parentThread, nil
}

func TestAGUIDetachedOriginKeepsForkAndExactDetachedParentSemantics(t *testing.T) {
	base, tail := detachedOriginLineage(70)
	root := base.runs["origin-0000"]
	root.ParentRunID = "parent-run"
	ctx := context.Background()
	bound, err := aguiDetachedResumeContext(ctx, base, tail)
	require.NoError(t, err)
	require.Equal(t, ctx, bound)
	_, present := clienttool.DetachedContinuationFromContext(bound)
	require.False(t, present, "a normal fork does not mint detached authority")
	root.ClientMessageID = ""
	inv := requestctx.Invocation{ID: root.RunID, ConversationID: root.ConversationID, TurnID: root.TurnID, ParentConversationID: "parent-native", ParentTurnID: "parent-turn", ExecutionMode: "detach", Detached: true}
	root.Input = rawAGUI(map[string]any{"messages": []any{}, "forwardedProps": map[string]any{"agentlyInvocation": inv}})
	parent := &aguistore.Run{RunID: "parent-run", Principal: "owner", ThreadID: "parent-wire", ConversationID: "parent-native", TurnID: "parent-turn"}
	base.runs[parent.RunID] = parent
	store := detachedOriginResolverStore{controlLineageStore: base, parentThread: &aguistore.Thread{Principal: "owner", ThreadID: "parent-wire", ConversationID: "parent-native"}}
	bound, err = aguiDetachedResumeContext(ctx, store, tail)
	require.NoError(t, err)
	proof, present := clienttool.DetachedContinuationFromContext(bound)
	require.True(t, present)
	require.Equal(t, "parent-run", proof.ParentProtocolRunID)
	require.Equal(t, "native", proof.ChildConversationID)
	require.Equal(t, "turn", proof.ChildTurnID)
	for _, mode := range []string{"owner", "wire", "native", "turn", "id", "nil"} {
		t.Run(mode, func(t *testing.T) {
			copy := *parent
			switch mode {
			case "owner":
				copy.Principal = "foreign"
			case "wire":
				copy.ThreadID = "foreign"
			case "native":
				copy.ConversationID = "foreign"
			case "turn":
				copy.TurnID = "foreign"
			case "id":
				copy.RunID = "foreign"
			}
			base.runs[parent.RunID] = &copy
			if mode == "nil" {
				base.runs[parent.RunID] = nil
			}
			_, err := aguiDetachedResumeContext(ctx, store, tail)
			require.Error(t, err)
		})
	}
}
