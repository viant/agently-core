package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/viant/agently-core/app/store/conversation"
	convdata "github.com/viant/agently-core/app/store/data"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/sdk/api"
)

// dsStubBackend is a Backend stub for HTTP handler tests. The embedded
// Backend is nil — we only override the three methods under test; any other
// method call will panic (which is fine: these tests never invoke them).
type dsStubBackend struct {
	Backend
	fetchCalls         int
	invalidateCalls    int
	registryCalls      int
	lastConversationID string
	getConversationErr error
	lastConversation   string
}

func (s *dsStubBackend) FetchDatasource(ctx context.Context, in *api.FetchDatasourceInput) (*api.FetchDatasourceOutput, error) {
	s.fetchCalls++
	s.lastConversationID = runtimerequestctx.ConversationIDFromContext(ctx)
	return &api.FetchDatasourceOutput{
		Rows:    []map[string]interface{}{{"id": 1, "name": "stub:" + in.ID}},
		Metrics: map[string]interface{}{"summary": map[string]interface{}{"count": 1}},
		Cache:   &api.DatasourceCacheMeta{Hit: false, FetchedAt: "2026-04-22T00:00:00Z"},
	}, nil
}
func (s *dsStubBackend) GetConversation(_ context.Context, id string) (*conversation.Conversation, error) {
	s.lastConversation = id
	if s.getConversationErr != nil {
		return nil, s.getConversationErr
	}
	return &conversation.Conversation{Id: id}, nil
}
func (s *dsStubBackend) InvalidateDatasourceCache(ctx context.Context, in *api.InvalidateDatasourceCacheInput) error {
	s.invalidateCalls++
	return nil
}
func (s *dsStubBackend) ListLookupRegistry(ctx context.Context, in *api.ListLookupRegistryInput) (*api.ListLookupRegistryOutput, error) {
	s.registryCalls++
	return &api.ListLookupRegistryOutput{
		Entries: []api.LookupRegistryEntry{
			{Name: "account", DataSource: "account", Trigger: "/", Required: true},
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

type emptyDatasourceBackend struct{ Backend }

func (emptyDatasourceBackend) FetchDatasource(_ context.Context, _ *api.FetchDatasourceInput) (*api.FetchDatasourceOutput, error) {
	return &api.FetchDatasourceOutput{}, nil
}

func TestHandleFetchDatasource_Returns501WhenStackNotConfigured(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/account/fetch", strings.NewReader("{}"))
	req.SetPathValue("id", "account")
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

func TestStatusForDatasourceErr_ParsesPlainAuthorizationRequired(t *testing.T) {
	err := errors.New("authorization required")
	if got := statusForDatasourceErr(err); got != http.StatusUnauthorized {
		t.Fatalf("want plain authorization required to map to 401, got %d", got)
	}
}

func TestStatusForDatasourceErr_ParsesPlainTokenMissingAuthMessage(t *testing.T) {
	err := errors.New("oauth session is missing a valid token")
	if got := statusForDatasourceErr(err); got != http.StatusUnauthorized {
		t.Fatalf("want missing token auth message to map to 401, got %d", got)
	}
}

func TestStatusForDatasourceErr_ParsesPlainPermissionDenied(t *testing.T) {
	err := errors.New("permission denied")
	if got := statusForDatasourceErr(err); got != http.StatusForbidden {
		t.Fatalf("want plain permission denied to map to 403, got %d", got)
	}
}

func TestHandleFetchDatasource_DispatchesToBackend(t *testing.T) {
	body := `{"inputs":{"q":"acm"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/account/fetch", strings.NewReader(body))
	req.SetPathValue("id", "account")
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
	if len(out.Rows) != 1 || out.Rows[0]["name"] != "stub:account" {
		t.Fatalf("projection mismatch: %+v", out.Rows)
	}
	if summary, ok := out.Metrics["summary"].(map[string]interface{}); !ok || summary["count"] != float64(1) {
		t.Fatalf("metrics mismatch: %+v", out.Metrics)
	}
}

func TestHandleFetchDatasource_EncodesEmptyRowsAsArray(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/empty/fetch", strings.NewReader("{}"))
	req.SetPathValue("id", "empty")
	w := httptest.NewRecorder()

	handleFetchDatasource(emptyDatasourceBackend{})(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	rows, ok := body["rows"].([]interface{})
	if !ok || len(rows) != 0 {
		t.Fatalf("want rows to be an empty JSON array, got %#v", body["rows"])
	}
}

func TestHandleFetchDatasource_BindsConversationIDFromRequest(t *testing.T) {
	body := `{"conversationId":"conv-123","inputs":{"q":"acm"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/account/fetch", strings.NewReader(body))
	req.SetPathValue("id", "account")
	w := httptest.NewRecorder()

	stub := &dsStubBackend{}
	handleFetchDatasource(stub)(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := stub.lastConversationID; got != "conv-123" {
		t.Fatalf("want bound conversation id conv-123, got %q", got)
	}
	if got := stub.lastConversation; got != "conv-123" {
		t.Fatalf("want access check conversation id conv-123, got %q", got)
	}
}

func TestHandleFetchDatasource_RejectsInaccessibleConversationContext(t *testing.T) {
	body := `{"conversationId":"conv-secret","inputs":{"q":"acm"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/account/fetch", strings.NewReader(body))
	req.SetPathValue("id", "account")
	w := httptest.NewRecorder()

	stub := &dsStubBackend{getConversationErr: convdata.ErrPermissionDenied}
	handleFetchDatasource(stub)(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", w.Code, w.Body.String())
	}
	if stub.fetchCalls != 0 {
		t.Fatalf("want 0 fetch calls when conversation access is denied, got %d", stub.fetchCalls)
	}
}

func TestHandleFetchDatasource_RejectsMissingConversationContext(t *testing.T) {
	body := `{"conversationId":"conv-missing","inputs":{"q":"acm"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/account/fetch", strings.NewReader(body))
	req.SetPathValue("id", "account")
	w := httptest.NewRecorder()

	stub := &dsStubBackend{getConversationErr: convdata.ErrConversationNotFound}
	handleFetchDatasource(stub)(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", w.Code, w.Body.String())
	}
	if stub.fetchCalls != 0 {
		t.Fatalf("want 0 fetch calls when conversation is missing, got %d", stub.fetchCalls)
	}
}

func TestHandleInvalidateDatasourceCache_Dispatches(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/v1/api/datasources/account/cache?inputsHash=abc", nil)
	req.SetPathValue("id", "account")
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
	if len(out.Entries) != 1 || out.Entries[0].Name != "account" {
		t.Fatalf("entries mismatch: %+v", out.Entries)
	}
}
