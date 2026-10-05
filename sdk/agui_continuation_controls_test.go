package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type controlLineageStore struct {
	aguistore.Store
	runs map[string]*aguistore.Run
}

func (s *controlLineageStore) GetRun(_ context.Context, principal, thread, id string) (*aguistore.Run, error) {
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
