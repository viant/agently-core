package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	httpui "github.com/viant/agently-core/adapter/http/ui"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	service "github.com/viant/agently-core/service/primitiveprovider"
	resource "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/forge/backend/types"
	"github.com/viant/jsonrpc"
	sdkmcp "github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type openSource struct{ raw json.RawMessage }

func (s openSource) Candidates(context.Context, identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	return []identity.ResourceCandidate{{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(s.raw)}}, nil
}
func (s openSource) ReadCandidate(context.Context, identity.ResourceURI, identity.ResourceCandidate) (json.RawMessage, error) {
	return s.raw, nil
}

type openPolicy struct{}

func (openPolicy) SelectRevision(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "owning-source"}, nil
}

type openPresentation struct {
	editor atomic.Bool
	calls  atomic.Int32
}

func (p *openPresentation) Resolve(_ context.Context, r *permittedview.Request) (*permittedview.Snapshot, error) {
	if len(r.ResourceIDs) != 0 || len(r.StringResourceIDs) != 0 || len(r.RequestedCapabilities) != 0 || len(r.RequestedGlobalCapabilities) != 0 || r.ResourceType != "" {
		panic("consumer requested entity permission")
	}
	p.calls.Add(1)
	roles := []string{}
	if p.editor.Load() {
		roles = []string{"editor"}
	}
	return &permittedview.Snapshot{AuthorizationVersion: "principal", ExpiresAt: time.Now().Add(time.Minute), Principal: map[string]any{"roles": roles}}, nil
}

// Actual owning SDK server/client/manager supplies metadata. Opening a window
// does not fetch any selected record or infer its access from UI capability hints.
func TestNativeWindowOpenUsesOwningMCPAndPresentationOnly(t *testing.T) {
	ctx := context.Background()
	var denied atomic.Bool
	var gets atomic.Int32
	window := &types.Window{}
	require.NoError(t, json.Unmarshal([]byte(`{"windowKey":"records","authorization":{"scope":"resource","resource":{"type":"record","id":{"source":"resource","selector":"recordId"}},"requestedCapabilities":["read","write"]},"dataSource":{"owner":{"service":{"endpoint":"owning-mcp","uri":"/records"},"parameters":[{"name":"recordId","from":"windowForm"}]}},"view":{"content":{"id":"approved","containers":[{"id":"edit","visibleWhen":{"source":"authorization","field":"principal.roles","contains":"editor"}},{"id":"plain"}]}}}`), window))
	descriptor := json.RawMessage(`{"id":"owner","backend":{"kind":"mcp_tool","service":"owning-mcp","method":"read"},"parameters":[{"name":"recordId","from":"windowForm"}]}`)
	descriptorFP, err := types.WindowDescriptorFingerprint(descriptor)
	require.NoError(t, err)
	window.ResourceDependencies = map[string]string{"owner": descriptorFP}
	variant := types.WindowResourceVariant{Window: window, DataSources: map[string]json.RawMessage{"owner": descriptor}}
	fp, err := types.WindowVariantFingerprint(variant)
	require.NoError(t, err)
	raw, err := json.Marshal(types.WindowResourceEnvelope{SchemaVersion: 2, Format: types.WindowBundleFormat, Targets: []types.WindowTargetBinding{{Target: types.WindowTarget{}, Variant: fp}}, Variants: map[string]types.WindowResourceVariant{fp: variant}})
	require.NoError(t, err)
	actor, verify := resource.PublicReadActors()
	provider, err := resource.NewLocalProvider(resource.LocalConfig{ProviderIdentity: "owner", Actor: actor, Verify: verify, Authorize: func(_ context.Context, _ identity.VerifiedActor, _ identity.ResourceURI, operation string) error {
		if operation == "resource.get" {
			gets.Add(1)
		}
		if denied.Load() && operation == "resource.get" {
			return identity.ErrResourceDenied
		}
		return nil
	}, Bindings: []resource.LocalResourceBinding{{URI: "window://example/records", FormatVersion: 2, Resolver: func(context.Context, identity.VerifiedActor, string) (*identity.ResourceResolver, error) {
		return &identity.ResourceResolver{ProviderIdentity: "owner", Source: openSource{raw}, Policy: openPolicy{}}, nil
	}}}, Validators: map[string]resource.LocalResourceValidator{"window": resource.ValidateWindowBundle}})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	require.NoError(t, mgr.RegisterLocal(ctx, "owner", &mcpcfg.MCPClient{ClientOptions: &sdkmcp.ClientOptions{Name: "owner-test", Version: "1"}, PrimitiveProviderIdentity: "owner"}, func(context.Context) (mcpclient.Interface, error) { return resource.NewLocalMCPClient(provider) }))
	gateway := resource.NewGateway(mgr, actor, verify, "consumer")
	defer gateway.Close()
	proof, err := types.NewWindowTargetHMAC([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	catalog := &resource.WindowCatalog{Gateway: gateway, TargetProof: proof, DefaultNamespace: "example", DefaultProviderIdentity: "owner"}
	principal := &openPresentation{}
	runtime := permittedview.NewRuntime(principal)
	svc := service.NewService(&service.Config{NamespaceResolver: func(context.Context) (service.NamespaceIdentity, error) {
		return service.NamespaceIdentity{Namespace: "default", SessionBound: true}, nil
	}, WindowDefinitions: catalog, WindowUIProjection: func(ctx context.Context, input *permittedview.BoundView) (*types.Window, error) {
		result, err := runtime.Project(ctx, input.Window, input)
		if err != nil {
			return nil, err
		}
		return result.Window, nil
	}})
	server := httptest.NewServer(http.HandlerFunc(svc.Hub().ServeWS))
	defer server.Close()
	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer socket.Close()
	require.NoError(t, socket.WriteJSON(map[string]any{"type": "ui.hello", "clientId": "fixture-ui"}))
	require.Eventually(t, func() bool { return len(svc.Hub().ListClients("default")) > 0 }, time.Second, time.Millisecond)
	delivered := make(chan map[string]any, 4)
	go func() {
		for {
			var command struct {
				ID     string         `json:"id"`
				Params map[string]any `json:"params"`
			}
			if socket.ReadJSON(&command) != nil {
				return
			}
			delivered <- command.Params
			_ = socket.WriteJSON(map[string]any{"id": command.ID, "ok": true, "result": map[string]any{"windowId": "records-instance"}})
		}
	}()
	handlerValue, err := NewHandler(svc)(ctx, nil, nil, nil)
	require.NoError(t, err)
	handler := handlerValue.(*Handler)
	handler.ClientInitialize = &schema.InitializeRequestParams{ProtocolVersion: "2025-06-18"}
	parameters := map[string]any{"recordId": "arbitrary-owner-selected-id", "opaque": "unchanged"}
	call := func() (*schema.CallToolResult, *jsonrpc.Error) {
		return handler.CallTool(ctx, &jsonrpc.TypedRequest[*schema.CallToolRequest]{Request: &schema.CallToolRequest{Params: schema.CallToolRequestParams{Name: "forgeWindowOpen", Arguments: map[string]any{"clientId": "fixture-ui", "windowKey": "records", "parameters": parameters, "options": map[string]any{"inlineMetadata": map[string]any{"view": "forged"}}}}}})
	}
	_, rpcErr := call()
	require.Nil(t, rpcErr)
	first := <-delivered
	require.Equal(t, parameters, first["parameters"])
	metadata := first["options"].(map[string]any)["inlineMetadata"].(map[string]any)
	require.Equal(t, "approved", metadata["view"].(map[string]any)["content"].(map[string]any)["id"])
	require.Len(t, metadata["view"].(map[string]any)["content"].(map[string]any)["containers"], 1)
	ownerDatasource := metadata["dataSource"]
	principal.editor.Store(true)
	_, rpcErr = call()
	require.Nil(t, rpcErr)
	next := <-delivered
	nextMetadata := next["options"].(map[string]any)["inlineMetadata"].(map[string]any)
	require.Len(t, nextMetadata["view"].(map[string]any)["content"].(map[string]any)["containers"], 2)
	require.Equal(t, ownerDatasource, nextMetadata["dataSource"])
	require.Equal(t, parameters, next["parameters"])
	require.Equal(t, int32(2), principal.calls.Load(), "one role projection per open; no final entity/preflight gate")
	// Eventless owning transport is explicitly reconnected after its policy changes.
	denied.Store(true)
	mgr.CloseConversation("")
	before := gets.Load()
	_, rpcErr = call()
	require.NotNil(t, rpcErr)
	require.Greater(t, gets.Load(), before, "denial comes from actual owning MCP get")
	require.Empty(t, delivered, "upstream denial must not release UI command")
	httpHandler := httpui.NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, httpui.WithWindowResourceProvider(svc))
	response := httptest.NewRecorder()
	httpHandler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/records?applyPermission=true", nil))
	require.Equal(t, http.StatusNotFound, response.Code)
}
