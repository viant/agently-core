package reportdefinition

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func authored(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/report.yaml")
	require.NoError(t, err)
	return body
}

func TestCompileAuthoredYAMLWithInjectedFetcher(t *testing.T) {
	definition, err := Parse(authored(t))
	require.NoError(t, err)
	called := map[string]int{}
	fetcher := DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		called[request.DatasetID]++
		require.Equal(t, "reports", request.DataSource.Service.Endpoint)
		if request.DataSourceID == "operations" {
			require.NotNil(t, request.Query.Filter)
			require.Len(t, request.Query.Filter.And, 2)
		}
		switch request.DatasetID {
		case "operationsSummary":
			return FetchResult{Rows: []map[string]any{{"processedUnits": 30, "operatingCost": 13.5}}}, nil
		case "operationsByDate":
			if request.Query.Page.Offset == 0 {
				return FetchResult{Rows: []map[string]any{{"operationsDate": "2026-09-01", "processedUnits": 10}}, HasMore: true}, nil
			}
			require.Equal(t, 1, request.Query.Page.Offset)
			return FetchResult{Rows: []map[string]any{{"operationsDate": "2026-09-02", "processedUnits": 20}}}, nil
		case "operationsEvidence":
			return FetchResult{Rows: []map[string]any{{"operationsDate": "2026-09-01", "region": "West", "processedUnits": 10, "operatingCost": 5.5}}}, nil
		case "targets":
			return FetchResult{Rows: []map[string]any{{"project": "Launch", "targetUnits": 40}}}, nil
		default:
			return FetchResult{}, errors.New("unexpected dataset")
		}
	})
	result, err := Compile(context.Background(), definition, map[string]any{"from": "2026-09-01", "region": []any{"West"}}, fetcher)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"operationsSummary": 1, "operationsByDate": 2, "operationsEvidence": 1, "targets": 1}, called)
	for _, raw := range []json.RawMessage{result.Source, result.ReportDocument, result.ReportSpec, result.ReportFill, result.ReportPrint} {
		require.True(t, json.Valid(raw))
	}
	require.NotContains(t, string(result.Source), "x-mock-preview")
	var document struct {
		Kind   string `json:"kind"`
		ID     string `json:"id"`
		Blocks []struct {
			ID         string `json:"id"`
			Kind       string `json:"kind"`
			DatasetRef string `json:"datasetRef"`
		} `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal(result.ReportDocument, &document))
	require.Equal(t, "reportDocument", document.Kind)
	require.Equal(t, "operations-overview-demo", document.ID)
	require.Len(t, document.Blocks, 7)
	require.Equal(t, "kpiBlock", document.Blocks[1].Kind)
	require.Equal(t, "operationsSummary", document.Blocks[1].DatasetRef)
	var fill struct {
		Datasets []struct {
			ID   string           `json:"id"`
			Rows []map[string]any `json:"rows"`
		} `json:"datasets"`
	}
	require.NoError(t, json.Unmarshal(result.ReportFill, &fill))
	for _, dataset := range fill.Datasets {
		if dataset.ID == "operationsByDate" {
			require.Len(t, dataset.Rows, 2)
			return
		}
	}
	t.Fatal("operationsByDate missing from canonical ReportFill")
}

func TestOriginalPreviewDemoGrammar(t *testing.T) {
	path := filepath.Join("..", "..", "..", "agently", "preview", "report", "examples", "demo", "report.yaml")
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("sibling agently preview checkout is unavailable")
	}
	require.NoError(t, err)
	definition, err := Parse(body)
	require.NoError(t, err)
	called := 0
	result, err := Compile(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		called++
		require.NotEmpty(t, request.Endpoint.Type)
		return FetchResult{Rows: []map[string]any{}}, nil
	}))
	require.NoError(t, err)
	require.Equal(t, len(definition.Datasets), called)
	require.Equal(t, "operations-overview-demo", definition.Metadata.ID)
	require.NotEmpty(t, result.ReportDocument)
}

func TestValidateBeforeFetch(t *testing.T) {
	tests := []struct{ name, old, replacement, message string }{
		{"unknown datasource", "dataSource: projectTargets", "dataSource: missing", "unknown dataSource"},
		{"unknown projected field", "dimensions: [operationsDate, region]", "dimensions: [operationsDate, missing]", "projection"},
		{"unknown block dataset", "dataset: targets", "dataset: missing", "unknown dataset"},
		{"unknown block field", "columns: [project, targetUnits]", "columns: [project, missing]", "unknown column"},
		{"bad page", "limit: 25, offset: 0", "limit: 1001, offset: 0", "page limit"},
		{"unknown binding", "parameter: from", "parameter: missing", "parameterBindings"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.Replace(string(authored(t)), tt.old, tt.replacement, 1)
			_, err := Parse([]byte(body))
			require.ErrorContains(t, err, tt.message)
		})
	}
}

func TestParseIgnoresPreviewExtensionAndRejectsUnknownFields(t *testing.T) {
	body := append(authored(t), []byte("\nx-mock-preview:\n  version: 1\n  dataSources:\n    operations: {file: operations.json}\n")...)
	definition, err := Parse(body)
	require.NoError(t, err)
	require.Equal(t, "operations-overview-demo", definition.Metadata.ID)
	_, err = ParseStrict(body)
	require.ErrorContains(t, err, "x-mock-preview is not allowed")
	_, err = Parse(append(authored(t), []byte("\nunknownField: true\n")...))
	require.ErrorContains(t, err, "unknownField")
	_, err = Parse(append(authored(t), []byte("\nkind: ReportDefinition\n")...))
	require.ErrorContains(t, err, "duplicate YAML key")
}

func TestCompileRejectsIncompleteOrInvalidFetchedData(t *testing.T) {
	definition, err := Parse(authored(t))
	require.NoError(t, err)
	_, err = Compile(context.Background(), definition, nil, nil)
	require.ErrorContains(t, err, "fetcher")
	_, err = Compile(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		if request.DatasetID == "operationsByDate" {
			return FetchResult{HasMore: true}, nil
		}
		return FetchResult{}, nil
	}))
	require.ErrorContains(t, err, "without progress")
	_, err = Compile(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		if request.DatasetID == "operationsByDate" {
			return FetchResult{Rows: []map[string]any{{"missing": 1}}}, nil
		}
		return FetchResult{}, nil
	}))
	require.ErrorContains(t, err, "missing projected field")
}

func TestDefinitionRequiresPositiveRevisionAndBoundedEncodedRows(t *testing.T) {
	zeroRevision := strings.Replace(string(authored(t)), "revision: 1", "revision: 0", 1)
	if _, err := Parse([]byte(zeroRevision)); err == nil || !strings.Contains(err.Error(), "positive revision") {
		t.Fatalf("zero report revision accepted: %v", err)
	}
	definition, err := Parse(authored(t))
	require.NoError(t, err)
	_, err = Compile(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		if request.DatasetID == "operationsByDate" {
			return FetchResult{Rows: []map[string]any{{"operationsDate": strings.Repeat("x", maxDatasetBytes), "processedUnits": 10}}}, nil
		}
		return FetchResult{Rows: []map[string]any{}}, nil
	}))
	require.ErrorContains(t, err, "encoded data budget")
}

func TestQueryJSONKeepsAuthoredFieldNamesForMCPFetcher(t *testing.T) {
	definition, err := Parse(authored(t))
	require.NoError(t, err)
	query := definition.Datasets["operationsByDate"].Query
	encoded, err := json.Marshal(query)
	require.NoError(t, err)
	var value map[string]any
	require.NoError(t, json.Unmarshal(encoded, &value))
	require.Contains(t, value, "projection")
	require.Contains(t, value, "orderBy")
	require.Contains(t, value, "page")
	require.NotContains(t, value, "Projection")
}

func TestCompileLayoutDoesNotFetchData(t *testing.T) {
	definition, err := ParseStrict(authored(t))
	require.NoError(t, err)
	compiled, err := CompileLayout(definition)
	require.NoError(t, err)
	require.NotEmpty(t, compiled.ReportDocument)
	require.NotEmpty(t, compiled.ReportSpec)
	require.NotEmpty(t, compiled.ReportFill)
}
