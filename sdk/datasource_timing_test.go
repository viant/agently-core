package sdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/viant/agently-core/protocol/primitive/observe"
	"github.com/viant/agently-core/sdk/api"
)

type timedDatasourceBackend struct{ Backend }

func (timedDatasourceBackend) FetchDatasource(ctx context.Context, _ *api.FetchDatasourceInput) (*api.FetchDatasourceOutput, error) {
	for range 2 {
		span := observe.Start(ctx, "gateway.mcp.call", observe.Labels{Provider: "private-provider-name"})
		span.End(observe.OK, observe.CacheUnknown)
	}
	return &api.FetchDatasourceOutput{}, nil
}

func TestDatasourceTimingHeaderIsOptInAndBounded(t *testing.T) {
	collector := &datasourceTimingCollector{}
	for _, duration := range []time.Duration{300 * time.Millisecond, 600 * time.Millisecond} {
		collector.Observe(observe.Event{Phase: "gateway.mcp.call", Duration: duration, Labels: observe.Labels{Provider: "private-provider-name"}})
	}
	collector.Observe(observe.Event{Phase: "untrusted/secret", Duration: time.Hour})
	header := collector.header(950 * time.Millisecond)
	if !strings.Contains(header, `gw_mcp;dur=900.000;desc="count=2"`) || !strings.Contains(header, "gw_mcp_max;dur=600.000") || !strings.Contains(header, "ds_handler;dur=950.000") {
		t.Fatalf("missing phase totals: %q", header)
	}
	if strings.Contains(header, "private-provider-name") || strings.Contains(header, "untrusted") {
		t.Fatalf("untrusted labels reached header: %q", header)
	}
	for _, url := range []string{"/v1/api/datasources/rows/fetch", "/v1/api/datasources/rows/fetch?perf=1"} {
		request := httptest.NewRequest(http.MethodPost, url, strings.NewReader(`{}`))
		request.SetPathValue("id", "rows")
		response := httptest.NewRecorder()
		handleFetchDatasource(timedDatasourceBackend{})(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("unexpected status: %d %s", response.Code, response.Body.String())
		}
		got := response.Header().Get("Server-Timing")
		if strings.Contains(url, "perf=1") != strings.HasPrefix(got, "ds_handler;dur=") {
			t.Fatalf("opt-in header mismatch for %q: %q", url, got)
		}
	}
}

func TestDatasourceTraceCapturesCacheDecisionsWithoutLabels(t *testing.T) {
	collector := &datasourceTimingCollector{trace: true}
	collector.Observe(observe.Event{Phase: "manager.catalog.scope", Duration: 2 * time.Millisecond, Cache: observe.CacheBypass, Outcome: observe.OK, Labels: observe.Labels{Provider: "private-provider", Namespace: "private-namespace"}})
	collector.Observe(observe.Event{Phase: "manager.cache.lookup", Duration: time.Millisecond, Cache: observe.CacheHit, Outcome: observe.OK})
	collector.Observe(observe.Event{Phase: "untrusted/private", Duration: time.Hour, Outcome: observe.Error})
	got := collector.traceJSON()
	for _, expected := range []string{`"manager.catalog.scope"`, `"bypass":1`, `"manager.cache.lookup"`, `"hit":1`} {
		if !strings.Contains(got, expected) {
			t.Fatalf("missing numeric trace item %q: %s", expected, got)
		}
	}
	for _, forbidden := range []string{"private-provider", "private-namespace", "untrusted/private"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("untrusted label or phase reached trace: %s", got)
		}
	}
}
