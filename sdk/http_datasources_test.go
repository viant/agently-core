package sdk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/viant/agently-core/sdk/api"
)

// HTTPClient should forward both inputs AND cache hints on FetchDatasource,
// matching the Go + Kotlin clients. This guards against cross-platform drift.
func TestHTTPClient_FetchDatasource_ForwardsCacheHints(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rows":[],"cache":{"hit":false,"fetchedAt":"2026-04-22T00:00:00Z"}}`))
	}))
	defer srv.Close()

	c, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	_, err = c.FetchDatasource(context.Background(), &api.FetchDatasourceInput{
		ID:     "advertiser",
		Inputs: map[string]interface{}{"q": "acm"},
		Cache:  &api.DatasourceCacheHints{BypassCache: true, WriteThrough: true},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotBody["cache"] == nil {
		t.Fatalf("cache hints not forwarded; body=%v", gotBody)
	}
	cache := gotBody["cache"].(map[string]interface{})
	if cache["bypassCache"] != true || cache["writeThrough"] != true {
		t.Fatalf("cache hints malformed: %v", cache)
	}
	if _, ok := gotBody["inputs"]; !ok {
		t.Fatalf("inputs missing: %v", gotBody)
	}
}

// HTTPClient.InvalidateDatasourceCache must carry the inputsHash query param.
func TestHTTPClient_InvalidateDatasourceCache_SendsInputsHash(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, _ := NewHTTP(srv.URL)
	err := c.InvalidateDatasourceCache(context.Background(), &api.InvalidateDatasourceCacheInput{
		ID: "advertiser", InputsHash: "abc123",
	})
	if err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	if !strings.Contains(gotURL, "inputsHash=abc123") {
		t.Fatalf("inputsHash missing from URL: %s", gotURL)
	}
}

// HTTPClient.ListLookupRegistry URL-encodes the context param.
func TestHTTPClient_ListLookupRegistry_EncodesContext(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("context")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"entries":[]}`))
	}))
	defer srv.Close()

	c, _ := NewHTTP(srv.URL)
	if _, err := c.ListLookupRegistry(context.Background(), &api.ListLookupRegistryInput{
		Context: "template:site_list_planner",
	}); err != nil {
		t.Fatalf("registry: %v", err)
	}
	if gotQuery != "template:site_list_planner" {
		t.Fatalf("want context=template:site_list_planner, got %q", gotQuery)
	}
}
