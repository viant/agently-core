package agent

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	toolapprovalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	"testing"
)

func TestMCPAppsToolPolicyUsesCurrentAgentAllowlist(t *testing.T) {
	ctx := context.Background()
	svc := &Service{registry: &fakeRegistry{defs: []llm.ToolDefinition{{Name: "server-read"}, {Name: "server-write"}}}}
	ag := &agentmdl.Agent{Tool: agentmdl.Tool{Items: []*llm.Tool{{Name: "server-read"}}}}
	_, err := svc.MCPAppToolPolicy(ctx, ag, "server-read")
	require.NoError(t, err)
	_, err = svc.MCPAppToolPolicy(ctx, ag, "server-write")
	require.Error(t, err)
	ag.Tool = agentmdl.Tool{}
	_, err = svc.MCPAppToolPolicy(ctx, ag, "server-read")
	require.Error(t, err)
}

func TestMCPAppsToolPolicyPreservesBundlesAndRechecksRevocation(t *testing.T) {
	ctx := context.Background()
	bundles := []*toolbundle.Bundle{{ID: "trusted", Match: []llm.Tool{{Name: "server-read", Approval: &llm.ApprovalConfig{Mode: llm.ApprovalModePrompt}}}}}
	svc := &Service{registry: &fakeRegistry{defs: []llm.ToolDefinition{{Name: "server-read"}}}, toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) { return bundles, nil }}
	ag := &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"trusted"}}}
	actual, err := svc.MCPAppToolPolicy(ctx, ag, "server-read")
	require.NoError(t, err)
	require.Equal(t, []string{"trusted"}, actual)
	bundles = []*toolbundle.Bundle{{ID: "trusted", Match: []llm.Tool{{Name: "server-other"}}}}
	_, err = svc.MCPAppToolPolicy(ctx, ag, "server-read")
	require.Error(t, err)
}

func TestMCPAppsPreparedGuestPreservesConfiguredUnionAndApproval(t *testing.T) {
	for _, name := range []string{"server-explicit", "server-bundled"} {
		t.Run(name, func(t *testing.T) {
			native := convmem.New()
			seedConversation(t, native, "original")
			registry := &recordingExecRegistry{fakeRegistry: fakeRegistry{defs: []llm.ToolDefinition{{Name: "server-explicit"}, {Name: "server-bundled"}, {Name: "server-denied"}}}, result: "safe"}
			bundles := []*toolbundle.Bundle{{ID: "trusted", Match: []llm.Tool{{Name: "server-bundled", Approval: &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue}}}}}
			service := New(nil, nil, nil, registry, nil, native, WithToolBundles(func(context.Context) ([]*toolbundle.Bundle, error) { return bundles, nil }))
			ag := &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"trusted"}, Items: []*llm.Tool{{Name: "server-explicit"}}}}
			prepared, err := service.PrepareMCPAppToolContext(context.Background(), ag, name)
			require.NoError(t, err)
			require.True(t, toolapprovalqueue.RequiresQueue(prepared, "server-bundled"))
			output, err := service.RunGuestToolCall(prepared, &GuestToolCallInput{ConversationID: "original", ToolName: name, ToolBundles: []string{"caller-cannot-override"}})
			require.NoError(t, err)
			if name == "server-bundled" {
				require.Equal(t, GuestToolStatusQueued, output.Status)
				require.False(t, registry.executed)
			} else {
				require.Equal(t, GuestToolStatusOK, output.Status)
				require.True(t, registry.executed)
			}
			_, err = service.RunGuestToolCall(prepared, &GuestToolCallInput{ConversationID: "original", ToolName: "server-denied", ToolBundles: []string{"caller-grant"}})
			require.Error(t, err)
			_, err = service.PrepareMCPAppToolContext(context.Background(), ag, "server-denied")
			require.Error(t, err)
			ag.Tool.Items = nil
			_, err = service.PrepareMCPAppToolContext(context.Background(), ag, "server-explicit")
			require.Error(t, err)
		})
	}
}

func TestMCPAppsPreparedPromptPolicyCannotBeDropped(t *testing.T) {
	service := &Service{registry: &fakeRegistry{defs: []llm.ToolDefinition{{Name: "server-prompt"}}}, toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
		return []*toolbundle.Bundle{{ID: "prompt", Match: []llm.Tool{{Name: "server-prompt", Approval: &llm.ApprovalConfig{Mode: llm.ApprovalModePrompt}}}}}, nil
	}}
	ctx, err := service.PrepareMCPAppToolContext(context.Background(), &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"prompt"}}}, "server-prompt")
	require.NoError(t, err)
	require.True(t, toolapprovalqueue.RequiresPrompt(ctx, "server-prompt"))
}
