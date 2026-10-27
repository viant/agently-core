package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	dssvc "github.com/viant/agently-core/service/datasource"
)

type reportSourceFetcherFunc func(context.Context, identity.ResourceRef, string, map[string]interface{}) (*dsproto.FetchResult, error)

func (f reportSourceFetcherFunc) FetchResourceDatasource(ctx context.Context, owner identity.ResourceRef, id string, inputs map[string]interface{}) (*dsproto.FetchResult, error) {
	return f(ctx, owner, id, inputs)
}

func TestGenericDatasourceEndpointCarriesLogicalReportOwner(t *testing.T) {
	calls := 0
	backend := &backendClient{datasourceSvc: dssvc.New(dssvc.Options{}), reportDatasources: reportSourceFetcherFunc(func(ctx context.Context, owner identity.ResourceRef, id string, inputs map[string]interface{}) (*dsproto.FetchResult, error) {
		calls++
		require.Equal(t, "report://example/summary", owner.URI)
		require.Empty(t, owner.Revision)
		require.Equal(t, "options", id)
		require.Equal(t, json.Number("0"), inputs["scope"])
		return &dsproto.FetchResult{Rows: []map[string]interface{}{{"value": 0, "label": "Zero"}}}, nil
	})}
	handler := handleFetchDatasource(backend)
	send := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/options/fetch", strings.NewReader(body))
		req.SetPathValue("id", "options")
		response := httptest.NewRecorder()
		handler(response, req)
		return response
	}
	response := send(`{"owner":{"uri":"report://example/summary"},"inputs":{"scope":0}}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"rows":[{"label":"Zero","value":0}]`)
	require.Equal(t, 1, calls)
	for _, body := range []string{
		`{"owner":{"uri":"report://example/summary","revision":"5"},"inputs":{"scope":0}}`,
		`{"owner":{"uri":"report://example/summary","hash":"caller"},"inputs":{"scope":0}}`,
		`{"owner":{"uri":"report://example/summary"},"descriptor":{"backend":{}},"inputs":{"scope":0}}`,
		`{"owner":{"uri":"report://example/summary"},"resource":{"uri":"datasource://other/options"},"inputs":{"scope":0}}`,
		`{"owner":{"uri":"report://example/summary"},"providerIdentity":"other","inputs":{"scope":0}}`,
	} {
		require.NotEqual(t, 200, send(body).Code)
		require.Equal(t, 1, calls)
	}
}
