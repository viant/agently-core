package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	exportrequestmodel "github.com/viant/agently-core/model/exportrequest"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/mcpapps"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

func TestMCPAppsChildJournalRequiresTrustedOriginalInvocation(t *testing.T) {
	client, _ := newDurableAGUIServer(t)
	ctx := context.Background()
	run, _, err := client.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "root", RunID: "root-run", TurnID: "root-native-turn", Input: rawAGUI(map[string]any{"threadId": "root", "runId": "root-run", "messages": []any{}})})
	require.NoError(t, err)
	run, err = client.store.Claim(ctx, "owner", "root", "root-run", run.Revision, "observer", time.Minute)
	require.NoError(t, err)
	thread, err := client.store.GetThread(ctx, "owner", "root")
	require.NoError(t, err)
	projection, err := aguistate.New(thread.State, thread.Messages)
	require.NoError(t, err)
	writer := &aguiJournalWriter{ctx: ctx, store: client.store, run: run, projection: projection, leaseOwner: "observer", messageBaseline: thread.Messages}
	translator := agui.NewTranslator("root", "root-run")
	require.NoError(t, translator.SetNativeIdentity("root-native-turn"))
	require.NoError(t, writer.write(encodeAGUIEvents(translator.Start()), nil))
	inv := requestctx.Invocation{ID: "real-child-invocation", Name: "child", ConversationID: "original-child", TurnID: "child-native-turn", ParentConversationID: "root", ParentTurnID: "root-native-turn", ParentToolCallID: "native-parent-op"}
	require.NoError(t, writer.write(encodeAGUIEvents(translator.RegisterInvocation(inv)), nil))
	binding := AGUIMCPAppBinding{ThreadID: inv.ConversationID, NativeTurnID: inv.TurnID, InvocationID: inv.ID, ServerID: "same-server", ServerHash: "hash", ResourceURI: "ui://child/app"}
	raw, err := MCPAppsActivity("child-app", binding, json.RawMessage(`{"resultType":"complete","content":[],"_meta":{"child":"private"}}`), nil)
	require.NoError(t, err)
	translated := translator.TranslateSubagent(inv, &streaming.Event{Type: streaming.EventTypeProtocol, ConversationID: inv.ConversationID, TurnID: inv.TurnID, ProtocolEvent: raw})
	require.NoError(t, writer.write(encodeAGUIEvents(translated), nil))
	var current []struct {
		ID      string `json:"id"`
		Content struct {
			Binding AGUIMCPAppBinding `json:"_agentlyApp"`
		} `json:"content"`
	}
	thread, err = client.store.GetThread(ctx, "owner", "root")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(thread.Messages, &current))
	alias := ""
	for _, message := range current {
		if message.ID == "child-app" {
			alias = message.Content.Binding.PublicServerID
		}
	}
	require.NotEmpty(t, alias)
	resolved, err := ResolveAGUIMCPApp(ctx, client.store, "owner", alias, "hash")
	require.NoError(t, err)
	require.Equal(t, "original-child", resolved.ThreadID)
	require.Equal(t, "child-native-turn", resolved.NativeTurnID)
	_, err = ResolveAGUIMCPApp(ctx, client.store, "foreign", alias, "hash")
	require.Error(t, err)
	// A real checkpoint cannot grant a different owner or child conversation.
	binding.ThreadID = "foreign-child"
	forged, err := MCPAppsActivity("forged", binding, json.RawMessage(`{"content":[]}`), nil)
	require.NoError(t, err)
	var event map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(forged, &event))
	event["subagentRunId"] = rawAGUI(inv.ID)
	require.Error(t, validateMCPAppJournalAncestry(ctx, client.store, writer.run, rawAGUI(event), writer.run.LastSequence+1))
	binding.ThreadID = inv.ConversationID
	binding.InvocationID = "unregistered"
	forged, err = MCPAppsActivity("forged", binding, json.RawMessage(`{"content":[]}`), nil)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(forged, &event))
	event["subagentRunId"] = rawAGUI(binding.InvocationID)
	require.Error(t, validateMCPAppJournalAncestry(ctx, client.store, writer.run, rawAGUI(event), writer.run.LastSequence+1))
}

type childMCPAppsNativeRegistry struct {
	integrationToolRegistry
	calls                int
	originalConversation string
}

func (r *childMCPAppsNativeRegistry) Execute(ctx context.Context, _ string, _ map[string]interface{}) (string, error) {
	r.calls++
	turn, ok := requestctx.TurnMetaFromContext(ctx)
	if !ok || turn.ConversationID != r.originalConversation {
		return "", fmt.Errorf("guest native conversation differs from original child")
	}
	var result mcpschema.CallToolResult
	if err := json.Unmarshal([]byte(`{"resultType":"complete","content":[{"type":"text","text":"safe-native-child"}],"structuredContent":{"child":true},"_meta":{"native":"private"}}`), &result); err != nil {
		return "", err
	}
	if err := mcpapps.Record(ctx, "same-server", "tool", exportrequestmodel.ID(ctx), &result); err != nil {
		return "", err
	}
	return "safe-native-child", nil
}

func TestMCPAppsChildProxyExecutesCanonicalGuestInOriginalChild(t *testing.T) {
	client, _ := newDurableAGUIServer(t)
	ctx := context.Background()
	native := convmem.New()
	require.NoError(t, native.EnsureConversation("original-child", func(conversation *apiconv.MutableConversation) { conversation.SetStatus("running") }))
	registry := &childMCPAppsNativeRegistry{originalConversation: "original-child", integrationToolRegistry: integrationToolRegistry{defs: []llm.ToolDefinition{{Name: mcpname.NewName("same-server", "tool").String()}}}}
	service := agentsvc.New(nil, nil, nil, registry, nil, native)
	backend, err := NewEmbedded(service, native)
	require.NoError(t, err)
	input := &agui.RunAgentInput{ThreadID: "isolated-proxy", RunID: "proxy-run"}
	record, _, err := client.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: input.ThreadID, RunID: input.RunID, TurnID: "proxy-native-turn", Input: rawAGUI(map[string]any{"threadId": input.ThreadID, "runId": input.RunID, "messages": []any{}})})
	require.NoError(t, err)
	app := AGUIMCPAppBinding{AppInstanceID: "issued-child-instance", ThreadID: "original-child", ServerID: "same-server", PublicServerID: "issued-child-alias", ServerHash: "hash", ResourceURI: "ui://child/app"}
	binding := AGUIMCPAppsBindings{App: app, Authorize: func(context.Context, AGUIMCPAppBinding) error { return nil }, AuthorizeTool: func(_ context.Context, _ AGUIMCPAppBinding, name string) error {
		if name != "tool" {
			return fmt.Errorf("unauthorized tool")
		}
		return nil
	}, ToolCaller: backend.ExecuteMCPUIToolCall}
	request := &MCPAppsProxyRequest{ServerID: app.PublicServerID, ServerHash: app.ServerHash, Method: "tools/call", Params: map[string]json.RawMessage{"name": json.RawMessage(`"tool"`)}}
	require.NoError(t, runAGUIMCPProxyWorker(WithAGUIMCPAppsBindings(ctx, binding), backend, client.store, record, input, request, nil))
	require.Equal(t, 1, registry.calls)
	finished, err := client.store.GetRun(ctx, "owner", input.ThreadID, input.RunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusFinished, finished.Status)
	require.Contains(t, string(finished.Pending), `"nativeOperationId":"tool-`)
	events, err := client.store.Replay(ctx, "owner", input.ThreadID, input.RunID, 0, 10)
	require.NoError(t, err)
	require.Contains(t, string(events[len(events)-1].Event), `"native":"private"`)
	require.ErrorIs(t, runAGUIMCPProxyWorker(WithAGUIMCPAppsBindings(ctx, binding), backend, client.store, finished, input, request, nil), aguistore.ErrInvalidTransition)
	require.Equal(t, 1, registry.calls)
}
