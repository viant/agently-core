package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
	forgeservice "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/ui/permittedview"
	forgetypes "github.com/viant/forge/backend/types"
)

type windowAccessFixture struct {
	allow       bool
	calls       int
	requestedID string
	allowedID   string
	admission   *permittedview.OpenAdmission
}

func (*windowAccessFixture) UsesWindowResourceResolution() bool { return true }
func (*windowAccessFixture) WindowDefinitionsList(context.Context, *forgeservice.WindowDefinitionListInput) (*forgeservice.WindowDefinitionListOutput, error) {
	return &forgeservice.WindowDefinitionListOutput{}, nil
}
func (f *windowAccessFixture) WindowDefinitionGet(_ context.Context, input *forgeservice.WindowDefinitionGetInput) (*forgeservice.WindowDefinitionGetOutput, error) {
	f.calls++
	f.requestedID = input.WindowID
	allowedID := f.allowedID
	if allowedID == "" {
		allowedID = "customer"
	}
	if !f.allow || input.WindowID != allowedID {
		return nil, identity.ErrResourceDenied
	}
	pin := identity.ResolvedResource{URI: "window://workspace/customer", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte("definition"))}, AuthorityBinding: "actor", ValidUntil: time.Now().Add(time.Minute)}
	return &forgeservice.WindowDefinitionGetOutput{WindowID: "customer", Definition: &forgetypes.Window{Resource: &pin, Authorization: &forgetypes.AuthorizationSpec{Scope: "resource", Resource: &forgetypes.AuthorizationResourceSpec{Type: "customer", ID: forgetypes.AuthorizationValueSelector{Source: "windowForm", Selector: "CustomerID"}}}}}, nil
}

func (f *windowAccessFixture) AdmitWindowOpenDecision(ctx context.Context, w *forgetypes.Window, p map[string]any) (*forgeservice.WindowOpenDecision, error) {
	if f.admission == nil || w.Resource == nil {
		return nil, identity.ErrResourceDenied
	}
	result, err := f.admission.ApplyDecision(ctx, *w.Resource, w, p)
	if err != nil {
		return nil, err
	}
	return &forgeservice.WindowOpenDecision{Window: result.Window, ValidUntil: result.ExpiresAt}, nil
}

func TestCanonicalMetadataPreservesFullRegisteredWindowID(t *testing.T) {
	provider := &windowAccessFixture{}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, WithWindowResourceProvider(provider))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/chat/new", nil))
	if provider.requestedID != "chat/new" || response.Code != http.StatusNotFound {
		t.Fatalf("canonical key was truncated or bypassed provider: id=%q status=%d", provider.requestedID, response.Code)
	}
	provider.allow, provider.allowedID, provider.calls = true, "chat/new", 0
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/chat/new", nil))
	if response.Code != http.StatusOK || provider.calls != 2 || provider.requestedID != "chat/new" {
		t.Fatalf("full canonical key lost at resolution/revalidation: status=%d calls=%d id=%q", response.Code, provider.calls, provider.requestedID)
	}
}

type denyEntityResolver struct{ calls int }

func (r *denyEntityResolver) Resolve(context.Context, *permittedview.Request) (*permittedview.Snapshot, error) {
	r.calls++
	return &permittedview.Snapshot{AuthorizationVersion: "v1", ExpiresAt: time.Now().Add(time.Minute), Resources: map[string]*permittedview.Resource{}}, nil
}

func TestDirectMetadataChecksWindowBeforeRequestingEntity(t *testing.T) {
	authority := &windowAccessFixture{}
	entities := &denyEntityResolver{}
	authority.admission = &permittedview.OpenAdmission{Runtime: permittedview.NewRuntime(entities)}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, permittedview.NewRuntime(entities), WithWindowResourceProvider(authority))
	request := func(path string) int {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		return response.Code
	}
	if code := request("/window/customer?applyPermission=true"); code != http.StatusNotFound || entities.calls != 0 {
		t.Fatalf("forbidden window reached entity preflight: code=%d calls=%d", code, entities.calls)
	}
	authority.allow = true
	if code := request("/window/customer?applyPermission=true"); code != http.StatusBadRequest || entities.calls != 0 {
		t.Fatalf("allowed window must bind entity: code=%d calls=%d", code, entities.calls)
	}
	if code := request("/window/customer?applyPermission=true&windowParams=%7B%22CustomerID%22%3A42%7D"); code != http.StatusForbidden || entities.calls != 1 {
		t.Fatalf("guessed entity not checked: code=%d calls=%d", code, entities.calls)
	}
	if code := request("/window/unknown"); code != http.StatusNotFound {
		t.Fatalf("unknown ID loaded through legacy files: %d", code)
	}
}
