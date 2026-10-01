package reportdefinition

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func nativeAuthored(t *testing.T) []byte {
	t.Helper()
	base, _, found := strings.Cut(string(authored(t)), "sections:\n")
	require.True(t, found)
	return []byte(base + `report:
  id: operations-overview-demo
  title: Operations Overview Preview
  blocks:
    - {id: overview, kind: sectionBlock, title: Overview, navigationLabel: Overview}
    - id: evidence
      kind: tableBlock
      title: Daily operations
      datasetRef: operationsEvidence
      columns:
        - {key: region, label: Region}
        - {key: processedUnits, label: Processed Units}
    - {id: note, kind: markdownBlock, title: Note, markdown: Source data is live.}
`)
}

func TestNativeForgeReportCompilesWithoutMutatingDefinition(t *testing.T) {
	definition, err := ParseStrict([]byte(strings.Replace(string(nativeAuthored(t)), "kind: ReportDefinition", "kind: reporting.report", 1)))
	require.NoError(t, err)
	require.Nil(t, definition.Sections)
	authored, err := json.Marshal(definition.Report)
	require.NoError(t, err)
	_, err = CompileLayout(definition)
	require.NoError(t, err)
	rows := []map[string]any{{"region": "West", "processedUnits": 10, "operationsDate": "2026-09-01", "operatingCost": 5.5}}
	fetcher := DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		if request.DatasetID == "operationsEvidence" {
			return FetchResult{Rows: rows, HasMoreKnown: true}, nil
		}
		return FetchResult{Rows: []map[string]any{}, HasMoreKnown: true}, nil
	})
	preview, err := CompilePreview(context.Background(), definition, nil, fetcher, 10)
	require.NoError(t, err)
	require.True(t, preview.Complete)
	require.Contains(t, string(preview.ReportSpec), `"kind":"tableBlock"`)
	complete, err := Compile(context.Background(), definition, nil, fetcher)
	require.NoError(t, err)
	require.NotEmpty(t, complete.ReportPrint)
	unchanged, err := json.Marshal(definition.Report)
	require.NoError(t, err)
	require.JSONEq(t, string(authored), string(unchanged))
	_, err = CompilePreview(context.Background(), definition, nil, fetcher, 10)
	require.NoError(t, err)
}

func TestNativeForgeReportRejectsAmbiguousOrUnboundSources(t *testing.T) {
	base := string(nativeAuthored(t))
	tests := []struct{ name, body, message string }{
		{"native kind needs native blocks", strings.Replace(string(authored(t)), "kind: ReportDefinition", "kind: reporting.report", 1), "requires native report blocks"},
		{"both grammars", base + "sections: [{id: other, title: Other, blocks: []}]\n", "exactly one"},
		{"identity drift", strings.Replace(base, "id: operations-overview-demo\n  title: Operations Overview Preview\n  blocks:", "id: other\n  title: Operations Overview Preview\n  blocks:", 1), "must match metadata"},
		{"unknown dataset", strings.Replace(base, "datasetRef: operationsEvidence", "datasetRef: unknown", 1), "not a declared dataset"},
		{"implicit dataset", strings.Replace(base, "      datasetRef: operationsEvidence\n", "", 1), "explicit datasetRef"},
		{"unknown field", strings.Replace(base, "{key: region, label: Region}", "{key: unknown, label: Unknown}", 1), "unknown projected field"},
		{"unknown KPI value alias", strings.Replace(base, "    - {id: note, kind: markdownBlock, title: Note, markdown: Source data is live.}", "    - {id: note, kind: kpiBlock, title: Note, datasetRef: operationsEvidence, valueKey: unknown}", 1), "unknown projected field"},
		{"unknown report field", strings.Replace(base, "report:\n", "report:\n  script: reject\n", 1), "unsupported field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseStrict([]byte(tt.body))
			require.ErrorContains(t, err, tt.message)
		})
	}
}
