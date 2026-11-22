package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/viant/agently-core/service/ui/permittedview"
	forgetypes "github.com/viant/forge/backend/types"
)

// Independently constructed synthetic display facts; no account or business
// resource permission is requested by this presentation fixture.
type publishedPresentationProvider struct {
	*windowAccessFixture
	projections int
	features    []string
	err         error
}

func (*publishedPresentationProvider) HasWindowUIProjection() bool { return true }

func (p *publishedPresentationProvider) ProjectWindowWithInput(_ context.Context, input *permittedview.BoundView) (*forgetypes.Window, error) {
	p.projections++
	if input.WindowID != "" || len(input.WindowForm) != 0 || len(input.ResourceData) != 0 {
		return nil, errors.New("presentation received resource-binding inputs")
	}
	if p.err != nil {
		return nil, p.err
	}
	window := *input.Window
	window.AuthorizationSnapshot = map[string]any{
		"authorizationVersion": "synthetic-display",
		"principal": map[string]any{
			"subject":  "synthetic-user",
			"roles":    []string{"reader"},
			"features": append([]string{}, p.features...),
		},
	}
	return &window, nil
}

func TestPublishedWindowUsesExistingConfiguredPrincipalPresentation(t *testing.T) {
	provider := &publishedPresentationProvider{windowAccessFixture: &windowAccessFixture{}, features: []string{"FEATURE"}}
	source := &publishedWindowFixture{variant: &forgetypes.WindowResourceVariant{
		Window: &forgetypes.Window{WindowKey: "connectors", Authorization: &forgetypes.AuthorizationSpec{Scope: "principal"},
			View:       forgetypes.View{Content: &forgetypes.Container{ID: "published"}},
			DataSource: map[string]forgetypes.DataSource{}},
	}}
	bindings := 0
	binder := func(_ context.Context, _, _, _, _, _ string, variant *forgetypes.WindowResourceVariant) error {
		bindings++
		if variant.Window.AuthorizationSnapshot != nil {
			t.Fatal("personalized display facts entered the source binding")
		}
		return nil
	}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil,
		WithWindowResourceProvider(provider), WithArtifactCohortWindowSourceForNamespaces("studio", source, binder, "example"))
	query := url.Values{"clientId": {"client"}, "windowId": {"instance"}}
	path := "/window/" + url.PathEscape("window://example/connectors") + "?" + query.Encode()
	request := func(etag string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Mcp-Session-Id", "synthetic-session")
		r.Header.Set("If-None-Match", etag)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		return response
	}
	first := request(`"abcdef.cohort-v2"`)
	if first.Code != http.StatusOK || provider.projections != 1 || provider.calls != 0 || bindings != 1 {
		t.Fatalf("published presentation was skipped: status=%d projections=%d legacy=%d bindings=%d", first.Code, provider.projections, provider.calls, bindings)
	}
	var delivered struct {
		Data forgetypes.Window `json:"data"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &delivered); err != nil {
		t.Fatal(err)
	}
	principal, _ := delivered.Data.AuthorizationSnapshot["principal"].(map[string]any)
	features, _ := principal["features"].([]any)
	if principal["subject"] != "synthetic-user" || len(features) != 1 || features[0] != "FEATURE" {
		t.Fatal("verified display facts were not delivered")
	}
	if first.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("personalized response can be retained by a metadata cache")
	}
	if source.variant.Window.AuthorizationSnapshot != nil {
		t.Fatal("personalized response mutated the reusable source")
	}
	provider.features = nil
	second := request(first.Header().Get("ETag"))
	if second.Code != http.StatusOK || provider.projections != 2 || bindings != 2 {
		t.Fatal("conditional request reused prior display facts")
	}
	if err := json.Unmarshal(second.Body.Bytes(), &delivered); err != nil {
		t.Fatal(err)
	}
	principal, _ = delivered.Data.AuthorizationSnapshot["principal"].(map[string]any)
	features, _ = principal["features"].([]any)
	if len(features) != 0 {
		t.Fatal("changed principal retained prior feature")
	}
	provider.err = permittedview.ErrUnavailable
	failed := request(first.Header().Get("ETag"))
	if failed.Code != http.StatusServiceUnavailable || failed.Header().Get("ETag") != "" {
		t.Fatal("configured presentation failure returned a cached successful window")
	}
	before := provider.projections
	source.err = errors.New("source access denied")
	if denied := request(""); denied.Code != http.StatusForbidden || provider.projections != before {
		t.Fatal("denied source reached principal presentation")
	}
}

func TestPublishedWindowPreservesProducerDisplayFacts(t *testing.T) {
	provider := &publishedPresentationProvider{windowAccessFixture: &windowAccessFixture{}, err: permittedview.ErrUnavailable}
	snapshot := map[string]any{"principal": map[string]any{"subject": "producer-user", "features": []string{"PRODUCER_FEATURE"}}}
	source := &publishedWindowFixture{variant: &forgetypes.WindowResourceVariant{Window: &forgetypes.Window{
		WindowKey: "connectors", View: forgetypes.View{Content: &forgetypes.Container{ID: "published"}},
		DataSource: map[string]forgetypes.DataSource{}, AuthorizationSnapshot: snapshot,
	}}}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil,
		WithWindowResourceProvider(provider), WithArtifactCohortWindowSourceForNamespaces("studio", source, nil, "example"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/"+url.PathEscape("window://example/connectors"), nil))
	if response.Code != http.StatusOK || provider.projections != 0 || provider.calls != 0 {
		t.Fatal("producer snapshot was replaced by a second identity lookup")
	}
	var delivered struct {
		Data forgetypes.Window `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &delivered); err != nil {
		t.Fatal(err)
	}
	principal, _ := delivered.Data.AuthorizationSnapshot["principal"].(map[string]any)
	if principal["subject"] != "producer-user" || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("producer display facts were lost or marked cacheable")
	}
}
