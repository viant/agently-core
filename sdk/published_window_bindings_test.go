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
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/sdk/api"
	dssvc "github.com/viant/agently-core/service/datasource"
	"github.com/viant/forge/backend/types"
)

type cohortOwnerKey struct{}
type publishedTool struct{ calls int }

func (p *publishedTool) Execute(_ context.Context, name string, _ map[string]interface{}) (string, error) {
	p.calls++
	if name != "studio:sdk.connectors.list" {
		return "", context.Canceled
	}
	return `{"items":[{"name":"real-mcp-tool-row"}]}`, nil
}

func cohortTestContext(owner, session string) context.Context {
	return runtimerequestctx.WithUIBridgeSessionID(context.WithValue(context.Background(), cohortOwnerKey{}, owner), session)
}
func cohortTestRegistry(t *testing.T, execute PublishedDatasourceExecute) (*PublishedWindowBindings, *int, *bool) {
	t.Helper()
	descriptor := &dsproto.DataSource{ID: "connectors", DataSource: types.DataSource{Selectors: &types.Selectors{Data: "items"}}, Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "studio", Method: "sdk.connectors.list"}}
	raw, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	variant := &types.WindowResourceVariant{Window: &types.Window{ResourceTarget: &types.WindowTarget{Platform: "web"}}, DataSources: map[string]json.RawMessage{"connectors": raw}}
	opens := 0
	denied := false
	open := func(_ context.Context, provider, uri string, target *types.WindowTarget) (*types.WindowResourceVariant, string, error) {
		opens++
		if denied || provider != "studio" || uri != "window://datastudio/connectors" || target == nil || target.Platform != "web" {
			return nil, "", context.Canceled
		}
		return variant, "published-hash", nil
	}
	owner := func(ctx context.Context) (string, error) {
		value, _ := ctx.Value(cohortOwnerKey{}).(string)
		if value == "" {
			return "", context.Canceled
		}
		return value, nil
	}
	registry := NewPublishedWindowBindings(open, execute, owner)
	if err := registry.Bind(cohortTestContext("alice", "session-1"), "client-1", "window-1", "studio", "window://datastudio/connectors", "published-hash", variant); err != nil {
		t.Fatal(err)
	}
	return registry, &opens, &denied
}
func TestPublishedDatasourceHTTPUsesServerBindingOneDataCall(t *testing.T) {
	toolCalls := 0
	execute := func(_ context.Context, ds *dsproto.DataSource, inputs map[string]interface{}, _ map[string]any) (*api.FetchDatasourceOutput, error) {
		toolCalls++
		if ds.ID != "connectors" || inputs["limit"] != json.Number("25") {
			t.Fatalf("wrong selected source/inputs: %+v %+v", ds, inputs)
		}
		return &api.FetchDatasourceOutput{Rows: []map[string]interface{}{{"name": "actual-data"}}}, nil
	}
	registry, opens, _ := cohortTestRegistry(t, execute)
	legacy := &dsStubBackend{}
	request := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/connectors/fetch?cohort=1&windowId=window-1", strings.NewReader(`{"inputs":{"limit":25}}`))
	request.SetPathValue("id", "connectors")
	request.Header.Set("Mcp-Session-Id", "session-1")
	request = request.WithContext(context.WithValue(request.Context(), cohortOwnerKey{}, "alice"))
	response := httptest.NewRecorder()
	handleFetchDatasourceWithBindings(legacy, registry)(response, request)
	if response.Code != http.StatusOK || toolCalls != 1 || *opens != 2 || legacy.fetchCalls != 0 || !strings.Contains(response.Body.String(), "actual-data") {
		t.Fatalf("fast route: status=%d body=%s tools=%d localOpens=%d legacy=%d", response.Code, response.Body.String(), toolCalls, *opens, legacy.fetchCalls)
	}
}
func TestPublishedDatasourceRejectsUndeclaredAndRequiresSessionBinding(t *testing.T) {
	toolCalls := 0
	registry, _, _ := cohortTestRegistry(t, func(context.Context, *dsproto.DataSource, map[string]interface{}, map[string]any) (*api.FetchDatasourceOutput, error) {
		toolCalls++
		return &api.FetchDatasourceOutput{}, nil
	})
	legacy := &dsStubBackend{}
	for _, tc := range []struct{ id, owner, session string }{{"connector_activate", "alice", "session-1"}, {"connectors", "bob", "session-1"}, {"connectors", "alice", "session-other"}} {
		r := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/"+tc.id+"/fetch?cohort=1&windowId=window-1", strings.NewReader(`{}`))
		r.SetPathValue("id", tc.id)
		r.Header.Set("Mcp-Session-Id", tc.session)
		r = r.WithContext(context.WithValue(r.Context(), cohortOwnerKey{}, tc.owner))
		w := httptest.NewRecorder()
		handleFetchDatasourceWithBindings(legacy, registry)(w, r)
		missing := tc.owner != "alice" || tc.session != "session-1"
		if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "window_binding_required") != missing {
			t.Fatalf("%+v allowed: %d %s", tc, w.Code, w.Body.String())
		}
	}
	if toolCalls != 0 || legacy.fetchCalls != 0 {
		t.Fatalf("denied request dispatched: tool=%d legacy=%d", toolCalls, legacy.fetchCalls)
	}
}

func TestPublishedDatasourceMissingBindingSignalsMetadataReadmissionOnly(t *testing.T) {
	toolCalls := 0
	registry, _, _ := cohortTestRegistry(t, func(context.Context, *dsproto.DataSource, map[string]interface{}, map[string]any) (*api.FetchDatasourceOutput, error) {
		toolCalls++
		return &api.FetchDatasourceOutput{}, nil
	})
	ctx := cohortTestContext("alice", "session-1")
	require.NoError(t, registry.Forget(ctx, "window-1", "published-hash"))
	legacy := &dsStubBackend{}
	request := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/connectors/fetch?cohort=1&perf=1&windowId=window-1", strings.NewReader(`{}`))
	request.SetPathValue("id", "connectors")
	request.Header.Set("Mcp-Session-Id", "session-1")
	request = request.WithContext(context.WithValue(request.Context(), cohortOwnerKey{}, "alice"))
	response := httptest.NewRecorder()
	handleFetchDatasourceWithBindings(legacy, registry)(response, request)
	require.Equal(t, http.StatusForbidden, response.Code)
	var marker map[string]string
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &marker))
	require.Equal(t, "window_binding_required", marker["code"])
	require.Equal(t, "window", marker["scope"])
	require.Contains(t, response.Header().Get("Server-Timing"), "binding_owner;dur=")
	require.Zero(t, toolCalls)
	require.Zero(t, legacy.fetchCalls)

	// No verified owner is an authorization failure, not a lost binding.
	missingOwner := request.Clone(context.Background())
	denial := httptest.NewRecorder()
	handleFetchDatasourceWithBindings(legacy, registry)(denial, missingOwner)
	require.Equal(t, http.StatusForbidden, denial.Code)
	require.NotContains(t, denial.Body.String(), "window_binding_required")
}
func TestPublishedDatasourceFinalAdmissionRecheckDropsRows(t *testing.T) {
	var denied *bool
	registry, opens, flag := cohortTestRegistry(t, func(context.Context, *dsproto.DataSource, map[string]interface{}, map[string]any) (*api.FetchDatasourceOutput, error) {
		*denied = true
		return &api.FetchDatasourceOutput{Rows: []map[string]interface{}{{"secret": "row"}}}, nil
	})
	denied = flag
	r := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/connectors/fetch?cohort=1&windowId=window-1", strings.NewReader(`{}`))
	r.SetPathValue("id", "connectors")
	r.Header.Set("Mcp-Session-Id", "session-1")
	r = r.WithContext(context.WithValue(r.Context(), cohortOwnerKey{}, "alice"))
	w := httptest.NewRecorder()
	handleFetchDatasourceWithBindings(&dsStubBackend{}, registry)(w, r)
	if w.Code != http.StatusForbidden || *opens != 2 || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "window_binding_required") {
		t.Fatalf("post-call revoked rows escaped: status=%d opens=%d body=%s", w.Code, *opens, w.Body.String())
	}
}

func TestPublishedDatasourceRevokedBeforeToolDoesNotSignalMissingBinding(t *testing.T) {
	toolCalls := 0
	registry, opens, denied := cohortTestRegistry(t, func(context.Context, *dsproto.DataSource, map[string]interface{}, map[string]any) (*api.FetchDatasourceOutput, error) {
		toolCalls++
		return &api.FetchDatasourceOutput{}, nil
	})
	*denied = true
	r := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/connectors/fetch?cohort=1&windowId=window-1", strings.NewReader(`{}`))
	r.SetPathValue("id", "connectors")
	r.Header.Set("Mcp-Session-Id", "session-1")
	r = r.WithContext(context.WithValue(r.Context(), cohortOwnerKey{}, "alice"))
	w := httptest.NewRecorder()
	handleFetchDatasourceWithBindings(&dsStubBackend{}, registry)(w, r)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.NotContains(t, w.Body.String(), "window_binding_required")
	require.Equal(t, 1, *opens)
	require.Zero(t, toolCalls)
}
func TestPublishedDatasourceActualSDKRouteUsesServiceMCPExecutor(t *testing.T) {
	tool := &publishedTool{}
	service := dssvc.New(dssvc.Options{Executor: tool, ReadContext: func(context.Context) (context.Context, error) { t.Fatal("legacy metadata read"); return nil, nil }})
	backend := &backendClient{datasourceSvc: service}
	registry, opens, _ := cohortTestRegistry(t, backend.FetchPublishedDatasource)
	r := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/connectors/fetch?cohort=1&windowId=window-1", strings.NewReader(`{}`))
	r.SetPathValue("id", "connectors")
	r.Header.Set("Mcp-Session-Id", "session-1")
	r = r.WithContext(context.WithValue(r.Context(), cohortOwnerKey{}, "alice"))
	w := httptest.NewRecorder()
	handleFetchDatasourceWithBindings(backend, registry)(w, r)
	if w.Code != http.StatusOK || tool.calls != 1 || *opens != 2 || !strings.Contains(w.Body.String(), "real-mcp-tool-row") {
		t.Fatalf("actual route: status=%d calls=%d opens=%d body=%s", w.Code, tool.calls, *opens, w.Body.String())
	}
}
func TestPublishedMarkerWithoutSDKBindingFailsClosed(t *testing.T) {
	legacy := &dsStubBackend{}
	r := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/connectors/fetch?cohort=1&windowId=window-1", strings.NewReader(`{}`))
	r.SetPathValue("id", "connectors")
	w := httptest.NewRecorder()
	handleFetchDatasource(legacy)(w, r)
	if w.Code != http.StatusForbidden || legacy.fetchCalls != 0 || strings.Contains(w.Body.String(), "window_binding_required") {
		t.Fatalf("unwired marker fell back: %d calls=%d", w.Code, legacy.fetchCalls)
	}
}
func TestPublishedBindingDelayedCloseCannotEraseNewVersion(t *testing.T) {
	registry, _, _ := cohortTestRegistry(t, func(context.Context, *dsproto.DataSource, map[string]interface{}, map[string]any) (*api.FetchDatasourceOutput, error) {
		return &api.FetchDatasourceOutput{}, nil
	})
	ctx := cohortTestContext("alice", "session-1")
	entry, ok, err := registry.lookup(ctx, "window-1")
	if err != nil || !ok {
		t.Fatal("missing first binding")
	}
	variant := &types.WindowResourceVariant{Window: &types.Window{ResourceTarget: &types.WindowTarget{Platform: "web"}}, DataSources: entry.descriptors}
	if err := registry.Bind(ctx, "client-1", "window-1", "studio", entry.uri, "new-hash", variant); err != nil {
		t.Fatal(err)
	}
	if err := registry.Forget(ctx, "window-1", entry.hash); err != nil {
		t.Fatal(err)
	}
	held, ok, err := registry.lookup(ctx, "window-1")
	if err != nil || !ok || held.hash != "new-hash" {
		t.Fatal("late close removed new binding")
	}
	if err := registry.Forget(ctx, "window-1", "new-hash"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := registry.lookup(ctx, "window-1"); ok {
		t.Fatal("exact close retained binding")
	}
}

func TestPublishedWindowIDCollisionAllowsOnlyFreshSessionAdmission(t *testing.T) {
	calls := 0
	registry, _, denied := cohortTestRegistry(t, func(ctx context.Context, _ *dsproto.DataSource, _ map[string]interface{}, _ map[string]any) (*api.FetchDatasourceOutput, error) {
		calls++
		return &api.FetchDatasourceOutput{Rows: []map[string]interface{}{{"owner": ctx.Value(cohortOwnerKey{})}}}, nil
	})
	for index, next := range []struct{ owner, session string }{{"alice", "session-2"}, {"bob", "session-3"}} {
		ctx := cohortTestContext(next.owner, next.session)
		result, handled, err := registry.Fetch(ctx, "window-1", "connectors", nil, nil)
		require.NoError(t, err)
		require.False(t, handled)
		require.Nil(t, result)
		require.Equal(t, index, calls)
		before := calls
		// The normal trusted metadata path must admit and bind this session anew.
		variant, hash, err := registry.open(ctx, "studio", "window://datastudio/connectors", &types.WindowTarget{Platform: "web"})
		require.NoError(t, err)
		require.NoError(t, registry.Bind(ctx, "new-client", "window-1", "studio", "window://datastudio/connectors", hash, variant))
		result, handled, err = registry.Fetch(ctx, "window-1", "connectors", nil, nil)
		require.NoError(t, err)
		require.True(t, handled)
		require.Equal(t, before+1, calls)
		require.Equal(t, next.owner, result.Rows[0]["owner"])
	}
	*denied = true
	before := calls
	result, handled, err := registry.Fetch(cohortTestContext("bob", "session-3"), "window-1", "connectors", nil, nil)
	require.Error(t, err)
	require.True(t, handled)
	require.Nil(t, result)
	require.Equal(t, before, calls)
}
