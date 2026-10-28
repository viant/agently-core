package sdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/viant/agently-core/protocol/primitive/observe"
)

type timedToolClient struct{ Client }

func (timedToolClient) ExecuteTool(ctx context.Context, _ string, _ map[string]interface{}) (string, error) {
	span := observe.Start(ctx, "report.resource.compile", observe.Labels{Provider: "sensitive-canary"})
	span.End(observe.OK, observe.CacheUnknown)
	call := observe.Start(ctx, "mcp.tool.call", observe.Labels{})
	call.End(observe.OK, observe.CacheUnknown)
	return `{"ok":true}`, nil
}
func TestToolTimingIsOptionalAndContainsOnlyNumericPhases(t *testing.T) {
	t.Setenv("BACKEND_PRIMITIVE_TRACE", "")
	for _, named := range []bool{false, true} {
		for _, perf := range []bool{false, true} {
			url := "/v1/tools/example/execute"
			body := `{}`
			if named {
				url = "/v1/tools/execute"
				body = `{"name":"example","args":{}}`
			}
			if perf {
				url += "?perf=1"
			}
			request := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
			request.SetPathValue("name", "example")
			response := httptest.NewRecorder()
			if named {
				handleExecuteToolByName(timedToolClient{})(response, request)
			} else {
				handleExecuteTool(timedToolClient{})(response, request)
			}
			if response.Code != 200 {
				t.Fatalf("status %d", response.Code)
			}
			header := response.Header().Get("Server-Timing")
			if perf && (!strings.Contains(header, "tool_handler;dur=") || !strings.Contains(header, "report_compile;dur=") || !strings.Contains(header, "tool_call;dur=")) {
				t.Fatalf("missing timing phases: %s", header)
			}
			if !perf && header != "" {
				t.Fatal("timing enabled without opt-in")
			}
			if strings.Contains(header, "sensitive-canary") {
				t.Fatal("labels leaked into header")
			}
		}
	}
	collector := &datasourceTimingCollector{trace: true}
	collector.Observe(observe.Event{Phase: "report.resource.compile", Duration: time.Millisecond})
	if !strings.Contains(collector.traceJSON(), "report.resource.compile") {
		t.Fatal("report trace missing")
	}
}
