package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
	forgeservice "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/ui/permittedview"
	forgetypes "github.com/viant/forge/backend/types"
)

type windowAccessFixture struct {
	allow          bool
	calls          int
	requestedID    string
	allowedID      string
	projection     *permittedview.Runtime
	selectorSource string
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
	source := f.selectorSource
	if source == "" {
		source = "windowForm"
	}
	return &forgeservice.WindowDefinitionGetOutput{WindowID: "customer", Definition: &forgetypes.Window{Resource: &pin, Authorization: &forgetypes.AuthorizationSpec{Scope: "resource", Resource: &forgetypes.AuthorizationResourceSpec{Type: "customer", ID: forgetypes.AuthorizationValueSelector{Source: source, Selector: "CustomerID"}}}}}, nil
}

type selectedEntityResolver struct{ ids []int }

func (r *selectedEntityResolver) Resolve(_ context.Context, request *permittedview.Request) (*permittedview.Snapshot, error) {
	r.ids = append(r.ids, request.ResourceIDs...)
	result := &permittedview.Snapshot{AuthorizationVersion: "current", ExpiresAt: time.Now().Add(time.Minute), Resources: map[string]*permittedview.Resource{}}
	if len(request.ResourceIDs) == 1 && request.ResourceIDs[0] == 42 {
		result.Resources["42"] = &permittedview.Resource{ID: 42, Type: "customer", Capabilities: map[string]bool{"read": true}}
	}
	return result, nil
}

func TestCanonicalWindowOpenDoesNotRequestEntityPermissions(t *testing.T) {
	resolver := &selectedEntityResolver{}
	provider := &windowAccessFixture{allow: true, selectorSource: "resource", projection: permittedview.NewRuntime(resolver)}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, WithWindowResourceProvider(provider))
	request := func(resource string) int {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/customer?applyPermission=true&resource="+url.QueryEscape(resource), nil))
		return response.Code
	}
	if status := request(`{"CustomerID":42}`); status != http.StatusOK {
		t.Fatalf("owner-provided window did not open: %d", status)
	}
	if len(resolver.ids) != 0 {
		t.Fatalf("consumer unexpectedly requested entity IDs: %v", resolver.ids)
	}
	if status := request(`{"CustomerID":99,"capabilities":{"read":true},"roles":["admin"]}`); status != http.StatusOK {
		t.Fatalf("irrelevant resource permission hints affected opening: %d", status)
	}
	before := len(resolver.ids)
	if status := request(`{"capabilities":{"read":true}}`); status != http.StatusOK || len(resolver.ids) != before {
		t.Fatalf("opening depended on a resource selector: status=%d IDs=%v", status, resolver.ids)
	}
}

func (f *windowAccessFixture) ProjectWindowWithInput(ctx context.Context, input *permittedview.BoundView) (*forgetypes.Window, error) {
	result, err := f.projection.Project(ctx, input.Window, input)
	if err != nil {
		return nil, err
	}
	return result.Window, nil
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
	if response.Code != http.StatusOK || provider.calls != 1 || provider.requestedID != "chat/new" {
		t.Fatalf("full canonical key lost at resolution/revalidation: status=%d calls=%d id=%q", response.Code, provider.calls, provider.requestedID)
	}
}

type denyEntityResolver struct{ calls int }

func (r *denyEntityResolver) Resolve(context.Context, *permittedview.Request) (*permittedview.Snapshot, error) {
	r.calls++
	return &permittedview.Snapshot{AuthorizationVersion: "v1", ExpiresAt: time.Now().Add(time.Minute), Resources: map[string]*permittedview.Resource{}}, nil
}

func TestDirectMetadataPreservesOwningDenialWithoutEntityPreflight(t *testing.T) {
	authority := &windowAccessFixture{}
	entities := &denyEntityResolver{}
	authority.projection = permittedview.NewRuntime(entities)
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
	if code := request("/window/customer?applyPermission=true"); code != http.StatusOK || entities.calls != 0 {
		t.Fatalf("owner allowed window should open without entity binding: code=%d calls=%d", code, entities.calls)
	}
	if code := request("/window/customer?applyPermission=true&windowParams=%7B%22CustomerID%22%3A42%7D"); code != http.StatusOK || entities.calls != 0 {
		t.Fatalf("UI metadata open performed entity lookup: code=%d calls=%d", code, entities.calls)
	}
	if code := request("/window/unknown"); code != http.StatusNotFound {
		t.Fatalf("unknown ID loaded through legacy files: %d", code)
	}
}
