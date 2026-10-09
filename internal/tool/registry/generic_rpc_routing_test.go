package tool

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/service/datasource"
	"github.com/viant/agently-core/service/reportdefinition"
	"github.com/viant/forge/backend/types"
	"github.com/viant/jsonrpc"
	schema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	authtransport "github.com/viant/mcp/client/auth/transport"
	"testing"
)

type genericRoutingRPC struct {
	t     *testing.T
	calls int
}

func (f *genericRoutingRPC) Notify(context.Context, *jsonrpc.Notification) error { return nil }
func (f *genericRoutingRPC) Send(ctx context.Context, r *jsonrpc.Request) (*jsonrpc.Response, error) {
	if r.Method == schema.MethodServerDiscover {
		return &jsonrpc.Response{Jsonrpc: jsonrpc.Version, Result: json.RawMessage(`{"cacheScope":"public","resultType":"complete","capabilities":{},"supportedVersions":["2026-07-28"],"ttlMs":0}`)}, nil
	}
	require.Equal(f.t, schema.MethodToolsCall, r.Method)
	require.Equal(f.t, "fresh-access-token", ctx.Value(authtransport.ContextAuthTokenKey))
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
		Meta      map[string]any `json:"_meta"`
	}
	require.NoError(f.t, json.Unmarshal(r.Params, &p))
	require.Equal(f.t, "rows", p.Name)
	require.NotContains(f.t, p.Meta, "viant.datly/component")
	require.Equal(f.t, "caller-value", p.Arguments["arbitrary_meta"])
	f.calls++
	return &jsonrpc.Response{Jsonrpc: jsonrpc.Version, Result: json.RawMessage(`{"structuredContent":{"data":[{"project":"generic","targetUnits":7}],"hasMore":false},"content":[]}`)}, nil
}

// Production Registry and DataSource Fetch dispatch through the real MCP
// client serializer; a Datly-classified sibling cannot classify this server.
func TestGenericRPCRegistryDatasourceAndReportWithoutVendorMetadata(t *testing.T) {
	tr := &genericRoutingRPC{t: t}
	client := mcpclient.New("generic", "1", tr, mcpclient.WithProtocolVersion(schema.LatestProtocolVersion))
	// The in-memory transport handles real client discovery before effect RPCs.
	remote := client
	reg := &Registry{mgr: &executeAuthManagerStub{client: remote}, internal: map[string]mcpclient.Interface{}, cache: map[string]*toolCacheEntry{}, recentResults: map[string]map[string]recentItem{}}
	reg.SetComponentProducerClassifier(func(server string) bool { return server == "ordinary" })
	store := datasource.NewMemoryStore()
	store.Put(&dsproto.DataSource{ID: "generic", DataSource: types.DataSource{Selectors: &types.Selectors{Data: "data", DataInfo: "$"}}, Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "ordinary", Method: "rows"}})
	svc := datasource.New(datasource.Options{Store: store, Executor: reg})
	out, err := svc.Fetch(context.Background(), "generic", map[string]any{"arbitrary_meta": "caller-value"}, datasource.FetchOptions{})
	require.NoError(t, err)
	require.Len(t, out.Rows, 1)
	require.Equal(t, "generic", out.Rows[0]["project"])
	require.Equal(t, 1, tr.calls)
	raw, err := json.Marshal(map[string]any{"data": out.Rows, "hasMore": false})
	require.NoError(t, err)
	result, err := reportdefinition.DecodeMCPResult(reportdefinition.ResultContract{Shape: "records", RowPath: "data", HasMorePath: "hasMore"}, []reportdefinition.Column{{Name: "project", Type: "string", Role: "dimension"}, {Name: "targetUnits", Type: "integer", Role: "measure"}}, raw)
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	require.True(t, result.HasMoreKnown)
	require.False(t, result.HasMore)
	reg.authorizationGuard = func(context.Context, string, map[string]interface{}) error {
		return errors.New("synthetic action denied")
	}
	denied, err := svc.Fetch(context.Background(), "generic", map[string]any{"arbitrary_meta": "caller-value"}, datasource.FetchOptions{BypassCache: true})
	require.ErrorContains(t, err, "synthetic action denied")
	require.Nil(t, denied)
	require.Equal(t, 1, tr.calls)
}
