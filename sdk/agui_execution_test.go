package sdk

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/agui/extensions"
	agentsvc "github.com/viant/agently-core/service/agent"
)

func TestAGUIExecutionPreservesWebControlsAndBFFIdentity(t *testing.T) {
	client, server := newDurableAGUIServer(t)
	var observed *agentsvc.QueryInput
	client.query = func(_ context.Context, query *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		observed = query
		return &agentsvc.QueryOutput{Content: "ok"}, nil
	}
	body := `{"threadId":"thread","runId":"web-controls","messages":[{"id":"web-user-message","role":"user","content":"internal task with selected report"}],"forwardedProps":{"agently":{"version":"1","operation":"chat","payload":{"agentId":"steward","model":"chosen-model","backendTools":["report/read"],"toolBundles":["reporting"],"reasoningEffort":"high","autoSelectTools":false,"autoSummarize":false,"disableChains":true,"allowedChains":["reviewer"],"resourceURIs":["agently://files/context-ref"],"toolCallExposure":"conversation","displayQuery":"Show this report","context":{"userId":"not-authority","agui":{"forged":true},"ui":{"clientId":"browser"},"workspace":{"windowId":"report-1","filterId":9007199254740993}},"attachments":[{"name":"report.csv","uri":"agently://files/approved-ref","mime":"text/csv","stagingFolder":"owned-staging"}]}}}}`
	status, wire := durablePost(t, server, body, nil)
	require.Equal(t, 200, status, wire)
	require.NotNil(t, observed)
	require.Equal(t, "owner", observed.UserId)
	require.Equal(t, "thread", observed.ConversationID)
	require.NotEqual(t, "web-user-message", observed.MessageID, "native turn identity stays server-owned")
	require.Equal(t, "internal task with selected report", observed.Query)
	require.Equal(t, "Show this report", observed.DisplayQuery)
	require.Equal(t, "steward", observed.AgentID)
	require.Equal(t, "chosen-model", observed.ModelOverride)
	require.Equal(t, []string{"report/read"}, observed.ToolsAllowed)
	require.Equal(t, []string{"reporting"}, observed.ToolBundles)
	require.Equal(t, "high", *observed.ReasoningEffort)
	require.NotNil(t, observed.AutoSelectTools)
	require.False(t, *observed.AutoSelectTools)
	require.NotNil(t, observed.AutoSummarize)
	require.False(t, *observed.AutoSummarize)
	require.True(t, observed.DisableChains)
	require.Equal(t, []string{"reviewer"}, observed.AllowedChains)
	require.Equal(t, []string{"agently://files/context-ref"}, observed.ResourceURIs)
	require.Equal(t, "conversation", string(*observed.ToolCallExposure))
	require.Equal(t, json.Number("9007199254740993"), observed.Context["workspace"].(map[string]any)["filterId"])
	require.NotContains(t, observed.Context["agui"], "forged")
	require.Equal(t, "browser", observed.Context["ui"].(map[string]any)["clientId"])
	require.Len(t, observed.Attachments, 1)
	require.Equal(t, "agently://files/approved-ref", observed.Attachments[0].URI)
	require.Equal(t, "text/csv", observed.Attachments[0].Mime)
	require.Equal(t, "owned-staging", observed.Attachments[0].StagingFolder)
	status, replay := durablePost(t, server, body, nil)
	require.Equal(t, 200, status, replay)
	require.Equal(t, wire, replay)
	require.EqualValues(t, 1, client.queries.Load())
	status, _ = durablePost(t, server, strings.Replace(body, `"high"`, `"low"`, 1), nil)
	require.Equal(t, 409, status, "same run identity cannot silently change accepted execution controls")
}

func TestAGUIExecutionRejectsIdentityOverridesAndMalformedControlsBeforeQuery(t *testing.T) {
	client, server := newDurableAGUIServer(t)
	for _, payload := range []string{
		`{"userId":"another-user"}`, `{"conversationId":"another-thread"}`, `{"history":[]}`,
		`{"backendTools":"all"}`, `{"reasoningEffort":true}`, `{"context":null}`,
		`{"attachments":[{"uri":"x","data":"not-a-file-reference"}]}`,
	} {
		body := `{"threadId":"thread","runId":"invalid-controls","messages":[{"id":"u","role":"user","content":"hello"}],"forwardedProps":{"agently":{"version":"1","operation":"chat","payload":` + payload + `}}}`
		status, _ := durablePost(t, server, body, nil)
		require.Equal(t, 400, status, payload)
	}
	require.Zero(t, client.queries.Load())
}

func TestAGUIResumeCannotReplaceOriginalNativeExecutionControls(t *testing.T) {
	input := &agui.RunAgentInput{ThreadID: "thread", RunID: "resume", Messages: []agui.Message{}}
	_, err := queryForAGUI(input, "owner", "native-turn", &agui.Extension{Version: "1", Operation: "chat", Payload: &agui.ExecutionPayload{BackendTools: []string{"new-tool"}}}, &aguistore.Run{})
	require.ErrorContains(t, err, "resume cannot replace")
	query, err := queryForAGUI(input, "owner", "native-turn", nil, &aguistore.Run{})
	require.NoError(t, err)
	require.Equal(t, "owner", query.UserId)
}

func TestAGUIExecutionEnvelopePreservesOptionalZeroValues(t *testing.T) {
	require.NoError(t, extensions.ValidateExecutionEnvelope([]byte(`{"version":"1","operation":"chat","payload":{"displayQuery":"","backendTools":[],"toolBundles":[],"context":{"zero":0,"false":false}}}`)))
	require.NoError(t, extensions.ValidateExecutionEnvelope([]byte(`{"version":"1","operation":"capabilities"}`)))
	require.Error(t, extensions.ValidateExecutionEnvelope([]byte(`{"version":"2","operation":"chat"}`)))
}

func TestAGUIWebSubmissionKeepsDurableStateInsteadOfDefaultEmptyClientState(t *testing.T) {
	client, server := newDurableAGUIServer(t)
	var seen []json.RawMessage
	client.query = func(_ context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		state := input.Context["agui"].(map[string]any)["state"].(json.RawMessage)
		seen = append(seen, append(json.RawMessage(nil), state...))
		return &agentsvc.QueryOutput{Content: "ok"}, nil
	}
	requests := []string{
		`{"threadId":"thread","runId":"first","messages":[{"id":"u1","role":"user","content":"one"}],"state":{"counter":5}}`,
		`{"threadId":"thread","runId":"second","messages":[{"id":"u2","role":"user","content":"two"}],"state":{},"forwardedProps":{"agently":{"version":"1","operation":"chat","payload":{"useServerState":true}}}}`,
		`{"threadId":"thread","runId":"third","messages":[{"id":"u3","role":"user","content":"three"}],"state":{"counter":9},"forwardedProps":{"agently":{"version":"1","operation":"chat","payload":{"useServerState":false}}}}`,
	}
	for _, body := range requests {
		status, wire := durablePost(t, server, body, nil)
		require.Equal(t, 200, status, wire)
		assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(wire)), "RUN_FINISHED")
	}
	require.Len(t, seen, 3)
	require.JSONEq(t, `{"counter":5}`, string(seen[0]))
	require.JSONEq(t, `{"counter":5}`, string(seen[1]))
	require.JSONEq(t, `{"counter":9}`, string(seen[2]))
}
