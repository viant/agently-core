package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	identity "github.com/viant/agently-core/protocol/resource"
	service "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/forge/backend/types"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type selectionHTTPFixture struct {
	bridge              *service.Service
	window              *types.Window
	gets, admits, signs int
	failGet, failAdmit  int
	emitterError        error
	parameters          map[string]any
}

func (f *selectionHTTPFixture) UsesWindowResourceResolution() bool { return true }
func (f *selectionHTTPFixture) WindowDefinitionsList(context.Context, *service.WindowDefinitionListInput) (*service.WindowDefinitionListOutput, error) {
	return &service.WindowDefinitionListOutput{}, nil
}
func (f *selectionHTTPFixture) WindowDefinitionGet(context.Context, *service.WindowDefinitionGetInput) (*service.WindowDefinitionGetOutput, error) {
	f.gets++
	if f.failGet == f.gets {
		return nil, identity.ErrResourceStale
	}
	return &service.WindowDefinitionGetOutput{WindowID: "view", Definition: f.window}, nil
}
func (f *selectionHTTPFixture) ProjectWindowWithInput(_ context.Context, input *permittedview.BoundView) (*types.Window, error) {
	f.admits++
	if f.failAdmit == f.admits {
		return nil, permittedview.ErrUnavailable
	}
	return input.Window, nil
}
func (f *selectionHTTPFixture) EmitWindowSelection(ctx context.Context, w *types.Window, p map[string]any) (*types.Window, error) {
	return f.bridge.EmitWindowSelection(ctx, w, p)
}
func selectionHTTP(t *testing.T) *selectionHTTPFixture {
	t.Helper()
	now := time.Now()
	f := &selectionHTTPFixture{window: &types.Window{Resource: &identity.ResolvedResource{ProviderIdentity: "owner", URI: "window://team/view", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte(`{}`))}, AuthorityBinding: "actor", ValidUntil: now.Add(time.Minute)}, ResourceTarget: &types.WindowTarget{Platform: "web", SelectionToken: "target"}, SelectionBinding: &identity.SelectionBinding{Token: "counterfeit-runtime"}}}
	f.bridge = service.NewService(&service.Config{WindowSelectionEmitter: func(_ context.Context, _ *types.Window, p map[string]any) (*identity.SelectionBinding, error) {
		f.signs++
		f.parameters = p
		if f.emitterError != nil {
			return nil, f.emitterError
		}
		digest, _ := identity.SelectionParametersDigest(p)
		return &identity.SelectionBinding{Actor: identity.CanonicalActor{Issuer: "idp", Subject: "canonical", TenantID: "1", AccountID: "2"}, InstanceID: "instance", ParametersDigest: digest, Token: "host-signed"}, nil
	}})
	return f
}
func TestCanonicalHTTPOpenProjectsOnceAndEmitsExactSelection(t *testing.T) {
	f := selectionHTTP(t)
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, WithWindowResourceProvider(f))
	raw := httptest.NewRecorder()
	handler.ServeHTTP(raw, httptest.NewRequest(http.MethodGet, "/window/view", nil))
	if raw.Code != 200 || f.signs != 0 || f.admits != 0 || strings.Contains(raw.Body.String(), "selectionBinding") || strings.Contains(raw.Body.String(), "counterfeit") {
		t.Fatal("raw GET emitted runtime provenance", raw.Code, raw.Body.String())
	}
	f.gets = 0
	parameters := `{"ids":[9007199254740993,"opaque"],"parents":{"account":"selected-parent"}}`
	opened := httptest.NewRecorder()
	handler.ServeHTTP(opened, httptest.NewRequest(http.MethodGet, "/window/view?applyPermission=true&windowParams="+url.QueryEscape(parameters), nil))
	if opened.Code != 200 || f.signs != 1 || f.admits != 1 || f.gets != 1 || !strings.Contains(opened.Body.String(), "host-signed") {
		t.Fatalf("authnil canonical OPEN checks/emission lost: %d %s signs=%d admits=%d gets=%d", opened.Code, opened.Body.String(), f.signs, f.admits, f.gets)
	}
	if f.parameters["ids"].([]any)[0].(json.Number).String() != "9007199254740993" || f.parameters["parents"].(map[string]any)["account"] != "selected-parent" {
		t.Fatal("full effective selection changed")
	}
}
func TestCanonicalHTTPOpenDenialAndAvailabilityNeverLeakBindings(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		get, admit    int
		emitter       error
		status, signs int
	}{
		{"initial stale", 1, 0, nil, 404, 0}, {"presentation unavailable", 0, 1, nil, 503, 0},
		{"emitter denied", 0, 0, errors.New("private detail"), 403, 1},
		{"emitter unavailable", 0, 0, fmt.Errorf("private detail: %w", service.ErrWindowSelectionUnavailable), 503, 1},
		{"emitter timeout", 0, 0, fmt.Errorf("private detail: %w", context.DeadlineExceeded), 504, 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := selectionHTTP(t)
			f.failGet, f.failAdmit, f.emitterError = scenario.get, scenario.admit, scenario.emitter
			handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, WithWindowResourceProvider(f))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/view?applyPermission=true", nil))
			if response.Code != scenario.status || f.signs != scenario.signs || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "selectionBinding") {
				t.Fatalf("wrong classification/emission: %d %s signs=%d", response.Code, response.Body.String(), f.signs)
			}
		})
	}
}

func TestCanonicalHTTPAdmittedFrameIsSingleReadAndNextRequestIsFresh(t *testing.T) {
	f := selectionHTTP(t)
	f.failGet = 2
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, WithWindowResourceProvider(f))
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/window/view?applyPermission=true", nil))
	if first.Code != 200 || f.gets != 1 || f.signs != 1 {
		t.Fatal("admitted frame reloaded inside request", first.Code, f.gets, f.signs)
	}
	var body struct {
		Data *types.Window `json:"data"`
	}
	if json.Unmarshal(first.Body.Bytes(), &body) != nil || body.Data == nil || body.Data.Resource == nil || body.Data.Resource.ContentFingerprint != f.window.Resource.ContentFingerprint {
		t.Fatal("original admitted candidate lost")
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/window/view?applyPermission=true", nil))
	if second.Code != 404 || f.gets != 2 || f.signs != 1 || strings.Contains(second.Body.String(), "selectionBinding") {
		t.Fatal("next request reused prior managing admission", second.Code, f.gets, f.signs)
	}
}
