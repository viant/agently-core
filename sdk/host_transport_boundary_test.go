package sdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/sdk/api"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/mcp-protocol/schema"
)

type boundaryBackend struct {
	*dsStubBackend
	toolCalls, resourceCalls int
}

func (b *boundaryBackend) ExecuteTool(context.Context, string, map[string]interface{}) (string, error) {
	b.toolCalls++
	return "", nil
}

func (b *boundaryBackend) ReadMCPResource(context.Context, string, string) (*schema.ReadResourceResult, error) {
	b.resourceCalls++
	return &schema.ReadResourceResult{}, nil
}

// The public HTTP surface is an authenticated host adapter. Requests cannot
// reach an MCP-backed datasource or report tool before workspace admission.
func TestHostTransportRejectsUnauthenticatedDataAndReportCalls(t *testing.T) {
	backend := &boundaryBackend{dsStubBackend: &dsStubBackend{}}
	handler := NewHandler(backend, WithAuth(&svcauth.Config{
		Enabled: true, CookieName: "agently_session", Local: &svcauth.Local{Enabled: false},
	}, svcauth.NewManager(0, nil)))
	for _, tc := range []struct{ path, body string }{
		{"/v1/api/datasources/rows/fetch?cohort=1&windowId=window-1", `{"inputs":{},"endpoint":"https://untrusted.example/mcp","token":"forged"}`},
		{"/v1/tools/reporting%3Arun_report/execute", `{"resource":{"uri":"report://owned/widgets"},"endpoint":"https://untrusted.example/mcp","token":"forged"}`},
		{"/v1/tools/execute", `{"name":"reporting:run_report","args":{"endpoint":"https://untrusted.example/mcp"}}`},
		{"/v1/api/mcp/resources/read?server=studio&uri=report://owned/widgets", ""},
	} {
		method := http.MethodPost
		if tc.body == "" {
			method = http.MethodGet
		}
		request := httptest.NewRequest(method, tc.path, strings.NewReader(tc.body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status=%d body=%s", tc.path, response.Code, response.Body.String())
		}
	}
	if backend.fetchCalls != 0 || backend.toolCalls != 0 || backend.resourceCalls != 0 {
		t.Fatalf("unauthenticated request dispatched datasource=%d tool=%d resource=%d", backend.fetchCalls, backend.toolCalls, backend.resourceCalls)
	}
}

func TestPublishedHostDatasourceIgnoresBrowserTransportAndCredentialFields(t *testing.T) {
	var selected *dsproto.DataSource
	registry, _, _ := cohortTestRegistry(t, func(_ context.Context, ds *dsproto.DataSource, _ map[string]interface{}, _ map[string]any) (*api.FetchDatasourceOutput, error) {
		selected = ds
		return &api.FetchDatasourceOutput{}, nil
	})
	backend := &dsStubBackend{}
	request := httptest.NewRequest(http.MethodPost,
		"/v1/api/datasources/connectors/fetch?cohort=1&windowId=window-1",
		strings.NewReader(`{"providerIdentity":"attacker","endpoint":"https://untrusted.example/mcp","credential":"forged","backend":{"service":"attacker","method":"execute"},"inputs":{"limit":25}}`))
	request.SetPathValue("id", "connectors")
	request.Header.Set("Mcp-Session-Id", "session-1")
	request = request.WithContext(cohortTestContext("alice", "session-1"))
	response := httptest.NewRecorder()
	handleFetchDatasourceWithBindings(backend, registry)(response, request)
	if response.Code != http.StatusOK || selected == nil || selected.Backend == nil {
		t.Fatalf("published fetch status=%d selected=%+v body=%s", response.Code, selected, response.Body.String())
	}
	if selected.Backend.Service != "studio" || selected.Backend.Method != "sdk.connectors.list" || backend.fetchCalls != 0 {
		t.Fatalf("browser changed trusted MCP binding: backend=%+v legacy=%d", selected.Backend, backend.fetchCalls)
	}
}
