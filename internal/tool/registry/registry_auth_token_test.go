package tool

import (
	"context"
	"errors"
	"testing"

	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

type authCaptureClient struct {
	options *mcpclient.RequestOptions
}

func (c *authCaptureClient) Initialize(ctx context.Context, options ...mcpclient.RequestOption) (*mcpschema.InitializeResult, error) {
	return &mcpschema.InitializeResult{}, nil
}
func (c *authCaptureClient) ListResourceTemplates(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListResourceTemplatesResult, error) {
	return nil, errors.New("not implemented")
}
func (c *authCaptureClient) ListResources(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListResourcesResult, error) {
	return nil, errors.New("not implemented")
}
func (c *authCaptureClient) ListPrompts(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListPromptsResult, error) {
	return nil, errors.New("not implemented")
}
func (c *authCaptureClient) ListTools(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListToolsResult, error) {
	return nil, errors.New("not implemented")
}
func (c *authCaptureClient) ReadResource(ctx context.Context, params *mcpschema.ReadResourceRequestParams, options ...mcpclient.RequestOption) (*mcpschema.ReadResourceResult, error) {
	return nil, errors.New("not implemented")
}
func (c *authCaptureClient) GetPrompt(ctx context.Context, params *mcpschema.GetPromptRequestParams, options ...mcpclient.RequestOption) (*mcpschema.GetPromptResult, error) {
	return nil, errors.New("not implemented")
}
func (c *authCaptureClient) CallTool(ctx context.Context, params *mcpschema.CallToolRequestParams, options ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	c.options = mcpclient.NewRequestOptions(options)
	return &mcpschema.CallToolResult{
		Content: []mcpschema.CallToolResultContentElem{&mcpschema.TextContent{Type: "text", Text: "ok"}},
	}, nil
}
func (c *authCaptureClient) Complete(ctx context.Context, params *mcpschema.CompleteRequestParams, options ...mcpclient.RequestOption) (*mcpschema.CompleteResult, error) {
	return nil, errors.New("not implemented")
}
func (c *authCaptureClient) Ping(ctx context.Context, params *mcpschema.PingRequestParams, options ...mcpclient.RequestOption) (*mcpschema.PingResult, error) {
	return nil, errors.New("not implemented")
}
func (c *authCaptureClient) Subscribe(ctx context.Context, params *mcpschema.SubscribeRequestParams, options ...mcpclient.RequestOption) (*mcpschema.SubscribeResult, error) {
	return nil, errors.New("not implemented")
}
func (c *authCaptureClient) Unsubscribe(ctx context.Context, params *mcpschema.UnsubscribeRequestParams, options ...mcpclient.RequestOption) (*mcpschema.UnsubscribeResult, error) {
	return nil, errors.New("not implemented")
}
func (c *authCaptureClient) SetLevel(ctx context.Context, params *mcpschema.SetLevelRequestParams, options ...mcpclient.RequestOption) (*mcpschema.SetLevelResult, error) {
	return nil, errors.New("not implemented")
}
func (c *authCaptureClient) ListRoots(ctx context.Context, params *mcpschema.ListRootsRequestParams, options ...mcpclient.RequestOption) (*mcpschema.ListRootsResult, error) {
	return nil, errors.New("not implemented")
}
func (c *authCaptureClient) CreateMessage(ctx context.Context, params *mcpschema.CreateMessageRequestParams, options ...mcpclient.RequestOption) (*mcpschema.CreateMessageResult, error) {
	return nil, errors.New("not implemented")
}
func (c *authCaptureClient) Elicit(ctx context.Context, params *mcpschema.ElicitRequestParams, options ...mcpclient.RequestOption) (*mcpschema.ElicitResult, error) {
	return nil, errors.New("not implemented")
}

type executeAuthManagerStub struct {
	client                   mcpclient.Interface
	withAuthTokenContextCall int
}

func (m *executeAuthManagerStub) Get(ctx context.Context, convID, serverName string) (mcpclient.Interface, error) {
	return m.client, nil
}
func (m *executeAuthManagerStub) Reconnect(ctx context.Context, convID, serverName string) (mcpclient.Interface, error) {
	return nil, errors.New("not implemented")
}
func (m *executeAuthManagerStub) Touch(convID, serverName string) {}
func (m *executeAuthManagerStub) Options(ctx context.Context, serverName string) (*mcpcfg.MCPClient, error) {
	return nil, nil
}
func (m *executeAuthManagerStub) UseIDToken(ctx context.Context, serverName string) bool {
	return false
}
func (m *executeAuthManagerStub) WithAuthTokenContext(ctx context.Context, serverName string) context.Context {
	m.withAuthTokenContextCall++
	return authctx.WithTokens(ctx, &scyauth.Token{
		Token: oauth2.Token{AccessToken: "fresh-access-token"},
	})
}

func TestExecute_UsesRefreshedAuthContextForMCPCall(t *testing.T) {
	client := &authCaptureClient{}
	reg := &Registry{
		mgr:           &executeAuthManagerStub{client: client},
		cache:         map[string]*toolCacheEntry{},
		internal:      map[string]mcpclient.Interface{},
		recentResults: map[string]map[string]recentItem{},
	}

	ctx := context.Background()
	ctx = runtimerequestctx.WithConversationID(ctx, "conv-1")
	out, err := reg.Execute(ctx, "guardian/ping", map[string]interface{}{"q": "x"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "ok" {
		t.Fatalf("Execute() output = %q, want %q", out, "ok")
	}
	if client.options == nil {
		t.Fatalf("expected client request options to be captured")
	}
	if client.options.StringToken != "fresh-access-token" {
		t.Fatalf("auth token = %q, want %q", client.options.StringToken, "fresh-access-token")
	}
}
