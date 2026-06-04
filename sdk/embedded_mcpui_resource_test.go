package sdk

import (
	"context"
	"testing"

	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpuimeta "github.com/viant/mcp-ui/meta"
	mcpuiresource "github.com/viant/mcp-ui/resource"
	mcpclient "github.com/viant/mcp/client"
)

type mcpUIResourceProviderStub struct{}

func (m *mcpUIResourceProviderStub) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return &mcpcfg.MCPClient{}, nil
}

type mcpUIResourceClientStub struct {
	readCalls int
	listCalls int
	wantURI   string
}

func (s *mcpUIResourceClientStub) Initialize(context.Context, ...mcpclient.RequestOption) (*mcpschema.InitializeResult, error) {
	return &mcpschema.InitializeResult{}, nil
}
func (s *mcpUIResourceClientStub) ListResourceTemplates(context.Context, *string, ...mcpclient.RequestOption) (*mcpschema.ListResourceTemplatesResult, error) {
	return nil, nil
}
func (s *mcpUIResourceClientStub) ListResources(context.Context, *string, ...mcpclient.RequestOption) (*mcpschema.ListResourcesResult, error) {
	s.listCalls++
	return &mcpschema.ListResourcesResult{}, nil
}
func (s *mcpUIResourceClientStub) ListPrompts(context.Context, *string, ...mcpclient.RequestOption) (*mcpschema.ListPromptsResult, error) {
	return nil, nil
}
func (s *mcpUIResourceClientStub) ListTools(context.Context, *string, ...mcpclient.RequestOption) (*mcpschema.ListToolsResult, error) {
	return &mcpschema.ListToolsResult{}, nil
}
func (s *mcpUIResourceClientStub) ReadResource(_ context.Context, params *mcpschema.ReadResourceRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.ReadResourceResult, error) {
	s.readCalls++
	if params == nil || params.Uri != s.wantURI {
		return nil, nil
	}
	contents, err := mcpuiresource.NewReadResultHTMLContents(
		s.wantURI,
		"<html><body>fixture</body></html>",
		mcpuimeta.ResourceUI{ProtocolVersion: "2025-11-25"},
	)
	if err != nil {
		return nil, err
	}
	return &mcpschema.ReadResourceResult{Contents: []mcpschema.ReadResourceResultContentsElem{*contents}}, nil
}
func (s *mcpUIResourceClientStub) GetPrompt(context.Context, *mcpschema.GetPromptRequestParams, ...mcpclient.RequestOption) (*mcpschema.GetPromptResult, error) {
	return nil, nil
}
func (s *mcpUIResourceClientStub) CallTool(context.Context, *mcpschema.CallToolRequestParams, ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	return &mcpschema.CallToolResult{}, nil
}
func (s *mcpUIResourceClientStub) Complete(context.Context, *mcpschema.CompleteRequestParams, ...mcpclient.RequestOption) (*mcpschema.CompleteResult, error) {
	return nil, nil
}
func (s *mcpUIResourceClientStub) Ping(context.Context, *mcpschema.PingRequestParams, ...mcpclient.RequestOption) (*mcpschema.PingResult, error) {
	return nil, nil
}
func (s *mcpUIResourceClientStub) Subscribe(context.Context, *mcpschema.SubscribeRequestParams, ...mcpclient.RequestOption) (*mcpschema.SubscribeResult, error) {
	return nil, nil
}
func (s *mcpUIResourceClientStub) Unsubscribe(context.Context, *mcpschema.UnsubscribeRequestParams, ...mcpclient.RequestOption) (*mcpschema.UnsubscribeResult, error) {
	return nil, nil
}
func (s *mcpUIResourceClientStub) SetLevel(context.Context, *mcpschema.SetLevelRequestParams, ...mcpclient.RequestOption) (*mcpschema.SetLevelResult, error) {
	return nil, nil
}
func (s *mcpUIResourceClientStub) ListRoots(context.Context, *mcpschema.ListRootsRequestParams, ...mcpclient.RequestOption) (*mcpschema.ListRootsResult, error) {
	return nil, nil
}
func (s *mcpUIResourceClientStub) CreateMessage(context.Context, *mcpschema.CreateMessageRequestParams, ...mcpclient.RequestOption) (*mcpschema.CreateMessageResult, error) {
	return nil, nil
}
func (s *mcpUIResourceClientStub) Elicit(context.Context, *mcpschema.ElicitRequestParams, ...mcpclient.RequestOption) (*mcpschema.ElicitResult, error) {
	return nil, nil
}

func TestBackendClient_ReadMCPUIResource_UsesServerScopeRead(t *testing.T) {
	const uri = "ui://polly/view/activity-activity-1"

	mgr, err := manager.New(&mcpUIResourceProviderStub{})
	client := &mcpUIResourceClientStub{wantURI: uri}
	mgr, err = manager.New(&mcpUIResourceProviderStub{}, manager.WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) {
		return client, nil
	}))
	if err != nil {
		t.Fatalf("manager.New() error = %v", err)
	}

	backend := &backendClient{mcpMgr: mgr}
	result, err := backend.ReadMCPUIResource(context.Background(), uri)
	if err != nil {
		t.Fatalf("ReadMCPUIResource() error = %v", err)
	}
	if result == nil || len(result.Contents) != 1 {
		t.Fatalf("ReadMCPUIResource() result = %#v", result)
	}
	if client.readCalls != 1 {
		t.Fatalf("readCalls = %d, want 1", client.readCalls)
	}
	if client.listCalls != 0 {
		t.Fatalf("listCalls = %d, want 0", client.listCalls)
	}
}
