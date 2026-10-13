package sdk

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	conversation "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/shared/toolexec"
)

type aliasEventPublisher struct{ events []*streaming.Event }

func (p *aliasEventPublisher) Publish(_ context.Context, event *streaming.Event) error {
	copy := *event
	p.events = append(p.events, &copy)
	return nil
}

func TestAGUIClientToolDeferralNativeStreamPreservesDeclaredAlias(t *testing.T) {
	ctx := recoveryContext()
	backend, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, backend.Shutdown(ctx)) })
	conv, err := convservice.New(ctx, backend)
	require.NoError(t, err)
	owner := conversation.NewConversation()
	owner.SetId("alias-conversation")
	owner.SetCreatedByUserID("owner")
	require.NoError(t, conv.PatchConversations(ctx, owner))
	turn := conversation.NewTurn()
	turn.SetId("alias-turn")
	turn.SetConversationID("alias-conversation")
	turn.SetStatus("running")
	require.NoError(t, conv.PatchTurn(ctx, turn))
	assistant := conversation.NewMessage()
	assistant.SetId("alias-assistant")
	assistant.SetConversationID("alias-conversation")
	assistant.SetTurnID("alias-turn")
	assistant.SetRole("assistant")
	assistant.SetType("text")
	require.NoError(t, conv.PatchMessage(ctx, assistant))
	session, err := clienttool.NewSession([]llm.ToolDefinition{{Name: "device-safe_read", Parameters: map[string]interface{}{"type": "object"}}})
	require.NoError(t, err)
	ctx = clienttool.WithSession(requestctx.WithModelMessageID(requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "alias-conversation", TurnID: "alias-turn"}), "alias-assistant"), session)
	publisher := &aliasEventPublisher{}
	conv.SetStreamPublisher(publisher)
	// Use the registry's service/method interpretation of a model-safe alias,
	// then the real executor and Datly-backed native conversation notifications.
	name := mcpname.Name(mcpname.Canonical("device-safe_read"))
	deferralStarted := time.Now()
	_, _, err = toolexec.ExecuteToolStep(ctx, nil, toolexec.StepInfo{ID: "alias-call", Name: name.Service() + "/" + name.Method(), Args: map[string]interface{}{"target": "public"}}, conv)
	deferralDuration := time.Since(deferralStarted)
	require.NoError(t, err)
	require.Len(t, session.Pending(), 1)
	require.Equal(t, "device-safe_read", session.Pending()[0].Name)
	var opener *streaming.Event
	for _, event := range publisher.events {
		if event.ToolCallID == "alias-call" && event.Type == streaming.EventTypeToolCallStarted {
			opener = event
			break
		}
	}
	require.NotNil(t, opener)
	require.Equal(t, "device/safe_read", opener.ToolName, "native UI notifications use display names")
	require.NoError(t, hydrateAGUITool(ctx, newAGUITestClient(), opener))
	require.Equal(t, "device-safe_read", opener.ToolName)
	translator := agui.NewTranslator("alias-thread", "alias-run")
	translator.Start()
	var wireName string
	for _, event := range translator.Translate(opener) {
		if event.Type == "TOOL_CALL_START" {
			wireName = event.ToolCallName
		}
	}
	require.Equal(t, "device-safe_read", wireName)
	foreign := &streaming.Event{Type: streaming.EventTypeToolCallStarted, ToolName: "other/safe_read"}
	require.NoError(t, hydrateAGUITool(ctx, newAGUITestClient(), foreign))
	require.Equal(t, "other/safe_read", foreign.ToolName, "unregistered names do not gain aliases")
	// Exercise the production SDK result-persistence entry against the same
	// Datly-backed original call; replay cannot create a second result/effect.
	completionStarted := time.Now()
	runtime := &backendClient{conv: conv}
	pending := session.Pending()[0]
	require.NoError(t, runtime.aguiCompleteTool(ctx, pending, json.RawMessage(`"public fixture result"`), ""))
	completionDuration := time.Since(completionStarted)
	require.NoError(t, runtime.aguiCompleteTool(ctx, pending, json.RawMessage(`"public fixture result"`), ""))
	require.Error(t, runtime.aguiCompleteTool(ctx, pending, json.RawMessage(`"changed result"`), ""))
	message, e := conv.GetMessage(ctx, pending.ToolMessageID, conversation.WithIncludeToolCall(true))
	require.NoError(t, e)
	require.Equal(t, "public fixture result", message.GetContent())
	t.Logf("actual Datly client-tool deferral=%s resultPersist=%s", deferralDuration, completionDuration)
	require.Less(t, deferralDuration+completionDuration, 3*time.Second, "public no-model proxy persistence must not impose a multi-second wait")

}
