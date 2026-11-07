package ui

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	identity "github.com/viant/agently-core/protocol/resource"
	service "github.com/viant/agently-core/service/primitiveprovider"
)

type reportBindingFixture struct {
	*publishedWindowBindingFixture
	fail  bool
	reads int
	pin   *identity.ResolvedResource
}

func (f *reportBindingFixture) ReportDefinition(context.Context, *service.ReportDefinitionInput) (*service.ReportDefinitionOutput, error) {
	f.reads++
	if f.fail {
		return nil, errors.New("unavailable")
	}
	return &service.ReportDefinitionOutput{Resource: f.pin, Definition: []byte(`{"privateDefinition":"not returned"}`)}, nil
}
func TestRestoredReportBindsThroughExistingHTTPWindowPath(t *testing.T) {
	f := &reportBindingFixture{publishedWindowBindingFixture: &publishedWindowBindingFixture{emissionHandoffFixture: &emissionHandoffFixture{selectionHTTPFixture: selectionHTTP(t)}}, pin: &identity.ResolvedResource{URI: "report://example/summary"}}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, WithWindowResourceProvider(f))
	call := func(session string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/window/report:%2F%2Fexample%2Fsummary?applyPermission=true&windowId=restored-report&clientId=owned-client", nil)
		req.Header.Set("Mcp-Session-Id", session)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	out := call("owned-session")
	if out.Code != 200 || f.bindings != 1 || f.reads != 1 || f.gets != 0 || f.delivered.Resource != f.pin || f.windowID != "restored-report" {
		t.Fatalf("restore failed: status=%d bindings=%d reads=%d body=%s", out.Code, f.bindings, f.reads, out.Body.String())
	}
	if out.Body.String() != "{\"data\":{\"bound\":true},\"status\":\"ok\"}\n" {
		t.Fatalf("registration response grew: %s", out.Body.String())
	}
	if call("").Code < 400 || f.bindings != 1 {
		t.Fatal("missing session bound report")
	}
	f.fail = true
	if call("owned-session").Code < 400 || f.bindings != 1 {
		t.Fatal("failed report read bound instance")
	}
}
