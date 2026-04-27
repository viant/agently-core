package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/viant/agently-core/sdk/api"
)

// dsStubBackend is a Backend stub for HTTP handler tests. The embedded
// Backend is nil — we only override the three methods under test; any other
// method call will panic (which is fine: these tests never invoke them).
type dsStubBackend struct {
	Backend
	fetchCalls      int
	invalidateCalls int
	registryCalls   int
}

func (s *dsStubBackend) FetchDatasource(ctx context.Context, in *api.FetchDatasourceInput) (*api.FetchDatasourceOutput, error) {
	s.fetchCalls++
	return &api.FetchDatasourceOutput{
		Rows:  []map[string]interface{}{{"id": 1, "name": "stub:" + in.ID}},
		Cache: &api.DatasourceCacheMeta{Hit: false, FetchedAt: "2026-04-22T00:00:00Z"},
	}, nil
}
func (s *dsStubBackend) InvalidateDatasourceCache(ctx context.Context, in *api.InvalidateDatasourceCacheInput) error {
	s.invalidateCalls++
	return nil
}
func (s *dsStubBackend) ListLookupRegistry(ctx context.Context, in *api.ListLookupRegistryInput) (*api.ListLookupRegistryOutput, error) {
	s.registryCalls++
	return &api.ListLookupRegistryOutput{
		Entries: []api.LookupRegistryEntry{
			{Name: "advertiser", DataSource: "advertiser", Trigger: "/", Required: true},
		},
	}, nil
}

// unconfiguredBackend returns ErrDatasourceStackNotConfigured from all three
// methods, simulating a runtime that constructed the Backend but didn't wire
// the datasource stack.
type unconfiguredBackend struct{ Backend }

func (unconfiguredBackend) FetchDatasource(_ context.Context, _ *api.FetchDatasourceInput) (*api.FetchDatasourceOutput, error) {
	return nil, ErrDatasourceStackNotConfigured
}
func (unconfiguredBackend) InvalidateDatasourceCache(_ context.Context, _ *api.InvalidateDatasourceCacheInput) error {
	return ErrDatasourceStackNotConfigured
}
func (unconfiguredBackend) ListLookupRegistry(_ context.Context, _ *api.ListLookupRegistryInput) (*api.ListLookupRegistryOutput, error) {
	return nil, ErrDatasourceStackNotConfigured
}

type upstreamDeniedBackend struct{ Backend }

func (upstreamDeniedBackend) FetchDatasource(_ context.Context, _ *api.FetchDatasourceInput) (*api.FetchDatasourceOutput, error) {
	return nil, errors.New(`{"status":"error","message":"user access denied","errors":[{"view":"tree","parameter":"SysConfig","statusCode":403,"message":"user access denied","object":[{"view":"systemconfig","parameter":"Auth","statusCode":403,"message":"user access denied"}]},{"view":"tree","parameter":"Auth","statusCode":403,"message":"user access denied"}]}`)
}

func TestHandleFetchDatasource_Returns501WhenStackNotConfigured(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/advertiser/fetch", strings.NewReader("{}"))
	req.SetPathValue("id", "advertiser")
	w := httptest.NewRecorder()

	h := handleFetchDatasource(unconfiguredBackend{})
	h(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("want 501 when backend returns ErrDatasourceStackNotConfigured, got %d", w.Code)
	}
}

func TestHandleFetchDatasource_Returns403WhenUpstreamPermissionDenied(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/iris_segments_tree/fetch", strings.NewReader("{}"))
	req.SetPathValue("id", "iris_segments_tree")
	w := httptest.NewRecorder()

	handleFetchDatasource(upstreamDeniedBackend{})(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 when backend returns upstream permission denial, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestStatusForDatasourceErr_ParsesRequestFailedWrappedAuthStatus(t *testing.T) {
	err := errors.New(`request failed: 500 Internal Server Error: {"status":"error","message":"user access denied","errors":[{"view":"tree","parameter":"Auth","statusCode":403,"message":"user access denied"}]}`)
	if got := statusForDatasourceErr(err); got != http.StatusForbidden {
		t.Fatalf("want wrapped upstream 403 to map to 403, got %d", got)
	}
}

func TestHandleFetchDatasource_DispatchesToBackend(t *testing.T) {
	body := `{"inputs":{"q":"acm"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/advertiser/fetch", strings.NewReader(body))
	req.SetPathValue("id", "advertiser")
	w := httptest.NewRecorder()

	stub := &dsStubBackend{}
	handleFetchDatasource(stub)(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if stub.fetchCalls != 1 {
		t.Fatalf("want 1 backend call, got %d", stub.fetchCalls)
	}
	var out api.FetchDatasourceOutput
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(out.Rows) != 1 || out.Rows[0]["name"] != "stub:advertiser" {
		t.Fatalf("projection mismatch: %+v", out.Rows)
	}
}

func TestHandleInvalidateDatasourceCache_Dispatches(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/v1/api/datasources/advertiser/cache?inputsHash=abc", nil)
	req.SetPathValue("id", "advertiser")
	w := httptest.NewRecorder()
	stub := &dsStubBackend{}
	handleInvalidateDatasourceCache(stub)(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	if stub.invalidateCalls != 1 {
		t.Fatalf("want 1 invalidate call, got %d", stub.invalidateCalls)
	}
}

func TestHandleListLookupRegistry_RequiresContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/api/lookups/registry", nil)
	w := httptest.NewRecorder()
	handleListLookupRegistry(&dsStubBackend{})(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 when context is missing, got %d", w.Code)
	}
}

func TestHandleListLookupRegistry_ReturnsEntries(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/api/lookups/registry?context=template:any", nil)
	w := httptest.NewRecorder()
	stub := &dsStubBackend{}
	handleListLookupRegistry(stub)(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if stub.registryCalls != 1 {
		t.Fatalf("want 1 registry call, got %d", stub.registryCalls)
	}
	var out api.ListLookupRegistryOutput
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Entries) != 1 || out.Entries[0].Name != "advertiser" {
		t.Fatalf("entries mismatch: %+v", out.Entries)
	}
}
