package sdk

import (
	"context"
	"encoding/json"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/sdk/api"
	"github.com/viant/forge/backend/types"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func operationRegistry(t *testing.T) (*PublishedWindowBindings, *int, *bool) {
	t.Helper()
	calls := 0
	revoked := false
	ds := &dsproto.DataSource{ID: "sample", Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "sample", Method: "read"}, Operations: map[string]*dsproto.Operation{"save": {Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "sample", Method: "save"}}}}
	raw, err := json.Marshal(ds)
	if err != nil {
		t.Fatal(err)
	}
	variant := &types.WindowResourceVariant{Window: &types.Window{ResourceTarget: &types.WindowTarget{Platform: "web"}}, DataSources: map[string]json.RawMessage{"sample": raw}}
	open := func(context.Context, string, string, *types.WindowTarget) (*types.WindowResourceVariant, string, error) {
		if revoked {
			return nil, "", context.Canceled
		}
		return variant, "sample-hash", nil
	}
	owner := func(ctx context.Context) (string, error) {
		value, _ := ctx.Value(cohortOwnerKey{}).(string)
		if value == "" {
			return "", context.Canceled
		}
		return value, nil
	}
	b := NewPublishedWindowBindings(open, func(context.Context, *dsproto.DataSource, map[string]interface{}, map[string]any) (*api.FetchDatasourceOutput, error) {
		return nil, context.Canceled
	}, owner)
	b.operate = func(_ context.Context, selected *dsproto.DataSource, name string, _ map[string]interface{}, _ map[string]any) (*api.OperateDatasourceOutput, error) {
		calls++
		if selected.Operations[name].Backend.Method != "save" {
			t.Fatal("operation changed")
		}
		return &api.OperateDatasourceOutput{Result: map[string]interface{}{"saved": true}}, nil
	}
	if err := b.Bind(cohortTestContext("alice", "session-1"), "client-1", "window-1", "example", "window://example/sample", "sample-hash", variant); err != nil {
		t.Fatal(err)
	}
	return b, &calls, &revoked
}
func TestOperateDatasourceHTTPHeldBinding(t *testing.T) {
	b, calls, _ := operationRegistry(t)
	r := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/sample/operate?cohort=1&windowId=window-1", strings.NewReader(`{"operation":"save","inputs":{"value":1}}`))
	r.SetPathValue("id", "sample")
	r.Header.Set("Mcp-Session-Id", "session-1")
	r = r.WithContext(context.WithValue(r.Context(), cohortOwnerKey{}, "alice"))
	w := httptest.NewRecorder()
	handleOperateDatasourceWithBindings(&dsStubBackend{}, b)(w, r)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("action response cacheable")
	}
	if w.Code != 200 || *calls != 1 || strings.Contains(w.Body.String(), "cache") || !strings.Contains(w.Body.String(), "saved") {
		t.Fatalf("%d %s calls=%d", w.Code, w.Body.String(), *calls)
	}
}
func TestOperateDatasourceBindingDenials(t *testing.T) {
	for _, name := range []string{"", "unknown"} {
		b, calls, _ := operationRegistry(t)
		_, handled, err := b.Operate(cohortTestContext("alice", "session-1"), "window-1", "sample", name, nil, nil)
		if err == nil || !handled || *calls != 0 {
			t.Fatalf("operation %q allowed", name)
		}
	}
	b, calls, revoked := operationRegistry(t)
	*revoked = true
	if _, _, err := b.Operate(cohortTestContext("alice", "session-1"), "window-1", "sample", "save", nil, nil); err == nil || *calls != 0 {
		t.Fatal("revoked binding dispatched")
	}
	b, calls, _ = operationRegistry(t)
	if _, _, err := b.Operate(cohortTestContext("mallory", "session-1"), "window-1", "sample", "save", nil, nil); err == nil && *calls != 0 {
		t.Fatal("foreign binding dispatched")
	}

	b, calls, revoked = operationRegistry(t)
	original := b.operate
	b.operate = func(ctx context.Context, ds *dsproto.DataSource, name string, in map[string]interface{}, params map[string]any) (*api.OperateDatasourceOutput, error) {
		out, err := original(ctx, ds, name, in, params)
		*revoked = true
		return out, err
	}
	out, _, err := b.Operate(cohortTestContext("alice", "session-1"), "window-1", "sample", "save", nil, nil)
	if err == nil || out != nil || *calls != 1 {
		t.Fatal("revoked after dispatch released output")
	}
}

func TestOperateDatasourceHTTPRejectsBrowserRouting(t *testing.T) {
	for _, key := range []string{"backend", "service", "server", "tool", "method", "url", "credentials", "cache"} {
		b, calls, _ := operationRegistry(t)
		r := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/sample/operate?cohort=1&windowId=window-1", strings.NewReader(`{"operation":"save","`+key+`":{}}`))
		r.SetPathValue("id", "sample")
		r.Header.Set("Mcp-Session-Id", "session-1")
		r = r.WithContext(context.WithValue(r.Context(), cohortOwnerKey{}, "alice"))
		w := httptest.NewRecorder()
		handleOperateDatasourceWithBindings(&dsStubBackend{}, b)(w, r)
		if w.Code != 400 || *calls != 0 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("routing %s allowed: %d calls=%d", key, w.Code, *calls)
		}
	}
}
