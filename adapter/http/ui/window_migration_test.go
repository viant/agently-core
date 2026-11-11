package ui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestSavedWindowMigrationUsesCurrentCohort(t *testing.T) {
	entry := WindowMigration{From: WindowMigrationTarget{"old-owner", "window://old/records"}, To: WindowMigrationTarget{"studio", "window://new/records"}}
	option, err := WithWindowMigrations([]WindowMigration{entry})
	if err != nil {
		t.Fatal(err)
	}
	source := &publishedWindowFixture{}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, option, WithArtifactCohortWindowSourceForNamespaces("studio", source, nil, "new"))
	q := url.Values{"providerIdentity": {"old-owner"}, "namespace": {"old"}, "resourceRef": {`{"uri":"window://old/records"}`}, "resolvedResource": {`{"uri":"window://old/records","providerIdentity":"old-owner","validUntil":"2000-01-01T00:00:00Z"}`}}
	call := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/window/"+url.PathEscape(entry.From.URI)+"?"+q.Encode(), nil))
		return w
	}
	for i := 0; i < 3; i++ {
		if w := call(); w.Code != 200 || source.uri != entry.To.URI {
			t.Fatalf("restore: %d %s", w.Code, w.Body.String())
		}
	}
	source.err = errors.New("source unavailable")
	if w := call(); w.Code == 200 {
		t.Fatal("unavailable destination became success")
	}
	q.Set("resourceRef", `{"uri":"window://other/records"}`)
	before := source.calls
	if w := call(); w.Code != 400 || source.calls != before {
		t.Fatal("conflicting reference reached destination")
	}
}

func TestWindowMigrationIsExactAndPrepared(t *testing.T) {
	entry := WindowMigration{From: WindowMigrationTarget{"old", "window://old/records"}, To: WindowMigrationTarget{"new", "window://new/records"}}
	option, err := WithWindowMigrations([]WindowMigration{entry})
	if err != nil {
		t.Fatal(err)
	}
	opts := &handlerOptions{}
	option(opts)
	for _, query := range []string{"", "?providerIdentity=other"} {
		r := httptest.NewRequest("GET", "/window/window://old/records"+query, nil)
		actual, err := migrateWindowRequest(r, entry.From.URI, opts.migrations)
		if err != nil || actual != r {
			t.Fatal("unmapped owner changed")
		}
	}
	if _, err := WithWindowMigrations([]WindowMigration{entry, entry}); err == nil {
		t.Fatal("duplicate accepted")
	}
	reverse := WindowMigration{From: entry.To, To: entry.From}
	if _, err := WithWindowMigrations([]WindowMigration{entry, reverse}); err == nil {
		t.Fatal("cycle accepted")
	}
}
