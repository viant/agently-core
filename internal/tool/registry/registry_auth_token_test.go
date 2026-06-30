package tool

import (
	"context"
	"errors"
	"strings"
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
	out, err := reg.Execute(ctx, "helper/ping", map[string]interface{}{"q": "x"})
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

type reconnectingCallClient struct {
	errOnCall error
	options   *mcpclient.RequestOptions
}

func (c *reconnectingCallClient) Initialize(ctx context.Context, options ...mcpclient.RequestOption) (*mcpschema.InitializeResult, error) {
	return &mcpschema.InitializeResult{}, nil
}
func (c *reconnectingCallClient) ListResourceTemplates(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListResourceTemplatesResult, error) {
	return nil, errors.New("not implemented")
}
func (c *reconnectingCallClient) ListResources(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListResourcesResult, error) {
	return nil, errors.New("not implemented")
}
func (c *reconnectingCallClient) ListPrompts(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListPromptsResult, error) {
	return nil, errors.New("not implemented")
}
func (c *reconnectingCallClient) ListTools(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListToolsResult, error) {
	return nil, errors.New("not implemented")
}
func (c *reconnectingCallClient) ReadResource(ctx context.Context, params *mcpschema.ReadResourceRequestParams, options ...mcpclient.RequestOption) (*mcpschema.ReadResourceResult, error) {
	return nil, errors.New("not implemented")
}
func (c *reconnectingCallClient) GetPrompt(ctx context.Context, params *mcpschema.GetPromptRequestParams, options ...mcpclient.RequestOption) (*mcpschema.GetPromptResult, error) {
	return nil, errors.New("not implemented")
}
func (c *reconnectingCallClient) CallTool(ctx context.Context, params *mcpschema.CallToolRequestParams, options ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	c.options = mcpclient.NewRequestOptions(options)
	if c.errOnCall != nil {
		return nil, c.errOnCall
	}
	return &mcpschema.CallToolResult{
		Content: []mcpschema.CallToolResultContentElem{&mcpschema.TextContent{Type: "text", Text: "ok"}},
	}, nil
}
func (c *reconnectingCallClient) Complete(ctx context.Context, params *mcpschema.CompleteRequestParams, options ...mcpclient.RequestOption) (*mcpschema.CompleteResult, error) {
	return nil, errors.New("not implemented")
}
func (c *reconnectingCallClient) Ping(ctx context.Context, params *mcpschema.PingRequestParams, options ...mcpclient.RequestOption) (*mcpschema.PingResult, error) {
	return nil, errors.New("not implemented")
}
func (c *reconnectingCallClient) Subscribe(ctx context.Context, params *mcpschema.SubscribeRequestParams, options ...mcpclient.RequestOption) (*mcpschema.SubscribeResult, error) {
	return nil, errors.New("not implemented")
}
func (c *reconnectingCallClient) Unsubscribe(ctx context.Context, params *mcpschema.UnsubscribeRequestParams, options ...mcpclient.RequestOption) (*mcpschema.UnsubscribeResult, error) {
	return nil, errors.New("not implemented")
}
func (c *reconnectingCallClient) SetLevel(ctx context.Context, params *mcpschema.SetLevelRequestParams, options ...mcpclient.RequestOption) (*mcpschema.SetLevelResult, error) {
	return nil, errors.New("not implemented")
}
func (c *reconnectingCallClient) ListRoots(ctx context.Context, params *mcpschema.ListRootsRequestParams, options ...mcpclient.RequestOption) (*mcpschema.ListRootsResult, error) {
	return nil, errors.New("not implemented")
}
func (c *reconnectingCallClient) CreateMessage(ctx context.Context, params *mcpschema.CreateMessageRequestParams, options ...mcpclient.RequestOption) (*mcpschema.CreateMessageResult, error) {
	return nil, errors.New("not implemented")
}
func (c *reconnectingCallClient) Elicit(ctx context.Context, params *mcpschema.ElicitRequestParams, options ...mcpclient.RequestOption) (*mcpschema.ElicitResult, error) {
	return nil, errors.New("not implemented")
}

type reconnectManagerStub struct {
	client      mcpclient.Interface
	reconnected bool
}

func (m *reconnectManagerStub) Get(ctx context.Context, convID, serverName string) (mcpclient.Interface, error) {
	return m.client, nil
}
func (m *reconnectManagerStub) Reconnect(ctx context.Context, convID, serverName string) (mcpclient.Interface, error) {
	m.reconnected = true
	m.client = &reconnectingCallClient{}
	return m.client, nil
}
func (m *reconnectManagerStub) Touch(convID, serverName string) {}
func (m *reconnectManagerStub) Options(ctx context.Context, serverName string) (*mcpcfg.MCPClient, error) {
	return nil, nil
}
func (m *reconnectManagerStub) UseIDToken(ctx context.Context, serverName string) bool {
	return false
}
func (m *reconnectManagerStub) WithAuthTokenContext(ctx context.Context, serverName string) context.Context {
	return ctx
}

func TestExecute_RetriesOnHandshakeMissingSessionHeader(t *testing.T) {
	mgr := &reconnectManagerStub{
		client: &reconnectingCallClient{errOnCall: errors.New("code: -32603, message: handshake missing Mcp-Session-Id header")},
	}
	reg := &Registry{
		mgr:           mgr,
		cache:         map[string]*toolCacheEntry{},
		internal:      map[string]mcpclient.Interface{},
		recentResults: map[string]map[string]recentItem{},
	}

	ctx := context.Background()
	ctx = runtimerequestctx.WithConversationID(ctx, "conv-1")
	out, err := reg.Execute(ctx, "helper/ping", map[string]interface{}{"q": "x"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "ok" {
		t.Fatalf("Execute() output = %q, want %q", out, "ok")
	}
	if !mgr.reconnected {
		t.Fatalf("expected reconnect to be attempted")
	}
}

func TestExecute_ReconnectRetryUsesConversationScope(t *testing.T) {
	mgr := &scriptedReconnectManager{
		clients: []mcpclient.Interface{
			&scriptedCallClient{err: errors.New("EOF")},
			&scriptedCallClient{result: textToolResult("ok")},
		},
	}
	reg := newReconnectTestRegistry(mgr)

	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	out, err := reg.Execute(ctx, "helper/ping", map[string]interface{}{"q": "x"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "ok" {
		t.Fatalf("Execute() output = %q, want %q", out, "ok")
	}
	if len(mgr.getCalls) != 2 {
		t.Fatalf("expected 2 Get calls, got %+v", mgr.getCalls)
	}
	if len(mgr.reconnectCalls) != 1 {
		t.Fatalf("expected 1 Reconnect call, got %+v", mgr.reconnectCalls)
	}
	for _, call := range append(mgr.getCalls, mgr.reconnectCalls...) {
		if call.convID != "conv-1" || call.server != "helper" {
			t.Fatalf("expected conv-1/helper scope for every manager call, got gets=%+v reconnects=%+v", mgr.getCalls, mgr.reconnectCalls)
		}
	}
}

func TestExecute_StopsAfterReconnectableErrorRetryExhaustion(t *testing.T) {
	mgr := &scriptedReconnectManager{
		clients: []mcpclient.Interface{
			&scriptedCallClient{err: errors.New("EOF")},
			&scriptedCallClient{err: errors.New("EOF")},
			&scriptedCallClient{err: errors.New("EOF")},
		},
	}
	reg := newReconnectTestRegistry(mgr)

	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	_, err := reg.Execute(ctx, "helper/ping", map[string]interface{}{"q": "x"})
	if err == nil {
		t.Fatal("expected Execute() to fail after retry exhaustion")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "eof") {
		t.Fatalf("expected final EOF error, got %v", err)
	}
	if len(mgr.getCalls) != 3 {
		t.Fatalf("expected 3 Get calls, got %+v", mgr.getCalls)
	}
	if len(mgr.reconnectCalls) != 2 {
		t.Fatalf("expected 2 Reconnect calls, got %+v", mgr.reconnectCalls)
	}
}

func TestExecute_RetriesReconnectableToolErrorResult(t *testing.T) {
	mgr := &scriptedReconnectManager{
		clients: []mcpclient.Interface{
			&scriptedCallClient{result: errorToolResult("stream error: INTERNAL_ERROR; received from peer")},
			&scriptedCallClient{result: textToolResult("ok")},
		},
	}
	reg := newReconnectTestRegistry(mgr)

	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	out, err := reg.Execute(ctx, "helper/ping", map[string]interface{}{"q": "x"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "ok" {
		t.Fatalf("Execute() output = %q, want %q", out, "ok")
	}
	if len(mgr.reconnectCalls) != 1 {
		t.Fatalf("expected 1 Reconnect call, got %+v", mgr.reconnectCalls)
	}
}

func TestExecute_DoesNotReconnectNonReconnectableToolError(t *testing.T) {
	mgr := &scriptedReconnectManager{
		clients: []mcpclient.Interface{
			&scriptedCallClient{err: errors.New("validation failed")},
		},
	}
	reg := newReconnectTestRegistry(mgr)

	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	_, err := reg.Execute(ctx, "helper/ping", map[string]interface{}{"q": "x"})
	if err == nil {
		t.Fatal("expected Execute() to fail")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("expected validation error, got %v", err)
	}
	if len(mgr.reconnectCalls) != 0 {
		t.Fatalf("expected no Reconnect calls, got %+v", mgr.reconnectCalls)
	}
	if len(mgr.getCalls) != 1 {
		t.Fatalf("expected 1 Get call, got %+v", mgr.getCalls)
	}
}

type scriptedManagerCall struct {
	convID string
	server string
}

type scriptedReconnectManager struct {
	clients        []mcpclient.Interface
	getCalls       []scriptedManagerCall
	reconnectCalls []scriptedManagerCall
	index          int
}

func (m *scriptedReconnectManager) Get(ctx context.Context, convID, serverName string) (mcpclient.Interface, error) {
	m.getCalls = append(m.getCalls, scriptedManagerCall{convID: convID, server: serverName})
	if len(m.clients) == 0 {
		return nil, errors.New("no scripted clients")
	}
	if m.index >= len(m.clients) {
		return m.clients[len(m.clients)-1], nil
	}
	return m.clients[m.index], nil
}

func (m *scriptedReconnectManager) Reconnect(ctx context.Context, convID, serverName string) (mcpclient.Interface, error) {
	m.reconnectCalls = append(m.reconnectCalls, scriptedManagerCall{convID: convID, server: serverName})
	if m.index < len(m.clients)-1 {
		m.index++
	}
	return m.clients[m.index], nil
}

func (m *scriptedReconnectManager) Touch(convID, serverName string) {}
func (m *scriptedReconnectManager) Options(ctx context.Context, serverName string) (*mcpcfg.MCPClient, error) {
	return nil, nil
}
func (m *scriptedReconnectManager) UseIDToken(ctx context.Context, serverName string) bool {
	return false
}
func (m *scriptedReconnectManager) WithAuthTokenContext(ctx context.Context, serverName string) context.Context {
	return ctx
}

type scriptedCallClient struct {
	reconnectingCallClient
	err    error
	result *mcpschema.CallToolResult
}

func (c *scriptedCallClient) CallTool(ctx context.Context, params *mcpschema.CallToolRequestParams, options ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	c.options = mcpclient.NewRequestOptions(options)
	if c.err != nil {
		return nil, c.err
	}
	if c.result != nil {
		return c.result, nil
	}
	return textToolResult("ok"), nil
}

func newReconnectTestRegistry(mgr *scriptedReconnectManager) *Registry {
	return &Registry{
		mgr:           mgr,
		cache:         map[string]*toolCacheEntry{},
		internal:      map[string]mcpclient.Interface{},
		recentResults: map[string]map[string]recentItem{},
	}
}

func textToolResult(text string) *mcpschema.CallToolResult {
	return &mcpschema.CallToolResult{
		Content: []mcpschema.CallToolResultContentElem{&mcpschema.TextContent{Type: "text", Text: text}},
	}
}

func errorToolResult(text string) *mcpschema.CallToolResult {
	isError := true
	return &mcpschema.CallToolResult{
		IsError: &isError,
		Content: []mcpschema.CallToolResultContentElem{&mcpschema.TextContent{Type: "text", Text: text}},
	}
}
