package sdk

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/agui"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	agentsvc "github.com/viant/agently-core/service/agent"
)

func TestMCPAppsNativeDatlyGuestParentApprovalAndReceipt(t *testing.T) {
	for _, action := range []string{"approve", "reject"} {
		t.Run(action, func(t *testing.T) {
			ctx := recoveryContext()
			runtime, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
			require.NoError(t, err)
			defer runtime.Shutdown(ctx)
			conv, err := convservice.New(ctx, runtime)
			require.NoError(t, err)
			original := apiconv.NewConversation()
			original.SetId("original-child")
			original.SetCreatedByUserID("owner")
			original.SetStatus("active")
			require.NoError(t, conv.PatchConversations(ctx, original))
			registry := &childMCPAppsNativeRegistry{originalConversation: "original-child", integrationToolRegistry: integrationToolRegistry{defs: []llm.ToolDefinition{{Name: "same-server/tool"}}}}
			service := agentsvc.New(nil, nil, nil, registry, nil, conv, agentsvc.WithToolBundles(func(context.Context) ([]*toolbundle.Bundle, error) {
				return []*toolbundle.Bundle{{ID: "queue", Match: []llm.Tool{{Name: "same-server/tool", Approval: &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue}}}}}, nil
			}), agentsvc.WithDataService(data.NewService(runtime)))
			backend := &backendClient{agent: service, conv: conv, data: data.NewService(runtime), goalInvoker: runtime, registry: registry}
			store := aguistore.New(runtime)
			input := &agui.RunAgentInput{ThreadID: "isolated-proxy", RunID: "initial"}
			record, _, err := store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: input.ThreadID, RunID: input.RunID, TurnID: "proxy-native-turn", Input: rawAGUI(map[string]any{"threadId": input.ThreadID, "runId": input.RunID, "messages": []any{}})})
			require.NoError(t, err)
			app := AGUIMCPAppBinding{AppInstanceID: "issued", ThreadID: "original-child", ServerID: "same-server", PublicServerID: "issued-alias", ServerHash: "hash", ResourceURI: "ui://app"}
			binding := AGUIMCPAppsBindings{App: app, Authorize: func(context.Context, AGUIMCPAppBinding) error { return nil }, AuthorizeTool: func(context.Context, AGUIMCPAppBinding, string) error { return nil }, ToolCaller: func(ctx context.Context, in *MCPUIToolCallInput) (*MCPUIToolCallOutput, error) {
				prepared, e := service.PrepareMCPAppToolContext(ctx, &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"queue"}}}, in.ToolName)
				if e != nil {
					return nil, e
				}
				return backend.ExecuteMCPUIToolCall(prepared, in)
			}}
			proxy := &MCPAppsProxyRequest{ServerID: app.PublicServerID, ServerHash: app.ServerHash, Method: "tools/call", Params: map[string]json.RawMessage{"name": json.RawMessage(`"tool"`)}}
			workerCtx := WithAGUIMCPAppsBindings(ctx, binding)
			require.NoError(t, runAGUIMCPProxyWorker(workerCtx, backend, store, record, input, proxy, nil))
			prior, err := store.GetRun(ctx, "owner", input.ThreadID, input.RunID)
			require.NoError(t, err)
			initialEvents, replayErr := store.Replay(ctx, "owner", input.ThreadID, input.RunID, 0, 10)
			require.NoError(t, replayErr)
			require.Equal(t, aguistore.StatusInterrupted, prior.Status, string(rawAGUI(initialEvents)))
			require.Zero(t, registry.calls)
			var pending aguiMCPProxyPending
			require.NoError(t, json.Unmarshal(prior.Pending, &pending))
			require.Len(t, pending.Interrupts, 1)
			parent, err := conv.GetMessage(ctx, pending.NativeTurnID)
			require.NoError(t, err)
			require.NotNil(t, parent)
			require.Equal(t, "assistant", parent.Role)
			require.Equal(t, 1, parent.Interim)
			require.Equal(t, "MCP UI host tool request", valueOrEmpty(parent.Content))
			require.NotContains(t, string(rawAGUI(parent)), "private")
			payload := rawAGUI(map[string]any{"action": action})
			continuation := &agui.RunAgentInput{ThreadID: input.ThreadID, RunID: "continuation", Resume: rawAGUI([]agui.WireResumeEntry{{InterruptId: pending.Interrupts[0].ID, Status: "resolved", Payload: &payload}})}
			next, _, err := store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: continuation.ThreadID, RunID: continuation.RunID, TurnID: prior.TurnID, PriorRunID: prior.RunID, ExpectedPriorRevision: prior.Revision, Input: rawAGUI(map[string]any{"threadId": continuation.ThreadID, "runId": continuation.RunID, "messages": []any{}, "resume": json.RawMessage(continuation.Resume)})})
			require.NoError(t, err)
			require.NoError(t, runAGUIMCPProxyWorker(workerCtx, backend, store, next, continuation, proxy, prior))
			finished, err := store.GetRun(ctx, "owner", continuation.ThreadID, continuation.RunID)
			require.NoError(t, err)
			conversation, readErr := conv.GetConversation(ctx, "original-child", apiconv.WithIncludeTranscript(true))
			require.NoError(t, readErr)
			found := false
			for _, turn := range conversation.Transcript {
				if turn.Id != pending.NativeTurnID {
					continue
				}
				found = true
				if action == "approve" {
					require.Equal(t, "succeeded", turn.Status)
				} else {
					require.Equal(t, "failed", turn.Status)
				}
			}
			require.True(t, found, "the actual native guest turn must be finalized")
			events, err := store.Replay(ctx, "owner", continuation.ThreadID, continuation.RunID, 0, 10)
			require.NoError(t, err)
			if action == "approve" {
				require.Equal(t, aguistore.StatusFinished, finished.Status)
				require.Equal(t, 1, registry.calls)
				require.Contains(t, string(events[len(events)-1].Event), `"native":"private"`)
			} else {
				require.Equal(t, aguistore.StatusError, finished.Status)
				require.Zero(t, registry.calls)
				require.Contains(t, string(events[len(events)-1].Event), "MCP_PROXY_REJECTED")
			}
			require.ErrorIs(t, runAGUIMCPProxyWorker(workerCtx, backend, store, finished, continuation, proxy, prior), aguistore.ErrInvalidTransition)
		})
	}
}
