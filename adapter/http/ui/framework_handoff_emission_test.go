package ui

import (
	"context"
	"errors"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/forge/backend/types"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type emissionHandoffFixture struct {
	*selectionHTTPFixture
	bindings int
}

type publishedWindowBindingFixture struct {
	*emissionHandoffFixture
	clientID, windowID, sessionID string
	delivered                     *types.Window
}

func (f *publishedWindowBindingFixture) BindAdmittedNativePresentationWindow(ctx context.Context, clientID, windowID, _ string, window *types.Window) (bool, error) {
	f.bindings++
	f.clientID, f.windowID, f.sessionID = clientID, windowID, requestctx.UIBridgeSessionIDFromContext(ctx)
	f.delivered = window
	return true, nil
}

func TestPublishedWindowGetBindsDeliveredInstanceWithoutAnotherReadOrOpen(t *testing.T) {
	f := &publishedWindowBindingFixture{emissionHandoffFixture: &emissionHandoffFixture{selectionHTTPFixture: selectionHTTP(t)}}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, WithWindowResourceProvider(f))
	request := httptest.NewRequest(http.MethodGet, "/window/view?windowId=record-321&clientId=client-one", nil)
	request.Header.Set("Mcp-Session-Id", "negotiated-session")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || f.bindings != 1 || f.gets != 1 || f.admits != 0 || f.signs != 0 {
		t.Fatalf("published delivery: code=%d binds=%d gets=%d admits=%d signs=%d", response.Code, f.bindings, f.gets, f.admits, f.signs)
	}
	if f.clientID != "client-one" || f.windowID != "record-321" || f.sessionID != "negotiated-session" || f.delivered.Resource != f.window.Resource {
		t.Fatal("delivered instance lost its server-selected resource or transport identity")
	}
	f.failGet = 2
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code < 400 || f.bindings != 1 {
		t.Fatal("failed provider read bound a window")
	}
}

func TestPublishedWindowWithAuthorizationHintsBindsOnOrdinaryGet(t *testing.T) {
	f := &publishedWindowBindingFixture{emissionHandoffFixture: &emissionHandoffFixture{selectionHTTPFixture: selectionHTTP(t)}}
	f.window.Authorization = &types.AuthorizationSpec{Scope: "resource", DataSourceRef: "owner_facts"}
	f.window.AuthorizationSnapshot = map[string]interface{}{"principal": map[string]interface{}{"roles": []string{"editor"}}}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, WithWindowResourceProvider(f))
	request := httptest.NewRequest(http.MethodGet, "/window/view?windowId=record-321&clientId=client-one", nil)
	request.Header.Set("Mcp-Session-Id", "negotiated-session")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || f.bindings != 1 || f.gets != 1 || f.admits != 0 || f.signs != 0 {
		t.Fatalf("managed delivery with hints: code=%d binds=%d gets=%d admits=%d signs=%d", response.Code, f.bindings, f.gets, f.admits, f.signs)
	}
	if f.delivered.Authorization != f.window.Authorization || f.delivered.AuthorizationSnapshot == nil || f.sessionID != "negotiated-session" {
		t.Fatal("producer hints or authenticated UI binding were lost")
	}
	f.failGet = 2
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code < 400 || f.bindings != 1 || strings.Contains(response.Body.String(), "authorizationSnapshot") {
		t.Fatal("denied producer metadata was exposed or bound")
	}
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
