package window

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/viant/agently-core/workspace"
	forgemcp "github.com/viant/agently-core/adapter/mcp/primitive"
	windowprotocol "github.com/viant/agently-core/protocol/window"
	forgesvc "github.com/viant/agently-core/service/primitiveprovider"
	reportspec "github.com/viant/forge/backend/reporting/spec"
	forgetypes "github.com/viant/forge/backend/types"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

// This executor crosses the actual Forge MCP handler, rather than returning a
// hand-authored JSON approximation of a provider's result envelope.
type portableHandlerExecutor struct{ handler *forgemcp.Handler }

func (e portableHandlerExecutor) Execute(ctx context.Context, name string, inputs map[string]interface{}) (string, error) {
	if !strings.HasPrefix(name, "provider-server:") {
		return "", fmt.Errorf("unapproved provider binding")
	}
	result, rpcErr := e.handler.CallTool(ctx, &jsonrpc.TypedRequest[*schema.CallToolRequest]{Request: &schema.CallToolRequest{Params: schema.CallToolRequestParams{Name: strings.TrimPrefix(name, "provider-server:"), Arguments: inputs}}})
	if rpcErr != nil {
		return "", fmt.Errorf("provider tool: %v", rpcErr)
	}
	if result.StructuredContent != nil {
		data, err := json.Marshal(result.StructuredContent)
		return string(data), err
	}
	for _, item := range result.Content {
		if text, ok := item.(schema.TextContent); ok {
			return text.Text, nil
		}
	}
	return "", fmt.Errorf("provider returned no JSON")
}
func TestConnectedPortableForgeProvider(t *testing.T) {
	for _, structured := range []bool{false, true} {
		name := "text"
		if structured {
			name = "structured"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			denied := false
			fetches := 0
			definition := &windowprotocol.Definition{ContractVersion: 1, DefinitionRevision: "published-1", Window: &forgetypes.Window{View: forgetypes.View{Content: &forgetypes.Container{}}}, DataSources: map[string]*windowprotocol.DataSource{
				"records": {ID: "records", DataSource: forgetypes.DataSource{Selectors: &forgetypes.Selectors{Data: "body.rows", DataInfo: "body"}}, Backend: &windowprotocol.Backend{Kind: "provider", Method: windowprotocol.FetchTool, MCPRequest: map[string]any{"queryPath": "request"}, Pinned: map[string]any{"windowKey": "report", "dataSourceId": "records", "definitionRevision": "published-1"}}},
			}}
			limit, offset := 50, 0
			definition.Report = &reportspec.ReportSpec{Version: 1, Kind: "reportSpec", Title: "Published native report", Source: reportspec.Source{Kind: "dashboard.reportBuilder", ContainerID: "report", StateKey: "report", DataSourceRef: "records"}, Parameters: &reportspec.Parameters{ViewMode: "table", PageSize: 50, OrderDir: "asc"}, LayoutIntent: &reportspec.LayoutIntent{Kind: "stack", ResultPanePosition: "main", BlockOrder: []string{"results"}}, Refinements: []map[string]any{}, CalculatedFields: []map[string]any{}, Datasets: []reportspec.Dataset{{ID: "primary", DataSourceRef: "records", Request: reportspec.RequestPayload{Limit: &limit, Offset: &offset}}}, Blocks: []reportspec.Block{{ID: "results", Kind: "tableBlock", DatasetRef: "primary", Columns: []reportspec.TableColumn{{Key: "count", Label: "Count"}}}}}
			provider := &forgesvc.PrimitiveProvider{Authority: forgesvc.PrimitiveAuthorityFuncs{AuthenticateFunc: func(context.Context) (string, error) { return "verified-account-lease", nil }, AuthorizeFunc: func(context.Context, string, string, string, string) error {
				if denied {
					return fmt.Errorf("denied")
				}
				return nil
			}}, Host: forgesvc.PrimitiveHostFuncs{
				CatalogFunc: func(context.Context, *windowprotocol.CatalogInput) (*windowprotocol.Catalog, error) {
					return &windowprotocol.Catalog{ContractVersion: 1, CatalogRevision: "catalog-1", Windows: []windowprotocol.WindowSummary{{Key: "report", Title: "Report"}}}, nil
				},
				DefinitionFunc: func(context.Context, *windowprotocol.DefinitionInput) (*windowprotocol.Definition, error) { return definition, nil },
				FetchFunc: func(_ context.Context, in *windowprotocol.FetchInput) (windowprotocol.FetchOutput, error) {
					fetches++
					if in.WindowKey != "report" || in.DataSourceID != "records" || in.DefinitionRevision != "published-1" {
						return nil, fmt.Errorf("caller overrode dispatch identity")
					}
					return json.RawMessage(`{"body":{"rows":[{"count":7}],"hasMore":false}}`), nil
				},
			}}
			handler, err := forgemcp.NewProviderHandler(forgesvc.NewService(&forgesvc.Config{PrimitiveProvider: provider, UseData: structured}))(ctx, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			forgeHandler := handler.(*forgemcp.Handler)
			forgeHandler.ClientInitialize = &schema.InitializeRequestParams{ProtocolVersion: "2025-06-18"}
			previous := workspace.Root()
			workspace.SetRoot(t.TempDir())
			ConfigureRemoteWindowAuthorization(nil)
			ConfigureRemoteWindowProvider(portableHandlerExecutor{forgeHandler}, []byte("windowProviders:\n  - id: studio\n    type: mcp\n    serverRef: provider-server\n    catalogTool: forgeWindowCatalog\n    windowTool: forgeWindowDefinition\n    datasourceTool: forgeDatasourceFetch\n"))
			t.Cleanup(func() {
				workspace.SetRoot(previous)
				ConfigureRemoteWindowProvider(nil, nil)
				ConfigureRemoteWindowAuthorization(nil)
			})
			catalog, err := ListRemoteWindows(ctx, "studio", "", "")
			if err != nil || catalog.Revision != "catalog-1" || len(catalog.Windows) != 1 {
				t.Fatalf("catalog=%+v err=%v", catalog, err)
			}
			win, err := LoadRemoteWindow(ctx, "studio", "report")
			if err != nil {
				t.Fatal(err)
			}
			assembled, marshalErr := json.Marshal(win)
			if marshalErr != nil || !strings.Contains(string(assembled), "dashboard.reportRuntime") || !strings.Contains(string(assembled), "datasetBindings") {
				t.Fatalf("native report did not reach window renderer: %s %v", assembled, marshalErr)
			}
			if win.DataSource["records"].Service == nil {
				t.Fatal("provider datasource was not wired into Forge window")
			}
			if !strings.Contains(win.DataSource["records"].Service.URI, "definitionRevision=published-1") {
				t.Fatal("generated window fetch did not bind publication revision")
			}
			report, revision, err := LoadRemoteReport(ctx, "studio", "report")
			if err != nil || revision != "published-1" || report.Title != "Published native report" || report.Source.DataSourceRef != "records" {
				t.Fatalf("native report=%+v revision=%s err=%v", report, revision, err)
			}
			rows, err := FetchRemoteDatasource(ctx, "studio", "report", "records", map[string]interface{}{"windowKey": "attacker", "dataSourceId": "other", "definitionRevision": "old"})
			if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["count"] != float64(7) {
				t.Fatalf("rows=%+v err=%v", rows, err)
			}
			if _, err = FetchRemoteDatasourceAtRevision(ctx, "studio", "report", "records", "old-publication", nil); err == nil {
				t.Fatal("old native report revision executed current datasource")
			}
			if fetches != 1 {
				t.Fatal("stale publication reached downstream provider")
			}
			denied = true
			if _, err = FetchRemoteDatasource(ctx, "studio", "report", "records", nil); err == nil {
				t.Fatal("provider denial bypassed")
			}
			if fetches != 1 {
				t.Fatal("denied datasource reached downstream executor")
			}
		})
	}
}
