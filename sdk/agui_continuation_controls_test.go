package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type controlLineageStore struct {
	aguistore.Store
	runs  map[string]*aguistore.Run
	calls int
	onGet func(context.Context, string)
}

func (s *controlLineageStore) GetRun(ctx context.Context, principal, thread, id string) (*aguistore.Run, error) {
	s.calls++
	if s.onGet != nil {
		s.onGet(ctx, id)
	}
	record := s.runs[id]
	if record == nil {
		return nil, aguistore.ErrNotFound
	}
	return record, nil
}
func TestAGUIContinuationRestoresImmutableControlsAcrossGenerations(t *testing.T) {
	original := &aguistore.Run{Principal: "owner", ThreadID: "thread", ConversationID: "thread", TurnID: "native-turn", RunID: "initial", Input: json.RawMessage(`{"threadId":"thread","runId":"initial","messages":[{"id":"user","role":"user","content":"model prompt"}],"forwardedProps":{"agently":{"version":"1","operation":"chat","payload":{"agentId":"original-agent","model":"original-model","backendTools":["report/read"],"toolBundles":["reporting"],"reasoningEffort":"high","autoSelectTools":false,"autoSummarize":false,"disableChains":true,"allowedChains":["review"],"resourceURIs":["agently://context"],"toolCallExposure":"conversation","displayQuery":"Friendly query","context":{"agui":{"forged":true},"workspace":{"filter":9007199254740993}},"attachments":[{"name":"report.csv","uri":"agently://files/ref","mime":"text/csv","stagingFolder":"owned"}]}}}}`)}
	first := &aguistore.Run{Principal: "owner", ThreadID: "thread", ConversationID: "thread", TurnID: "native-turn", RunID: "first", PriorRunID: "initial"}
	second := &aguistore.Run{Principal: "owner", ThreadID: "thread", ConversationID: "thread", TurnID: "native-turn", RunID: "second", PriorRunID: "first"}
	store := &controlLineageStore{runs: map[string]*aguistore.Run{"initial": original, "first": first}}
	reserved := map[string]any{"state": json.RawMessage(`{"counter":9}`), "context": []any{"current"}}
	query := &agentsvc.QueryInput{ConversationID: "thread", MessageID: "native-turn", UserId: "owner", AgentID: "permitted-agent", ModelOverride: "permitted-model", Context: map[string]any{"agui": reserved}}
	require.NoError(t, restoreAGUIContinuationControls(context.Background(), store, second, query))
	require.Equal(t, []string{"report/read"}, query.ToolsAllowed)
	require.Equal(t, []string{"reporting"}, query.ToolBundles)
	require.NotNil(t, query.ReasoningEffort)
	require.Equal(t, "high", *query.ReasoningEffort)
	require.NotNil(t, query.AutoSelectTools)
	require.False(t, *query.AutoSelectTools)
	require.False(t, *query.AutoSummarize)
	require.True(t, query.DisableChains)
	require.Equal(t, []string{"review"}, query.AllowedChains)
	require.Equal(t, []string{"agently://context"}, query.ResourceURIs)
	require.Equal(t, "Friendly query", query.DisplayQuery)
	require.Len(t, query.Attachments, 1)
	require.Equal(t, "agently://files/ref", query.Attachments[0].URI)
	require.Equal(t, "permitted-model", query.ModelOverride)
	require.Equal(t, "permitted-agent", query.AgentID)
	require.Equal(t, "owner", query.UserId)
	query.Context["agui"].(map[string]any)["identity-check"] = true
	require.Equal(t, true, reserved["identity-check"])
	require.Equal(t, json.Number("9007199254740993"), query.Context["workspace"].(map[string]any)["filter"])
	replacement := &agui.Extension{Payload: &agui.ExecutionPayload{BackendTools: []string{"forbidden"}}}
	require.ErrorContains(t, applyAGUIExecution(&agentsvc.QueryInput{}, replacement, true), "cannot replace")
}
func TestAGUIContinuationRejectsBrokenOrCrossScopeLineage(t *testing.T) {
	valid := &aguistore.Run{Principal: "owner", ThreadID: "thread", ConversationID: "thread", TurnID: "turn", RunID: "initial", Input: json.RawMessage(`{"threadId":"thread","runId":"initial","messages":[]}`)}
	tests := []struct {
		name     string
		ancestor *aguistore.Run
	}{
		{"missing", nil}, {"principal", &aguistore.Run{RunID: "initial", ThreadID: "thread", ConversationID: "thread", Principal: "foreign", TurnID: "turn"}},
		{"thread", &aguistore.Run{RunID: "initial", ThreadID: "foreign", ConversationID: "foreign", Principal: "owner", TurnID: "turn"}},
		{"turn", &aguistore.Run{RunID: "initial", ThreadID: "thread", ConversationID: "thread", Principal: "owner", TurnID: "foreign"}},
		{"conversation", &aguistore.Run{RunID: "initial", ThreadID: "thread", ConversationID: "foreign", Principal: "owner", TurnID: "turn"}},
		{"run", &aguistore.Run{RunID: "foreign", ThreadID: "thread", ConversationID: "thread", Principal: "owner", TurnID: "turn"}},
		{"cycle", &aguistore.Run{RunID: "initial", ThreadID: "thread", ConversationID: "thread", Principal: "owner", TurnID: "turn", PriorRunID: "current"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &controlLineageStore{runs: map[string]*aguistore.Run{"initial": test.ancestor}}
			record := &aguistore.Run{RunID: "current", ThreadID: "thread", ConversationID: "thread", Principal: "owner", TurnID: "turn", PriorRunID: "initial"}
			query := &agentsvc.QueryInput{ConversationID: "thread", MessageID: "turn", UserId: "owner"}
			err := restoreAGUIContinuationControls(context.Background(), store, record, query)
			require.Error(t, err)
			if test.ancestor == nil {
				require.True(t, errors.Is(err, aguistore.ErrNotFound))
			}
		})
	}
	store := &controlLineageStore{runs: map[string]*aguistore.Run{"initial": valid}}
	query := &agentsvc.QueryInput{ConversationID: "other", MessageID: "turn", UserId: "owner"}
	require.Error(t, restoreAGUIContinuationControls(context.Background(), store, &aguistore.Run{RunID: "current", ThreadID: "thread", ConversationID: "thread", Principal: "owner", TurnID: "turn", PriorRunID: "initial"}, query))
}

func continuationControlChain(length int) (*controlLineageStore, *aguistore.Run, *agentsvc.QueryInput) {
	store := &controlLineageStore{runs: map[string]*aguistore.Run{}}
	var current *aguistore.Run
	for i := 0; i <= length; i++ {
		current = &aguistore.Run{Principal: "owner", ThreadID: "thread", ConversationID: "conversation", TurnID: "turn", RunID: fmt.Sprintf("run-%d", i)}
		if i == 0 {
			current.Input = json.RawMessage(`{"threadId":"thread","runId":"run-0","messages":[],"forwardedProps":{"agently":{"version":"1","operation":"chat","payload":{"backendTools":["report/read"],"reasoningEffort":"high","disableChains":true,"allowedChains":["review"]}}}}`)
		} else {
			current.PriorRunID = fmt.Sprintf("run-%d", i-1)
		}
		store.runs[current.RunID] = current
	}
	query := &agentsvc.QueryInput{ConversationID: "conversation", MessageID: "turn", UserId: "owner"}
	return store, current, query
}

func TestAGUIContinuationRestoresControlsWithoutAncestorDepthCeiling(t *testing.T) {
	for _, length := range []int{33, 64, 65, 128, 1024} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			store, record, query := continuationControlChain(length)
			require.NoError(t, restoreAGUIContinuationControls(context.Background(), store, record, query))
			require.Equal(t, length, store.calls)
			require.Equal(t, []string{"report/read"}, query.ToolsAllowed)
			require.Equal(t, "high", *query.ReasoningEffort)
			require.True(t, query.DisableChains)
			require.Equal(t, []string{"review"}, query.AllowedChains)
		})
	}
}

func TestAGUIContinuationRejectsCyclesBeyondFormerDepthCeiling(t *testing.T) {
	for _, length := range []int{0, 1, 128} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			store, record, query := continuationControlChain(length)
			store.runs["run-0"].PriorRunID = record.RunID
			require.ErrorContains(t, restoreAGUIContinuationControls(context.Background(), store, record, query), "lineage is cyclic")
			require.Equal(t, length, store.calls)
			require.Empty(t, query.ToolsAllowed)
		})
	}
}

func TestAGUIContinuationStopsOnCancellationEvenIfStoreIgnoresIt(t *testing.T) {
	for _, cancelAfter := range []int{0, 17, 128} {
		t.Run(fmt.Sprint(cancelAfter), func(t *testing.T) {
			store, record, query := continuationControlChain(128)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelAfter == 0 {
				cancel()
			} else {
				store.onGet = func(context.Context, string) {
					if store.calls == cancelAfter {
						cancel()
					}
				}
			}
			require.ErrorIs(t, restoreAGUIContinuationControls(ctx, store, record, query), context.Canceled)
			require.Equal(t, cancelAfter, store.calls)
			require.Empty(t, query.ToolsAllowed)
			require.Nil(t, query.ReasoningEffort)
		})
	}
}

func TestAGUIContinuationRestoresDetachedChildOwnRoot(t *testing.T) {
	store, record, query := continuationControlChain(128)
	childRoot := store.runs["run-0"]
	childRoot.ParentRunID = "parent-execution"
	store.runs[childRoot.ParentRunID] = &aguistore.Run{RunID: childRoot.ParentRunID, ThreadID: "parent-thread", ConversationID: "parent-conversation", TurnID: "parent-turn", Principal: "owner", PriorRunID: "parent-execution"}
	childRoot.Input = json.RawMessage(`{"threadId":"thread","runId":"run-0","parentRunId":"parent-execution","messages":[],"forwardedProps":{"agently":{"version":"1","operation":"chat","payload":{"backendTools":["child/read"],"reasoningEffort":"low"}}}}`)
	require.NoError(t, restoreAGUIContinuationControls(context.Background(), store, record, query))
	require.Equal(t, 128, store.calls)
	require.Equal(t, []string{"child/read"}, query.ToolsAllowed)
	require.Equal(t, "low", *query.ReasoningEffort)
}
