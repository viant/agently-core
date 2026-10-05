package sdk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/mcpapps"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

func TestMCPAppsJournalAliasesArePrincipalAndActivityScoped(t *testing.T) {
	client, _ := newDurableAGUIServer(t)
	ctx := context.Background()
	run, _, err := client.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "original", RunID: "app-run", Input: json.RawMessage(`{"threadId":"original","runId":"app-run","messages":[]}`)})
	require.NoError(t, err)
	var registered []json.RawMessage
	var aliases []string
	for i, uri := range []string{"ui://same/a", "ui://same/b"} {
		activity, e := MCPAppsActivity(uri, AGUIMCPAppBinding{ServerID: "same", ServerHash: "hash", ResourceURI: uri}, json.RawMessage(`{"content":[]}`), nil)
		require.NoError(t, e)
		activity, e = registerMCPAppActivity(activity, run, int64(i+2))
		require.NoError(t, e)
		var event struct {
			Content struct {
				ServerID string `json:"serverId"`
			} `json:"content"`
		}
		require.NoError(t, json.Unmarshal(activity, &event))
		aliases = append(aliases, event.Content.ServerID)
		registered = append(registered, activity)
	}
	registered = append([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "original", "runId": "app-run"})}, registered...)
	messages := []any{map[string]any{"id": "ordinary-user", "role": "user", "content": "ordinary string"}, map[string]any{"id": "ordinary-assistant", "role": "assistant", "content": "ordinary response"}}
	for _, activity := range registered[1:] {
		var event map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(activity, &event))
		messages = append(messages, map[string]any{"id": event["messageId"], "role": "activity", "activityType": "mcp-apps", "content": event["content"]})
	}
	thread, err := client.store.GetThread(ctx, "owner", "original")
	require.NoError(t, err)
	_, err = client.store.Append(ctx, "owner", "original", "app-run", run.Revision, registered, &aguistore.Change{Messages: rawAGUI(messages), ExpectedThreadRevision: thread.Revision})
	require.NoError(t, err)
	require.NotEqual(t, aliases[0], aliases[1])
	for i, alias := range aliases {
		app, e := ResolveAGUIMCPApp(ctx, client.store, "owner", alias, "hash")
		require.NoError(t, e)
		require.Equal(t, []string{"ui://same/a", "ui://same/b"}[i], app.ResourceURI)
		require.Equal(t, "same", app.ServerID)
		_, e = ResolveAGUIMCPApp(ctx, client.store, "foreign", alias, "hash")
		require.Error(t, e)
		_, e = ResolveAGUIMCPApp(ctx, client.store, "owner", alias, "changed")
		require.Error(t, e)
	}
	_, err = ResolveAGUIMCPApp(ctx, client.store, "owner", "same", "hash")
	require.Error(t, err)
	// A valid encoded location pointing at a non-app event is never authority.
	var identity mcpAppJournalIdentity
	decodedAlias, _ := base64.RawURLEncoding.DecodeString(aliases[0][len("agui-app:"):])
	require.NoError(t, json.Unmarshal(decodedAlias, &identity))
	identity.Sequence = 1
	encoded, _ := json.Marshal(identity)
	_, err = ResolveAGUIMCPApp(ctx, client.store, "owner", "agui-app:"+base64.RawURLEncoding.EncodeToString(encoded), "hash")
	require.Error(t, err)
	latest, err := client.store.GetRun(ctx, "owner", "original", "app-run")
	require.NoError(t, err)
	thread, err = client.store.GetThread(ctx, "owner", "original")
	require.NoError(t, err)
	latest, err = client.store.Append(ctx, "owner", "original", "app-run", latest.Revision, nil, &aguistore.Change{Messages: json.RawMessage(`[]`), ExpectedThreadRevision: thread.Revision})
	require.NoError(t, err)
	_, err = ResolveAGUIMCPApp(ctx, client.store, "owner", aliases[0], "hash")
	require.Error(t, err)
	thread, err = client.store.GetThread(ctx, "owner", "original")
	require.NoError(t, err)
	latest, err = client.store.Append(ctx, "owner", "original", "app-run", latest.Revision, nil, &aguistore.Change{Messages: rawAGUI(messages), ExpectedThreadRevision: thread.Revision})
	require.NoError(t, err)
	_, err = ResolveAGUIMCPApp(ctx, client.store, "owner", aliases[0], "hash")
	require.NoError(t, err)
	_, err = client.store.Append(ctx, "owner", "original", "app-run", latest.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_ERROR", "message": "revoked"})}, nil)
	require.NoError(t, err)
	_, err = ResolveAGUIMCPApp(ctx, client.store, "owner", aliases[0], "hash")
	require.Error(t, err)
}

func TestMCPAppsDurableProxyHostResultNeverEntersProjection(t *testing.T) {
	client, _ := newDurableAGUIServer(t)
	ctx := context.Background()
	input := &agui.RunAgentInput{ThreadID: "isolated", RunID: "isolated"}
	run, _, err := client.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: input.ThreadID, RunID: input.RunID, TurnID: "proxy-turn", Input: rawAGUI(map[string]any{"threadId": "isolated", "runId": "isolated", "messages": []any{}})})
	require.NoError(t, err)
	binding := AGUIMCPAppsBindings{App: AGUIMCPAppBinding{AppInstanceID: "issued", ThreadID: "original", ServerID: "native", ServerHash: "hash", ResourceURI: "ui://app"}, Authorize: func(context.Context, AGUIMCPAppBinding) error { return nil }, AuthorizeTool: func(context.Context, AGUIMCPAppBinding, string) error { return nil }, ToolCaller: func(ctx context.Context, in *MCPUIToolCallInput) (*MCPUIToolCallOutput, error) {
		require.Equal(t, "original", in.ConversationID)
		require.Equal(t, "proxy-turn", mcpapps.TurnID(ctx))
		var value mcpschema.CallToolResult
		require.NoError(t, json.Unmarshal([]byte(`{"resultType":"complete","content":[{"type":"text","text":"safe"}],"structuredContent":{"n":1},"_meta":{"token":"private"}}`), &value))
		return &MCPUIToolCallOutput{Status: "completed"}, mcpapps.Record(ctx, "native", "view", "op", &value)
	}}
	proxy := &MCPAppsProxyRequest{ServerID: "native", ServerHash: "hash", Method: "tools/call", Params: map[string]json.RawMessage{"name": json.RawMessage(`"view"`)}}
	require.NoError(t, runAGUIMCPProxyWorker(WithAGUIMCPAppsBindings(ctx, binding), client, client.store, run, input, proxy, nil))
	latest, err := client.store.GetRun(ctx, "owner", "isolated", "isolated")
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusFinished, latest.Status)
	require.Contains(t, string(latest.Pending), "private")
	events, err := client.store.Replay(ctx, "owner", "isolated", "isolated", 0, 10)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Contains(t, string(events[1].Event), `"_meta"`)
	thread, err := client.store.GetThread(ctx, "owner", "isolated")
	require.NoError(t, err)
	require.NotContains(t, string(thread.State), "private")
	require.NotContains(t, string(thread.Messages), "private")
}

func TestMCPAppsRestartUsesReceiptAndNeverRepeatsUncertainDispatch(t *testing.T) {
	for _, receipt := range []bool{false, true} {
		t.Run(map[bool]string{false: "uncertain", true: "receipt"}[receipt], func(t *testing.T) {
			client, _ := newDurableAGUIServer(t)
			ctx := context.Background()
			input := &agui.RunAgentInput{ThreadID: "proxy", RunID: "proxy"}
			record, _, err := client.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "proxy", RunID: "proxy", Input: rawAGUI(map[string]any{"threadId": "proxy", "runId": "proxy", "messages": []any{}})})
			require.NoError(t, err)
			record, err = client.store.Claim(ctx, "owner", "proxy", "proxy", record.Revision, "crashed", time.Second)
			require.NoError(t, err)
			pending := aguiMCPProxyPending{}
			if receipt {
				pending.HostResult = json.RawMessage(`{"resultType":"complete","content":[],"_meta":{"restart":"private"}}`)
				pending.NativeOperationID = "native-op"
			}
			record, err = client.store.Append(ctx, "owner", "proxy", "proxy", record.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "proxy", "runId": "proxy"})}, &aguistore.Change{LeaseOwner: "crashed", Pending: rawAGUI(pending)})
			require.NoError(t, err)
			time.Sleep(1100 * time.Millisecond)
			calls := 0
			binding := AGUIMCPAppsBindings{ToolCaller: func(context.Context, *MCPUIToolCallInput) (*MCPUIToolCallOutput, error) { calls++; return nil, nil }}
			require.NoError(t, runAGUIMCPProxyWorker(WithAGUIMCPAppsBindings(ctx, binding), client, client.store, record, input, &MCPAppsProxyRequest{Method: "tools/call"}, nil))
			require.Zero(t, calls)
			latest, err := client.store.GetRun(ctx, "owner", "proxy", "proxy")
			require.NoError(t, err)
			if receipt {
				require.Equal(t, aguistore.StatusFinished, latest.Status)
			} else {
				require.Equal(t, aguistore.StatusError, latest.Status)
			}
		})
	}
}

func TestMCPAppsHistorySnapshotPreservesIssuedAlias(t *testing.T) {
	app := AGUIMCPAppBinding{ThreadID: "original", ServerID: "native", ServerHash: "hash", ResourceURI: "ui://app"}
	raw, err := MCPAppsActivity("activity", app, json.RawMessage(`{"content":[]}`), nil)
	require.NoError(t, err)
	registered, err := registerMCPAppActivity(raw, &aguistore.Run{ThreadID: "original", ConversationID: "original", RunID: "run"}, 1)
	require.NoError(t, err)
	var event map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(registered, &event))
	current := aguistate.Object{"id": "activity", "role": "activity", "activityType": "mcp-apps"}
	var content map[string]any
	require.NoError(t, json.Unmarshal(event["content"], &content))
	current["content"] = content
	var original map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &original))
	snapshot := rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": []any{map[string]any{"id": "activity", "role": "activity", "activityType": "mcp-apps", "content": original["content"]}}})
	restored, err := preserveMCPAppSnapshotAliases(snapshot, []aguistate.Object{current})
	require.NoError(t, err)
	require.Contains(t, string(restored), "agui-app:")
	require.NotContains(t, string(restored), `"serverId":"native"`)
}
