package resource

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp"
	protocolclient "github.com/viant/mcp-protocol/client"
	namespaces "github.com/viant/mcp-protocol/extension/namespaces"
	"github.com/viant/mcp-protocol/schema"
	protocolserver "github.com/viant/mcp-protocol/server"
	mcpclient "github.com/viant/mcp/client"
	mcpserver "github.com/viant/mcp/server"
)

// The fixture uses the same SDK adapter and standard in-memory notification
// bridge as the existing managing provider. No catalog data travels in events.
type indexNotificationClient struct {
	mcpclient.Interface
	handler *mcpserver.Handler
	bridge  *indexNotificationBridge
}

func (c *indexNotificationClient) SetNotificationHandler(h protocolclient.Handler) {
	c.bridge.mu.Lock()
	c.bridge.receiver = h
	c.bridge.mu.Unlock()
}
func (c *indexNotificationClient) Close() error { c.handler.Close(); return nil }

type indexNotificationBridge struct {
	mu       sync.RWMutex
	receiver protocolclient.Handler
}

func (b *indexNotificationBridge) Notify(ctx context.Context, n *jsonrpc.Notification) error {
	b.mu.RLock()
	h := b.receiver
	b.mu.RUnlock()
	if h != nil {
		h.OnNotification(ctx, n)
	}
	return nil
}
func (b *indexNotificationBridge) Send(_ context.Context, r *jsonrpc.Request) (*jsonrpc.Response, error) {
	return &jsonrpc.Response{Id: r.Id, Error: jsonrpc.NewMethodNotFound("fixture callback unavailable", nil)}, nil
}

var _ transport.Notifier = (*indexNotificationBridge)(nil)

func TestSharedNamespaceRawMCPDefinitionsUseExistingReaderManagerAndLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := func(label string) *httptest.Server {
		handler := protocolserver.WithDefaultHandler(ctx, func(h *protocolserver.DefaultHandler) error {
			return protocolserver.RegisterTool[struct{}, struct {
				Message string `json:"message"`
			}](h.Registry, "ping", "Actual child tool", func(context.Context, struct{}) (*schema.CallToolResult, *jsonrpc.Error) {
				return &schema.CallToolResult{Content: []schema.CallToolResultContentElem{schema.TextContent{Type: "text", Text: `{"message":"` + label + `"}`}}}, nil
			})
		})
		server, err := mcpserver.New(mcpserver.WithImplementation(schema.Implementation{Name: label, Version: "1"}), mcpserver.WithStreamableURI("/mcp"), mcpserver.WithRootRedirect(false), mcpserver.WithNewHandler(handler))
		require.NoError(t, err)
		server.UseStreamableHTTP(true)
		httpServer := httptest.NewServer(server.HTTP(ctx, "").Handler)
		t.Cleanup(httpServer.Close)
		return httpServer
	}
	first, second := child("first"), child("second")
	config := func(server *httptest.Server) json.RawMessage {
		raw, err := json.Marshal(&cfg.MCPClient{ClientOptions: &mcp.ClientOptions{Name: "authored client label", ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL + "/mcp"}}}})
		require.NoError(t, err)
		return raw
	}
	var mu sync.RWMutex
	definitions := map[string]json.RawMessage{"mcp://external/child": config(first)}
	namespaceURI, _ := namespaces.URI("opaque configuration collection")
	result := func(value any) (*schema.CallToolResult, *jsonrpc.Error) {
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		return &schema.CallToolResult{Content: []schema.CallToolResultContentElem{schema.TextContent{Type: "text", Text: string(raw)}}}, nil
	}
	factory := protocolserver.WithDefaultHandler(ctx, func(h *protocolserver.DefaultHandler) error {
		if err := protocolserver.RegisterTool[namespaces.ListRequest, namespaces.ListResult](h.Registry, namespaces.ListToolName, "Configuration namespaces", func(context.Context, namespaces.ListRequest) (*schema.CallToolResult, *jsonrpc.Error) {
			return result(namespaces.ListResult{Namespaces: []namespaces.Namespace{{URI: namespaceURI, Name: "Display name is not URI authority"}}})
		}); err != nil {
			return err
		}
		if err := protocolserver.RegisterTool[namespaces.GetRequest, namespaces.GetResult](h.Registry, namespaces.GetToolName, "Configuration index", func(_ context.Context, in namespaces.GetRequest) (*schema.CallToolResult, *jsonrpc.Error) {
			if in.URI != namespaceURI {
				return nil, &jsonrpc.Error{Code: -32602, Message: "invalid locator"}
			}
			mu.RLock()
			rows := []namespaces.Resource{}
			for uri := range definitions {
				rows = append(rows, namespaces.Resource{URI: uri, Kind: "mcp", Name: "Authored connection"})
			}
			mu.RUnlock()
			sort.Slice(rows, func(i, j int) bool { return rows[i].URI < rows[j].URI })
			page, next, err := namespaces.Paginate(rows, in.Cursor, 1)
			if err != nil {
				return nil, &jsonrpc.Error{Code: -32602, Message: err.Error()}
			}
			return result(namespaces.GetResult{Resources: page, NextCursor: next})
		}); err != nil {
			return err
		}
		return protocolserver.RegisterTool[namespaces.ResourceGetRequest, struct{}](h.Registry, namespaces.ResourceGetToolName, "Authored configuration", func(_ context.Context, in namespaces.ResourceGetRequest) (*schema.CallToolResult, *jsonrpc.Error) {
			mu.RLock()
			raw, ok := definitions[in.URI]
			mu.RUnlock()
			if !ok {
				yes := true
				return &schema.CallToolResult{IsError: &yes}, nil
			}
			return result(raw)
		})
	})
	server, err := mcpserver.New(mcpserver.WithImplementation(schema.Implementation{Name: "configuration root", Version: "1"}), mcpserver.WithNewHandler(factory))
	require.NoError(t, err)
	bridge := &indexNotificationBridge{}
	mgr, err := manager.New(manager.NewRepoProvider(manager.WithRepoStateStore(fsstore.NewStateStore(t.TempDir()))))
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	require.NoError(t, mgr.RegisterLocal(ctx, "index", &cfg.MCPClient{PrimitiveProviderIdentity: "root", ClientOptions: &mcp.ClientOptions{}}, func(ctx context.Context) (mcpclient.Interface, error) {
		handler := server.NewHandler(ctx, bridge).(*mcpserver.Handler)
		return &indexNotificationClient{Interface: mcpserver.NewAdapter(handler), handler: handler, bridge: bridge}, nil
	}))
	actor, verify := PublicReadActors()
	gateway := NewGateway(mgr, actor, verify, "consumer")
	defer gateway.Close()
	reader, err := NewMCPReferenceReader(gateway, MCPDefinitionSource{ConnectionName: "index", ProviderIdentity: "root", Namespace: "external"}, nil)
	require.NoError(t, err)
	bound, err := mgr.BindDefinitionReader(reader)
	require.NoError(t, err)
	require.True(t, bound)
	names, err := reader.List(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"child"}, names)
	loaded, err := reader.Load(ctx, "mcp://external/child")
	require.NoError(t, err)
	require.Equal(t, "child", loaded.ResolvedIdentity.ServiceName)
	require.Equal(t, "root", loaded.ResolvedIdentity.ProviderIdentity)
	callChild := func(name string) string {
		client, err := mgr.Get(ctx, "", name)
		require.NoError(t, err)
		raw, err := toolRaw(ctx, client, "ping", struct{}{})
		require.NoError(t, err)
		var body struct {
			Message string `json:"message"`
		}
		require.NoError(t, json.Unmarshal(raw, &body))
		return body.Message
	}
	require.Equal(t, "first", callChild("child"))
	require.Equal(t, "first", callChild("mcp://external/child"))
	original, err := mgr.Get(ctx, "", "child")
	require.NoError(t, err)
	mu.Lock()
	definitions["mcp://external/child"] = config(second)
	definitions["mcp://external/added"] = config(first)
	mu.Unlock()
	require.NoError(t, bridge.Notify(ctx, &jsonrpc.Notification{Jsonrpc: "2.0", Method: "notifications/resources/list_changed"}))
	names, err = reader.List(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"added", "child"}, names)
	require.Equal(t, "second", callChild("mcp://external/child"))
	updated, err := mgr.Get(ctx, "", "child")
	require.NoError(t, err)
	require.NotSame(t, original, updated, "same alias must reconnect on actual configuration fingerprint change")
	require.Equal(t, "first", callChild("added"))
	mu.Lock()
	delete(definitions, "mcp://external/child")
	mu.Unlock()
	require.NoError(t, bridge.Notify(ctx, &jsonrpc.Notification{Jsonrpc: "2.0", Method: "notifications/resources/list_changed"}))
	names, err = reader.List(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"added"}, names)
	_, err = mgr.Get(ctx, "", "child")
	require.Error(t, err, "removed owning configuration must not fall back to workspace repository")
}
