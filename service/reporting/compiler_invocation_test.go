package reporting

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	"testing"
)

func TestCompileFencedReportAdmittedInvocation(t *testing.T) {
	service := New(Options{Store: NewStoreAdapter(reportmemory.New())})
	invocation := json.RawMessage(`{"source":{"kind":"dashboard.reportBuilder","containerId":"delivery","stateKey":"reportBuilder:delivery","dataSourceRef":"cube"},"parameters":{"viewMode":"table","groupBy":"","pageSize":50,"orderField":"","orderDir":"asc"},"datasets":[{"id":"rows","dataSourceRef":"delivery_daily","request":{"measures":{"spend":true},"filters":{"adOrderId":[2659534]},"limit":1000,"offset":0}}]}`)
	compiled, err := service.CompileFencedReport(context.Background(), &CompileFencedReportRequest{Content: validFencedReportContent(), ReportID: "backend", Invocation: invocation})
	require.NoError(t, err)
	var spec map[string]any
	require.NoError(t, json.Unmarshal(compiled.ReportSpec, &spec))
	require.Equal(t, "delivery", spec["source"].(map[string]any)["containerId"])
	dataset := spec["datasets"].([]any)[0].(map[string]any)
	require.Equal(t, "delivery_daily", dataset["dataSourceRef"])
	require.Equal(t, float64(2659534), dataset["request"].(map[string]any)["filters"].(map[string]any)["adOrderId"].([]any)[0])
	require.NotEmpty(t, compiled.ReportFill)
	require.NotEmpty(t, compiled.ReportPrint)
	_, err = service.CompileFencedReport(context.Background(), &CompileFencedReportRequest{Content: validFencedReportContent(), Invocation: json.RawMessage(`{"unexpected":true}`)})
	require.ErrorContains(t, err, "unknown field")
}
