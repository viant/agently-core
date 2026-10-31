package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	identity "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/forge/backend/requestctx"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	registry "github.com/viant/agently-core/internal/tool/registry"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type validationMCPClient struct {
	mcpclient.Interface
	status, calls int
}

func (c *validationMCPClient) Initialize(context.Context, ...mcpclient.RequestOption) (*schema.InitializeResult, error) {
	var result schema.InitializeResult
	_ = json.Unmarshal([]byte(`{"protocolVersion":"2025-11-25","serverInfo":{"name":"fixture","version":"1"},"capabilities":{"tools":{}}}`), &result)
	return &result, nil
}
func (c *validationMCPClient) ListTools(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	return &schema.ListToolsResult{Tools: []schema.Tool{{Name: "read", InputSchema: schema.ToolInputSchema{Type: "object"}}}}, nil
}
func (c *validationMCPClient) CallTool(context.Context, *schema.CallToolRequestParams, ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	c.calls++
	payload := map[string]any{"status": "error", "message": "Both bounds must be supplied together.", "statusCode": c.status}
	raw, _ := json.Marshal(payload)
	failed := true
	return &schema.CallToolResult{IsError: &failed, StructuredContent: payload, Content: []schema.CallToolResultContentElem{schema.TextContent{Type: "text", Text: string(raw)}}}, nil
}

type registryToolHTTPClient struct {
	Client
	registry *registry.Registry
	progress bool
}

func (c registryToolHTTPClient) ExecuteTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	if c.progress {
		requestctx.EmitToolProgress(ctx, map[string]string{"phase": "started"})
	}
	return c.registry.Execute(ctx, name, args)
}

// Independently authored protocol fixtures exercise numeric application status,
// not HTTP transport status or message keyword inference.
func TestMCPValidationStatusSurvivesRegistryAndHTTP(t *testing.T) {
	for _, status := range []int{400, 422, 401, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := &validationMCPClient{status: status}
			mgr, err := manager.New(nil)
			require.NoError(t, err)
			require.NoError(t, mgr.RegisterLocal(context.Background(), "fixture", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "fixture", ToolsListVisibility: cfg.ToolsListVisibilityPublic}, func(context.Context) (mcpclient.Interface, error) { return client, nil }))
			t.Cleanup(func() { mgr.CloseConversation("") })
			reg, err := registry.NewWithManager(mgr)
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, "/v1/tools/fixture:read/execute", strings.NewReader(`{}`))
			req.SetPathValue("name", "fixture:read")
			response := httptest.NewRecorder()
			handleExecuteTool(registryToolHTTPClient{registry: reg})(response, req)
			require.Equal(t, status, response.Code, response.Body.String())
			require.Equal(t, 1, client.calls, "validation must not replay the source operation")
			if status == 400 || status == 422 {
				require.Contains(t, response.Body.String(), "Both bounds must be supplied together.")
			}
			streamRequest := httptest.NewRequest(http.MethodPost, "/v1/tools/fixture:read/execute", strings.NewReader(`{}`))
			streamRequest.SetPathValue("name", "fixture:read")
			streamRequest.Header.Set("Accept", "text/event-stream")
			streamResponse := httptest.NewRecorder()
			handleExecuteTool(registryToolHTTPClient{registry: reg, progress: true})(streamResponse, streamRequest)
			require.Contains(t, streamResponse.Body.String(), "event: error\n")
			require.Contains(t, streamResponse.Body.String(), fmt.Sprintf(`"status":%d`, status))
			require.Equal(t, 2, client.calls)
			_, sourceErr := reg.Execute(context.Background(), "fixture:read", nil)
			require.Error(t, sourceErr)
			datasourceResponse := httptest.NewRecorder()
			writeDatasourceError(datasourceResponse, sourceErr)
			require.Equal(t, status, datasourceResponse.Code)
			require.Equal(t, 3, client.calls)
			if status == 400 || status == 422 {
				denied := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/v1/tools/fixture:read/execute", strings.NewReader(`{}`))
				request.SetPathValue("name", "fixture:read")
				handleExecuteTool(authorityErrorToolClient{failure: errors.Join(sourceErr, identity.ErrResourceDenied)})(denied, request)
				require.Equal(t, http.StatusForbidden, denied.Code)
				require.NotContains(t, denied.Body.String(), "Both bounds")
				require.NotContains(t, denied.Body.String(), "restricted-definition")
				datasourceDenied := httptest.NewRecorder()
				writeDatasourceError(datasourceDenied, errors.Join(sourceErr, identity.ErrResourceDenied))
				require.Equal(t, http.StatusForbidden, datasourceDenied.Code)
				require.NotContains(t, datasourceDenied.Body.String(), "Both bounds")

			}

			if status == 401 || status == 403 {
				require.NotContains(t, streamResponse.Body.String(), "Both bounds")
			}

		})
	}
}
