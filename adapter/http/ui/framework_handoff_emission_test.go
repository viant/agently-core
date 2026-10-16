package ui

import (
	"context"
	"errors"
	"github.com/viant/forge/backend/types"
	"net/http"
	"net/http/httptest"
	"testing"
)

type emissionHandoffFixture struct {
	*selectionHTTPFixture
	bindings int
}

func (f *emissionHandoffFixture) BindAdmittedFrameworkWindow(context.Context, string, string, string, *types.Window) error {
	f.bindings++
	return nil
}
func TestFrameworkHandoffRunsOnlyAfterSuccessfulEmission(t *testing.T) {
	f := &emissionHandoffFixture{selectionHTTPFixture: selectionHTTP(t)}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, WithWindowResourceProvider(f))
	f.emitterError = errors.New("owned emission unavailable")
	request := func() int {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/chat/new?applyPermission=true&windowId=chat/new&clientId=active-client", nil))
		return response.Code
	}
	if code := request(); code < 400 || f.bindings != 0 {
		t.Fatal("failed emission left a bound instance", code, f.bindings)
	}
	f.emitterError = nil
	if code := request(); code != 200 || f.bindings != 1 {
		t.Fatal("successful emission did not bind once", code, f.bindings)
	}
}
