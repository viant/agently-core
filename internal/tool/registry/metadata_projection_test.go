package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/mcp"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
	"github.com/viant/mcp/client/catalog"
)

type metadataProjectionManager struct {
	catalogPolicyManager
	page        *catalog.Page
	projections atomic.Int64
	snapshots   atomic.Int64
	credential  string
}

func (m *metadataProjectionManager) ToolCatalogSelect(ctx context.Context, conversation, server string, names []string, metadata bool, _ ...client.RequestOption) (string, []schema.Tool, error) {
	m.projections.Add(1)
	if authctx.EffectiveUserID(ctx) != "alice" || (m.credential != "" && authctx.MCPAuthToken(ctx, false) != m.credential) {
		return "denied-current", nil, nil
	}
	tools, err := m.page.Select(names, metadata)
	return "current-revision", tools, err
}
func (m *metadataProjectionManager) ToolCatalogSnapshot(context.Context, string, string, ...client.RequestOption) (string, []schema.Tool, error) {
	m.snapshots.Add(1)
	tools, err := m.page.Select(nil, false)
	return "current-revision", tools, err
}
func TestMetadataProjectionUsesPreparedNativeCatalogAndKeepsSelectedSchemas(t *testing.T) {
	wire := []byte(`{"tools":[{"name":"measure","description":"Synthetic measurement","inputSchema":{"type":"object","properties":{"scope":{"type":"string"}},"required":["scope"]},"outputSchema":{"type":"object","properties":{"rows":{"type":"array"}}},"_meta":{"ui":{"visibility":["model"]}}}]}`)
	page, err := catalog.New(wire, "service")
	require.NoError(t, err)
	tools, err := page.Select(nil, false)
	require.NoError(t, err)
	mgr := &metadataProjectionManager{page: page}
	mgr.getFunc = func(string, string) (client.Interface, error) { return &discoveryListClient{tools: tools}, nil }
	reg := &Registry{mgr: mgr}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	full := reg.DefinitionsWithContext(ctx)
	require.Len(t, full, 1)
	require.Zero(t, mgr.snapshots.Load())
	for i := 0; i < 2; i++ {
		metadata := reg.DefinitionMetadataWithContext(ctx)
		require.Len(t, metadata, 1)
		require.Equal(t, full[0].Name, metadata[0].Name)
		require.Equal(t, full[0].Description, metadata[0].Description)
		require.Equal(t, full[0].SourceMCPServer, metadata[0].SourceMCPServer)
		require.Nil(t, metadata[0].Parameters)
		require.Nil(t, metadata[0].OutputSchema)
		require.Empty(t, metadata[0].Required)
	}
	require.Zero(t, mgr.snapshots.Load(), "warm projection must not copy the full catalog")
	require.Len(t, mgr.getCallsSnapshot(), 1, "warm metadata does not rediscover a client")
	require.GreaterOrEqual(t, mgr.projections.Load(), int64(2))
	selected, err := reg.MatchDefinitionWithContextResult(ctx, "service:measure")
	require.NoError(t, err)
	require.Len(t, selected, 1)
	require.NotEmpty(t, selected[0].Parameters["properties"])
	require.Equal(t, []string{"scope"}, selected[0].Required)
	require.NotEmpty(t, selected[0].OutputSchema)
	visible, err := reg.MCPToolsVisible(ctx, []string{"service-measure"}, "model")
	require.NoError(t, err)
	require.True(t, visible["service-measure"])
	denied := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "bob"})
	require.Empty(t, reg.DefinitionMetadataWithContext(denied))
	require.Empty(t, reg.cache, "private projection never enters the shared registry cache")
}
func TestMetadataProjectionPreservesArtifactDescriptionWithoutOutputSchema(t *testing.T) {
	mgr := &outputSchemaManager{outputCaptureManager: outputCaptureManager{policy: &mcpcfg.MCPClient{ToolsListVisibility: mcpcfg.ToolsListVisibilityPublic, OutputArtifacts: map[string]mcpcfg.OutputArtifact{"Download": {Name: "file.xlsx"}}}}}
	reg := newReconnectTestRegistry(&mgr.scriptedReconnectManager)
	reg.mgr = mgr
	reg.cache["service/Download"] = &toolCacheEntry{def: llm.ToolDefinition{Name: "service/Download", OutputSchema: map[string]interface{}{"raw": true}}, mcpDef: schema.Tool{Name: "Download"}}
	metadata := reg.DefinitionMetadataWithContext(context.Background())
	require.Len(t, metadata, 1)
	require.Contains(t, metadata[0].Description, "owned immutable artifact")
	require.Nil(t, metadata[0].OutputSchema)
	full, ok := reg.GetDefinitionWithContext(context.Background(), "service:Download")
	require.True(t, ok)
	encoded, err := json.Marshal(full.OutputSchema)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "resources")
}

func TestMetadataProjectionKeepsColdInternalFullDefinitionsAuthoritative(t *testing.T) {
	page, err := catalog.New([]byte(`{"tools":[{"name":"measure","inputSchema":{"type":"object","properties":{"scope":{"type":"string"}},"required":["scope"]},"outputSchema":{"type":"object","properties":{"rows":{"type":"array"}}}}]}`), "service")
	require.NoError(t, err)
	mgr := &metadataProjectionManager{page: page}
	tools, err := page.Select(nil, false)
	require.NoError(t, err)
	reg := &Registry{mgr: mgr, cache: map[string]*toolCacheEntry{}, internal: map[string]client.Interface{"service": &discoveryListClient{tools: tools}}}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	metadata := reg.DefinitionMetadataWithContext(ctx)
	require.Len(t, metadata, 1)
	require.Nil(t, metadata[0].Parameters)
	require.Nil(t, metadata[0].OutputSchema)
	require.NotEmpty(t, reg.cache, "cold local providers retain their existing full discovery path")
	for _, entry := range reg.cache {
		require.NotEmpty(t, entry.def.Parameters["properties"])
		require.NotEmpty(t, entry.def.OutputSchema)
	}
	require.Zero(t, mgr.projections.Load(), "local providers are owned by their local client, not the remote manager")
	full, ok := reg.GetDefinitionWithContext(ctx, "service:measure")
	require.True(t, ok)
	require.NotEmpty(t, full.Parameters["properties"])
	require.Equal(t, []string{"scope"}, full.Required)
	require.NotEmpty(t, full.OutputSchema)
}

func TestMetadataProjectionUsesCurrentCredentialWithoutRetainingCandidates(t *testing.T) {
	page, err := catalog.New([]byte(`{"tools":[{"name":"measure","inputSchema":{"type":"object"}}]}`), "service")
	require.NoError(t, err)
	mgr := &metadataProjectionManager{page: page, credential: "synthetic-first"}
	reg := &Registry{mgr: mgr}
	first := authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "synthetic-first")
	require.Len(t, reg.DefinitionMetadataWithContext(first), 1)
	changed := authctx.WithBearer(first, "synthetic-second")
	require.Empty(t, reg.DefinitionMetadataWithContext(changed), "a different credential cannot borrow earlier admitted metadata")
	mgr.credential = "synthetic-second"
	require.Len(t, reg.DefinitionMetadataWithContext(changed), 1)
	require.Empty(t, reg.DefinitionMetadataWithContext(first), "the current owner revocation must remain authoritative")
	require.Empty(t, reg.cache)
}

func TestMetadataProjectionThroughNativeManagerPreservesWarmCatalog(t *testing.T) {
	t.Setenv("AGENTLY_MCP_SERVERS", "server")
	var lists atomic.Int64
	tools := make([]any, 519)
	for i := range tools {
		tools[i] = map[string]any{"name": fmt.Sprintf("measure_%03d", i), "description": "Synthetic measurement", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"scope": map[string]any{"type": "string"}}, "required": []string{"scope"}}, "outputSchema": map[string]any{"type": "object", "properties": map[string]any{"rows": map[string]any{"type": "array"}}}, "_meta": map[string]any{"ui": map[string]any{"visibility": []string{"model"}}}}
	}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "synthetic-projection-session")
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "synthetic", "version": "1"}}
		case "tools/list":
			lists.Add(1)
			result = map[string]any{"tools": tools, "cacheScope": "private", "ttlMs": 0}
		default:
			w.WriteHeader(202)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer source.Close()
	provider := &surfaceCatalogProvider{options: &mcpcfg.MCPClient{ToolsListVisibility: mcpcfg.ToolsListVisibilityPrivate, ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: source.URL}}}}}
	owner, err := manager.New(provider)
	require.NoError(t, err)
	reg := &Registry{mgr: owner, cache: map[string]*toolCacheEntry{}}
	ctx := requestctx.WithConversationID(authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "synthetic-catalog-credential"), "synthetic-chat")
	ctx = manager.WithToolCatalogReadSnapshot(manager.WithNamesMemo(ctx))
	full := reg.DefinitionsWithContext(ctx)
	require.Len(t, full, 519)
	for iteration := 0; iteration < 3; iteration++ {
		metadata := reg.DefinitionMetadataWithContext(ctx)
		require.Len(t, metadata, len(full))
		for i, candidate := range metadata {
			require.Equal(t, full[i].Name, candidate.Name)
			require.Equal(t, full[i].Description, candidate.Description)
			require.Nil(t, candidate.Parameters)
			require.Nil(t, candidate.OutputSchema)
			require.Empty(t, candidate.Required)
		}
		for _, index := range []int{0, 1, 100, 200, 500, 518} {
			selected, err := reg.MatchDefinitionWithContextResult(ctx, fmt.Sprintf("server:measure_%03d", index))
			require.NoError(t, err)
			require.Len(t, selected, 1)
			require.Contains(t, selected[0].Parameters["properties"], "scope")
			require.Equal(t, []string{"scope"}, selected[0].Required)
			require.NotEmpty(t, selected[0].OutputSchema["properties"])
		}
	}
	require.EqualValues(t, 1, lists.Load(), "warm metadata and exact hydration reuse the existing manager page")
}
