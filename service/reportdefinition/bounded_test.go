package reportdefinition

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	reportfill "github.com/viant/forge/backend/reporting/fill"
)

func TestCompileBoundedExactCapProducesCompleteForgeArtifacts(t *testing.T) {
	definition := previewDefinition(t)
	var trendPages []Page
	var summaryPage Page
	result, err := CompileBounded(context.Background(), definition, map[string]any{"from": "2026-09-01"}, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		require.NotNil(t, request.Query.Page)
		require.Greater(t, request.Query.Page.Limit, 0)
		require.LessOrEqual(t, request.Query.Page.Limit, 3)
		switch request.DatasetID {
		case "operationsByDate":
			require.NotNil(t, request.Query.Filter)
			trendPages = append(trendPages, *request.Query.Page)
			rows := make([]map[string]any, request.Query.Page.Limit)
			for i := range rows {
				rows[i] = map[string]any{"operationsDate": "2026-09-01", "processedUnits": 10}
			}
			return FetchResult{Rows: rows, HasMore: len(trendPages) == 1, HasMoreKnown: true}, nil
		case "operationsSummary":
			summaryPage = *request.Query.Page
			return FetchResult{Rows: []map[string]any{{"processedUnits": 30, "operatingCost": 13.5}}, HasMoreKnown: true}, nil
		default:
			return FetchResult{HasMoreKnown: true}, nil
		}
	}), 3)
	require.NoError(t, err)
	require.Equal(t, []Page{{Limit: 2, Offset: 0}, {Limit: 1, Offset: 2}}, trendPages)
	require.Equal(t, Page{Limit: 3, Offset: 0}, summaryPage)
	require.Equal(t, Page{Limit: 2, Offset: 0}, *definition.Datasets["operationsByDate"].Query.Page)
	require.Nil(t, definition.Datasets["operationsSummary"].Query.Page)
	for _, artifact := range []json.RawMessage{result.Source, result.ReportDocument, result.ReportSpec, result.ReportFill, result.ReportPrint} {
		require.True(t, json.Valid(artifact))
		require.NotEmpty(t, artifact)
	}
	fill, err := reportfill.DecodeJSON(result.ReportFill)
	require.NoError(t, err)
	found := false
	for _, dataset := range fill.Datasets {
		require.False(t, dataset.Provenance.Truncated)
		if dataset.ID == "operationsByDate" {
			found = true
			require.Len(t, dataset.Rows, 3)
		}
	}
	require.True(t, found)
}

func TestCompileBoundedOverCapReturnsNoArtifactsOrExtraFetches(t *testing.T) {
	definition := previewDefinition(t)
	var pages []Page
	result, err := CompileBounded(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		require.Equal(t, "operationsByDate", request.DatasetID)
		require.NotNil(t, request.Query.Page)
		pages = append(pages, *request.Query.Page)
		rows := make([]map[string]any, request.Query.Page.Limit)
		for i := range rows {
			rows[i] = map[string]any{"operationsDate": "2026-09-01", "processedUnits": 10}
		}
		return FetchResult{Rows: rows, HasMore: true, HasMoreKnown: true}, nil
	}), 3)
	require.Nil(t, result)
	require.ErrorContains(t, err, "exceeds bounded row cap 3")
	require.Equal(t, []Page{{Limit: 2, Offset: 0}, {Limit: 1, Offset: 2}}, pages)
}

func TestCompileBoundedRejectsUnknownCompleteness(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hasMore bool
	}{
		{name: "unknown end"},
		{name: "unknown continuation", hasMore: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definition := previewDefinition(t)
			calls := 0
			result, err := CompileBounded(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
				calls++
				require.NotNil(t, request.Query.Page)
				return FetchResult{HasMore: tc.hasMore}, nil
			}), 1)
			require.Nil(t, result)
			require.ErrorContains(t, err, "unknown completeness")
			require.Equal(t, 1, calls)
		})
	}
}

func TestCompileBoundedPreservesOffsetAndRejectsInvalidLimits(t *testing.T) {
	definition := previewDefinition(t)
	targets := definition.Datasets["targets"]
	targets.Query.Page.Offset = 7
	definition.Datasets["targets"] = targets
	var targetPage Page
	result, err := CompileBounded(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		if request.DatasetID == "targets" {
			targetPage = *request.Query.Page
			return FetchResult{Rows: []map[string]any{{"project": "Launch", "targetUnits": 40}, {"project": "Expand", "targetUnits": 50}}, HasMoreKnown: true}, nil
		}
		return FetchResult{HasMoreKnown: true}, nil
	}), 2)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, Page{Limit: 2, Offset: 7}, targetPage)
	require.Equal(t, 7, definition.Datasets["targets"].Query.Page.Offset)

	called := 0
	fetcher := DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		called++
		return FetchResult{Rows: make([]map[string]any, request.Query.Page.Limit+1), HasMoreKnown: true}, nil
	})
	for _, cap := range []int{0, -1, maxRows + 1} {
		result, err := CompileBounded(context.Background(), definition, nil, fetcher, cap)
		require.Nil(t, result)
		require.ErrorContains(t, err, "bounded row cap")
	}
	require.Zero(t, called)
	result, err = CompileBounded(context.Background(), definition, nil, fetcher, 1)
	require.Nil(t, result)
	require.ErrorContains(t, err, "above page limit")
	require.Equal(t, 1, called)
}
