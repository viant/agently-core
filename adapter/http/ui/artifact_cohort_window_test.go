package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	identity "github.com/viant/agently-core/protocol/resource"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	forgetypes "github.com/viant/forge/backend/types"
)

type publishedWindowFixture struct {
	calls   int
	uri     string
	variant *forgetypes.WindowResourceVariant
	err     error
}

func (f *publishedWindowFixture) OpenWindowVariant(_ context.Context, uri string, _ *forgetypes.WindowTarget) (*forgetypes.WindowResourceVariant, string, error) {
	f.calls++
	f.uri = uri
	if f.err != nil {
		return nil, "", f.err
	}
	if f.variant != nil {
		return f.variant, "abcdef", nil
	}
	return &forgetypes.WindowResourceVariant{Window: &forgetypes.Window{WindowKey: "connectors", View: forgetypes.View{Content: &forgetypes.Container{ID: "published"}}, DataSource: map[string]forgetypes.DataSource{"rows": {Service: &forgetypes.Service{Endpoint: "agentlyAPI", URI: "/v1/api/datasources/rows/fetch", Method: "POST"}}}}, DataSources: map[string]json.RawMessage{"rows": json.RawMessage(`{"id":"rows"}`)}}, "abcdef", nil
}

func TestClaimedPublishedNamespaceNeverFallsBackForOldCanonicalLocator(t *testing.T) {
	legacy := &windowAccessFixture{allow: true, allowedID: "window://datastudio/connectors"}
	published := &publishedWindowFixture{}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil,
		WithWindowResourceProvider(legacy),
		WithArtifactCohortWindowSourceForNamespaces("datastudio-management", published, nil, "datastudio"))
	request := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		return response
	}
	canonical := "/window/" + url.PathEscape("window://datastudio/connectors")
	if got := request(canonical); got.Code != http.StatusOK || legacy.calls != 0 || published.calls != 1 || published.uri != "window://datastudio/connectors" {
		t.Fatalf("old canonical locator did not use local admission: status=%d legacy=%d published=%d uri=%q", got.Code, legacy.calls, published.calls, published.uri)
	}
	published.err = errors.New("revoked")
	for _, path := range []string{
		canonical,
		"/window/connectors?namespace=datastudio",
		canonical + "?providerIdentity=other",
		canonical + "?resourceRef=malformed",
		"/window/" + url.PathEscape("window://datastudio/removed"),
	} {
		if got := request(path); got.Code != http.StatusForbidden || legacy.calls != 0 {
			t.Fatalf("claimed namespace fell through legacy route: path=%s status=%d legacy=%d", path, got.Code, legacy.calls)
		}
	}
	other := request("/window/" + url.PathEscape("window://other/connectors"))
	if other.Code == http.StatusForbidden || legacy.calls != 1 {
		t.Fatalf("unclaimed namespace was intercepted: status=%d legacy=%d", other.Code, legacy.calls)
	}
}

func TestAmbiguousPublishedNamespaceClaimDeniesWithoutLegacyLookup(t *testing.T) {
	legacy := &windowAccessFixture{allow: true, allowedID: "window://datastudio/connectors"}
	first, second := &publishedWindowFixture{}, &publishedWindowFixture{}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil,
		WithWindowResourceProvider(legacy),
		WithArtifactCohortWindowSourceForNamespaces("one", first, nil, "datastudio"),
		WithArtifactCohortWindowSourceForNamespaces("two", second, nil, "datastudio"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/"+url.PathEscape("window://datastudio/connectors"), nil))
	if response.Code != http.StatusForbidden || legacy.calls != 0 || first.calls != 0 || second.calls != 0 {
		t.Fatalf("ambiguous namespace escaped deny: status=%d legacy=%d first=%d second=%d", response.Code, legacy.calls, first.calls, second.calls)
	}
}

func TestPublishedWindowLowersClosedDatasourceDescriptorsForBrowser(t *testing.T) {
	remote := json.RawMessage(`{"id":"connectors","selectors":{"data":"rows"},"backend":{"kind":"mcp_tool","service":"datastudio-management","method":"studio.sdk.connectors.list"}}`)
	local := json.RawMessage(`{"id":"connector_filter","autoFetch":true,"backend":{"kind":"inline","rows":[]}}`)
	fixture := &publishedWindowFixture{variant: &forgetypes.WindowResourceVariant{Window: &forgetypes.Window{WindowKey: "connectors", View: forgetypes.View{Content: &forgetypes.Container{ID: "published"}}, DataSource: map[string]forgetypes.DataSource{"connectors": {}}}, DataSources: map[string]json.RawMessage{"connectors": remote, "connector_filter": local}}}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, WithArtifactCohortWindowSource("studio", fixture, nil))
	query := url.Values{"providerIdentity": {"studio"}, "namespace": {"studio"}, "resourceRef": {`{"uri":"window://studio/connectors"}`}}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/connectors?"+query.Encode(), nil))
	if response.Code != http.StatusOK {
		t.Fatalf("published metadata: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Data forgetypes.Window `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	service := body.Data.DataSource["connectors"].Service
	if service == nil || service.Endpoint != "agentlyAPI" || service.URI != "/v1/api/datasources/connectors/fetch?cohort=1" || service.Method != "POST" {
		t.Fatalf("remote datasource route: %+v", service)
	}
	inline := body.Data.DataSource["connector_filter"]
	if inline.Service != nil || inline.AutoFetch == nil || *inline.AutoFetch {
		t.Fatalf("inline datasource unexpectedly fetched: %+v", inline)
	}
}
func TestPublishedWindowGetUsesOnlyAdmittedLocalVariantAndRechecks304(t *testing.T) {
	legacy := &windowAccessFixture{allow: false}
	published := &publishedWindowFixture{}
	bound := 0
	binder := func(ctx context.Context, clientID, windowID, providerIdentity, uri, hash string, variant *forgetypes.WindowResourceVariant) error {
		bound++
		if runtimerequestctx.UIBridgeSessionIDFromContext(ctx) != "mcp-session" || clientID != "client" || windowID != "instance" || providerIdentity != "studio" || uri != "window://studio/connectors" || hash != "abcdef" || len(variant.DataSources) != 1 {
			t.Fatal("incorrect published binding")
		}
		return nil
	}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, WithWindowResourceProvider(legacy), WithArtifactCohortWindowSource("studio", published, binder))
	expected := expectedWindowResource{URI: "window://studio/connectors", ProviderIdentity: "studio", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte("published"))}}
	raw, _ := json.Marshal(expected)
	query := url.Values{"expectedResource": {string(raw)}, "providerIdentity": {"studio"}, "clientId": {"client"}, "windowId": {"instance"}}
	path := "/window/connectors?" + query.Encode()
	request := func(etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Mcp-Session-Id", "mcp-session")
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	first := request("")
	if first.Code != http.StatusOK || first.Header().Get("ETag") != `"abcdef.cohort-v2"` || first.Header().Get("X-Artifact-Hash") != "abcdef" || first.Header().Get("Cache-Control") != "private, no-cache" || legacy.calls != 0 || published.calls != 1 || published.uri != expected.URI || bound != 1 {
		t.Fatalf("published GET: %d %q calls=%d/%d bound=%d body=%s", first.Code, first.Header().Get("ETag"), legacy.calls, published.calls, bound, first.Body.String())
	}
	var body struct {
		Status string            `json:"status"`
		Data   forgetypes.Window `json:"data"`
	}
	if json.Unmarshal(first.Body.Bytes(), &body) != nil || body.Status != "ok" || body.Data.View.Content.ID != "published" {
		t.Fatal("wrong existing UI response shape", first.Body.String())
	}
	if got := body.Data.DataSource["rows"].Service.URI; got != "/v1/api/datasources/rows/fetch?cohort=1" {
		t.Fatalf("missing published datasource routing marker: %s", got)
	}
	oldRepresentation := request(`"abcdef"`)
	if oldRepresentation.Code != http.StatusOK {
		t.Fatalf("old representation incorrectly reused: %d", oldRepresentation.Code)
	}
	second := request(`"abcdef.cohort-v2"`)
	if second.Code != http.StatusNotModified || second.Header().Get("X-Artifact-Hash") != "abcdef" || legacy.calls != 0 || published.calls != 3 || bound != 3 {
		t.Fatalf("304 skipped admission/bind: %d %d %d %d", second.Code, legacy.calls, published.calls, bound)
	}
	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/window/connectors?providerIdentity=studio&expectedResource=malformed", nil))
	if bad.Code != http.StatusForbidden || legacy.calls != 0 {
		t.Fatalf("registered malformed request fell through: %d legacy=%d", bad.Code, legacy.calls)
	}
	ref := url.Values{"providerIdentity": {"studio"}, "namespace": {"studio"}, "resourceRef": {`{"uri":"window://studio/connectors"}`}}
	byRef := httptest.NewRecorder()
	handler.ServeHTTP(byRef, httptest.NewRequest(http.MethodGet, "/window/connectors?"+ref.Encode(), nil))
	if byRef.Code != http.StatusOK || legacy.calls != 0 || published.calls != 4 {
		t.Fatalf("registered resourceRef missed local replica: %d legacy=%d local=%d", byRef.Code, legacy.calls, published.calls)
	}
	canonical := httptest.NewRecorder()
	handler.ServeHTTP(canonical, httptest.NewRequest(http.MethodGet, "/window/"+url.PathEscape(expected.URI)+"?"+ref.Encode(), nil))
	if canonical.Code != http.StatusOK || legacy.calls != 0 || published.calls != 5 {
		t.Fatalf("canonical URI window key missed local replica: %d legacy=%d local=%d", canonical.Code, legacy.calls, published.calls)
	}
}
