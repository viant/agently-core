package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	registryimpl "github.com/viant/agently-core/internal/tool/registry"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	toolapproval "github.com/viant/agently-core/protocol/tool/approvalqueue"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/service/browsermcp"
	mcpclient "github.com/viant/mcp/client"
	"strings"
	"sync/atomic"
	"testing"
)

type browserBundleProvider struct{ options *cfg.MCPClient }

func (p browserBundleProvider) Options(_ context.Context, name string) (*cfg.MCPClient, error) {
	if name != "device" {
		return nil, fmt.Errorf("unknown fixture server")
	}
	return p.options, nil
}
func (p browserBundleProvider) Names(context.Context) ([]string, error) {
	return []string{"device"}, nil
}
func TestBrowserWildcardBundleUsesValidatedCatalogAndPreservesApproval(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "fixture-owner"})
	config := &cfg.MCPClient{ExecutionLocation: "browser", BrowserTransport: &cfg.BrowserTransport{Type: "chrome-extension", ExtensionID: strings.Repeat("a", 32), PortName: "fixture-browser"}, BrowserTools: []string{"*"}}
	var backendClients atomic.Int32
	mgr, err := manager.New(browserBundleProvider{config}, manager.WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) {
		backendClients.Add(1)
		return nil, fmt.Errorf("backend client must not be created")
	}))
	require.NoError(t, err)
	reg, err := registryimpl.NewWithManager(mgr)
	require.NoError(t, err)
	catalogRegistry := browsermcp.New(mgr.BrowserDescriptors)
	schema := json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}}}`)
	catalog, err := catalogRegistry.Register(ctx, "fixture-owner", browsermcp.Registration{ConversationID: "conversation", ThreadID: "thread", ConnectionID: "connection", Server: "device", Tools: []browsermcp.Tool{{Name: "read", InputSchema: schema}, {Name: "write", InputSchema: schema}}})
	require.NoError(t, err)
	defs, err := catalogRegistry.Validate(ctx, "fixture-owner", "thread", catalog.Tools)
	require.NoError(t, err)
	scoped := toolapproval.WithState(browsermcp.WithDefinitions(ctx, defs))
	svc := &Service{registry: reg, toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
		return []*toolbundle.Bundle{{ID: "workbench", Match: []llm.Tool{{Name: "device-*", Exclude: []string{"device-write", "device-absent"}, Approval: &llm.ApprovalConfig{Mode: llm.ApprovalModePrompt}}}}}, nil
	}}
	resolved, err := svc.resolveBundleDefinitions(scoped, []string{"workbench"})
	require.NoError(t, err)
	require.Len(t, resolved, 1)
	require.Equal(t, "device-read", resolved[0].Name)
	approval, ok := toolapproval.ConfigFor(scoped, "device-read")
	require.True(t, ok)
	require.True(t, approval.IsPrompt())
	require.Zero(t, backendClients.Load())
	store := convmem.New()
	conversation := apiconv.NewConversation()
	conversation.SetId("conversation")
	require.NoError(t, store.PatchConversations(ctx, conversation))
	svc.conversation = store
	clientSession, err := clienttool.NewSession(resolved)
	require.NoError(t, err)
	binding, err := svc.BuildBinding(clienttool.WithSession(scoped, clientSession), &QueryInput{ConversationID: "conversation", Agent: &agentmdl.Agent{Identity: agentmdl.Identity{ID: "coder"}, Tool: agentmdl.Tool{Bundles: []string{"workbench"}}}})
	require.NoError(t, err)
	require.Contains(t, bindingToolNames(binding.Tools.Signatures), "device-read")
	require.NotContains(t, bindingToolNames(binding.Tools.Signatures), "device-write")
	require.Zero(t, backendClients.Load())
	_, err = svc.resolveBundleDefinitions(ctx, []string{"workbench"})
	require.ErrorIs(t, err, manager.ErrBrowserExecutionRequired)
	require.Zero(t, backendClients.Load())
	_, err = catalogRegistry.Validate(ctx, "different-owner", "thread", catalog.Tools)
	require.Error(t, err)
	require.NoError(t, catalogRegistry.Revoke("fixture-owner", catalog.ID))
	_, err = catalogRegistry.Validate(ctx, "fixture-owner", "thread", catalog.Tools)
	require.Error(t, err)
}
