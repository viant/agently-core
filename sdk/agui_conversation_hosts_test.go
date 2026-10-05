package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/runtime/aguistate"
)

func TestAGUIBootstrapHostActivitiesRequireJournalAndCurrentAuthorization(t *testing.T) {
	client, _ := newDurableAGUIServer(t)
	ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "owner"})
	run, _, err := client.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "original", RunID: "app-run", Input: json.RawMessage(`{"threadId":"original","runId":"app-run","messages":[]}`)})
	require.NoError(t, err)
	activity, err := MCPAppsActivity("app", AGUIMCPAppBinding{ServerID: "server", ServerHash: "hash", ResourceURI: "ui://app"}, json.RawMessage(`{"content":[],"_meta":{"private":"host-only"}}`), nil)
	require.NoError(t, err)
	activity, err = registerMCPAppActivity(activity, run, 2)
	require.NoError(t, err)
	var event map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(activity, &event))
	var message aguistate.Object
	require.NoError(t, json.Unmarshal(rawAGUI(map[string]any{"id": event["messageId"], "role": "activity", "activityType": "mcp-apps", "content": event["content"]}), &message))
	thread, err := client.store.GetThread(ctx, "owner", "original")
	require.NoError(t, err)
	_, err = client.store.Append(ctx, "owner", "original", "app-run", run.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "original", "runId": "app-run"}), activity}, &aguistore.Change{Messages: rawAGUI([]aguistate.Object{message}), ExpectedThreadRevision: thread.Revision})
	require.NoError(t, err)
	command := &aguistore.Run{Principal: "owner", ThreadID: "original"}
	authorized := 0
	host := &AGUIMCPAppsHost{Authorize: func(context.Context, AGUIMCPAppBinding) error { authorized++; return nil }}
	hostCtx := WithAGUIWorkspaceBindings(ctx, AGUIWorkspaceBindings{MCPApps: host})
	got, missing := aguiBootstrapHostActivities(hostCtx, client, client.store, command, []aguistate.Object{message})
	require.Len(t, got, 1)
	require.Empty(t, missing)
	require.Contains(t, string(rawAGUI(got)), "host-only")
	require.Equal(t, 1, authorized)
	for _, test := range []struct {
		name    string
		context context.Context
		record  *aguistore.Run
	}{
		{"no-host", ctx, command},
		{"foreign-principal", WithAGUIWorkspaceBindings(iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "foreign"}), AGUIWorkspaceBindings{MCPApps: host}), command},
		{"foreign-thread", hostCtx, &aguistore.Run{Principal: "owner", ThreadID: "another"}},
		{"revoked", WithAGUIWorkspaceBindings(ctx, AGUIWorkspaceBindings{MCPApps: &AGUIMCPAppsHost{Authorize: func(context.Context, AGUIMCPAppBinding) error { return fmt.Errorf("revoked") }}}), command},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, missing := aguiBootstrapHostActivities(test.context, client, client.store, test.record, []aguistate.Object{message})
			require.Empty(t, got)
			require.Equal(t, []string{message["id"].(string)}, missing)
		})
	}
	transcript := &ConversationStateResponse{Conversation: &ConversationState{ConversationID: "original"}}
	messages, quality := aguiBootstrapMessages(ctx, client, transcript, []aguistate.Object{message})
	require.Empty(t, messages)
	require.False(t, quality.Lossless)
}
