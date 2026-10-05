package clienttool

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"testing"
)

func dependencyEdge(id, parentTurn, childTurn string) Dependency {
	return Dependency{ID: id, ParentCall: PendingCall{ID: "delegate-" + id, Name: "llm/agents:run", ToolMessageID: "message-" + id, ConversationID: "parent", TurnID: parentTurn}, ChildConversationID: "reused-child", ChildTurnID: childTurn, ResultAdapter: AgentRunResultV1}
}
func TestDependencyGraphIsolatesSiblingsReusedConversationsAndScopedFailures(t *testing.T) {
	session, err := NewSession([]llm.ToolDefinition{{Name: "frontend"}})
	require.NoError(t, err)
	first := dependencyEdge("first", "parent-one", "child-one")
	second := dependencyEdge("second", "parent-two", "child-two")
	require.NoError(t, session.RegisterDependency(first))
	require.NoError(t, session.RegisterDependency(second))
	require.NoError(t, session.MarkDependencyWaiting(first.ID))
	require.NoError(t, session.MarkDependencyWaiting(second.ID))
	for _, child := range []string{"child-one", "child-two"} {
		_, err := session.DeferForTurn("reused-child", child, "leaf-"+child, "frontend", nil, func() (PendingCall, error) {
			return PendingCall{ConversationID: "reused-child", TurnID: child, ToolMessageID: "tool-" + child}, nil
		})
		require.NoError(t, err)
	}
	require.Len(t, session.PendingForSubtree("parent", "parent-one"), 1)
	require.Equal(t, "leaf-child-one", session.PendingForSubtree("parent", "parent-one")[0].ID)
	require.Len(t, session.WaitingDependencies("parent", "parent-two"), 1)
	require.Equal(t, "second", session.WaitingDependencies("parent", "parent-two")[0].ID)
	_, err = session.DeferForTurn("reused-child", "child-one", "failed", "frontend", nil, func() (PendingCall, error) { return PendingCall{}, fmt.Errorf("child persistence failed") })
	require.Error(t, err)
	require.Error(t, session.ErrorForTurn("reused-child", "child-one"))
	require.NoError(t, session.ErrorForTurn("reused-child", "child-two"))
	require.NoError(t, session.ErrorForTurn("parent", "parent-two"))
	cycle := Dependency{ID: "cycle", ParentCall: PendingCall{ID: "cycle-op", Name: "llm/agents:run", ToolMessageID: "cycle-message", ConversationID: "reused-child", TurnID: "child-one"}, ChildConversationID: "parent", ChildTurnID: "parent-one", ResultAdapter: AgentRunResultV1}
	require.ErrorContains(t, session.RegisterDependency(cycle), "cyclic")
	collision := first
	collision.ID = "other-parent"
	collision.ParentCall.ID = "other-op"
	collision.ParentCall.TurnID = "parent-two"
	require.ErrorContains(t, session.RegisterDependency(collision), "another dependency")
}
func TestDependencyResultAdapterRejectsWaitingAndContainsOnlyPublicResult(t *testing.T) {
	dependency := dependencyEdge("first", "parent", "child")
	_, _, err := FormatDependencyResult(dependency, ChildResult{Status: "waiting_for_user", ConversationID: "reused-child", TurnID: "child"})
	require.ErrorContains(t, err, "not terminal")
	result, toolError, err := FormatDependencyResult(dependency, ChildResult{Status: "succeeded", Content: "answer", ConversationID: "reused-child", TurnID: "child"})
	require.NoError(t, err)
	require.Empty(t, toolError)
	require.JSONEq(t, `{"answer":"answer","status":"succeeded","conversationId":"reused-child","messageId":"child"}`, result)
}

func TestScopedPendingAllowsRepeatedNativeCallIDsAcrossTurns(t *testing.T) {
	session, err := NewSession([]llm.ToolDefinition{{Name: "frontend"}})
	require.NoError(t, err)
	writes := 0
	for _, turn := range []string{"one", "two"} {
		for attempt := 0; attempt < 2; attempt++ {
			_, err := session.DeferForTurn("conversation", turn, "same-native-id", "frontend", nil, func() (PendingCall, error) {
				writes++
				return PendingCall{ConversationID: "conversation", TurnID: turn, ToolMessageID: "message-" + turn}, nil
			})
			require.NoError(t, err)
		}
	}
	require.Equal(t, 2, writes)
	require.Len(t, session.Pending(), 2)
	require.Len(t, session.PendingForTurn("conversation", "one"), 1)
}
