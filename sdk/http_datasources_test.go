package sdk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/viant/agently-core/sdk/api"
)

// HTTPClient should forward both inputs AND cache hints on FetchDatasource,
// matching the Go + Kotlin clients. This guards against cross-platform drift.
func TestHTTPClient_FetchDatasource_ForwardsCacheHints(t *testing.T) {
	var gotBody map[string]any
	var gotPath string
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rows":[],"cache":{"hit":false,"fetchedAt":"2026-04-22T00:00:00Z"}}`))
	}))

	_, err := c.FetchDatasource(context.Background(), &api.FetchDatasourceInput{
		ID:             " sources/main ",
		ConversationID: " conv-1 ",
		Inputs:         map[string]interface{}{"q": "acm"},
		Cache:          &api.DatasourceCacheHints{BypassCache: true, WriteThrough: true},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotPath != "/v1/api/datasources/sources%2Fmain/fetch" {
		t.Fatalf("path not normalized/encoded: %s", gotPath)
	}
	if gotBody["conversationId"] != "conv-1" {
		t.Fatalf("conversation id not trimmed: %v", gotBody)
	}
	if _, ok := gotBody["windowId"]; ok {
		t.Fatalf("public fetch leaked window permit context: %v", gotBody)
	}
	if _, ok := gotBody["permitVersion"]; ok {
		t.Fatalf("public fetch leaked permit version: %v", gotBody)
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
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.WriteHeader(http.StatusNoContent)
	}))

	err := c.InvalidateDatasourceCache(context.Background(), &api.InvalidateDatasourceCacheInput{
		ID: " account ", InputsHash: " abc123 ",
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
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("context")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"entries":[]}`))
	}))

	if _, err := c.ListLookupRegistry(context.Background(), &api.ListLookupRegistryInput{
		Context: " template:resource_list_review ",
	}); err != nil {
		t.Fatalf("registry: %v", err)
	}
	if gotQuery != "template:resource_list_review" {
		t.Fatalf("want context=template:resource_list_review, got %q", gotQuery)
	}
}

func TestHTTPClient_DatasourceLookupRejectBlankIdentifiersBeforeDispatch(t *testing.T) {
	var requests int
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
	}))

	if _, err := c.FetchDatasource(context.Background(), &api.FetchDatasourceInput{ID: "   "}); err == nil {
		t.Fatalf("expected blank datasource id to fail")
	}
	if err := c.InvalidateDatasourceCache(context.Background(), &api.InvalidateDatasourceCacheInput{ID: "   "}); err == nil {
		t.Fatalf("expected blank datasource id to fail")
	}
	if _, err := c.ListLookupRegistry(context.Background(), &api.ListLookupRegistryInput{Context: "   "}); err == nil {
		t.Fatalf("expected blank lookup context to fail")
	}
	if requests != 0 {
		t.Fatalf("expected no requests for invalid identifiers, got %d", requests)
	}
}
