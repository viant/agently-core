package reportdefinition

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	reportfill "github.com/viant/forge/backend/reporting/fill"
)

func previewDefinition(t *testing.T) *Definition {
	t.Helper()
	definition, err := ParseStrict(authored(t))
	require.NoError(t, err)
	return definition
}

func TestCompilePreviewNarrowsEveryPageAndMarksRenderablePartialData(t *testing.T) {
	definition := previewDefinition(t)
	var pages []Page
	preview, err := CompilePreview(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		require.NotNil(t, request.Query.Page)
		if request.DatasetID != "operationsByDate" {
			return FetchResult{Rows: []map[string]any{}, HasMoreKnown: true}, nil
		}
		pages = append(pages, *request.Query.Page)
		rows := make([]map[string]any, request.Query.Page.Limit)
		for i := range rows {
			rows[i] = map[string]any{"operationsDate": "2026-09-01", "processedUnits": 10}
		}
		return FetchResult{Rows: rows, HasMore: true, HasMoreKnown: true}, nil
	}), 3)
	require.NoError(t, err)
	require.Equal(t, []Page{{Limit: 2, Offset: 0}, {Limit: 1, Offset: 2}}, pages)
	require.Equal(t, "reportPreview", preview.Kind)
	require.False(t, preview.Complete)
	require.Equal(t, PreviewDataset{RowCount: 3, Reason: "rowCap"}, preview.Datasets["operationsByDate"])
	require.True(t, preview.Datasets["targets"].Complete)
	for _, raw := range []json.RawMessage{preview.Source, preview.ReportDocument, preview.ReportSpec, preview.ReportFill} {
		require.True(t, json.Valid(raw))
	}
	fill, err := reportfill.DecodeJSON(preview.ReportFill)
	require.NoError(t, err)
	foundRows, foundBlock := false, false
	for _, dataset := range fill.Datasets {
		if dataset.ID == "operationsByDate" {
			foundRows = true
			require.Len(t, dataset.Rows, 3)
			require.True(t, dataset.Provenance.Truncated)
			require.True(t, dataset.Provenance.HasMore)
		}
	}
	for _, block := range fill.Blocks {
		if block.ID == "operationsTrend" {
			foundBlock = true
			require.Equal(t, "operationsByDate", block.DatasetRef)
			require.NotEmpty(t, block.ChartModel)
		}
	}
	require.True(t, foundRows)
	require.True(t, foundBlock)
	encoded, err := json.Marshal(preview)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "reportPrint")
}

func TestCompilePreviewPreservesOffsetAndNarrowsAuthoredLimit(t *testing.T) {
	definition := previewDefinition(t)
	targets := definition.Datasets["targets"]
	targets.Query.Page.Offset = 7
	definition.Datasets["targets"] = targets
	var pages []Page
	preview, err := CompilePreview(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		if request.DatasetID != "targets" {
			return FetchResult{HasMoreKnown: true}, nil
		}
		pages = append(pages, *request.Query.Page)
		rows := make([]map[string]any, request.Query.Page.Limit)
		for i := range rows {
			rows[i] = map[string]any{"project": "Launch", "targetUnits": 40}
		}
		return FetchResult{Rows: rows, HasMore: true, HasMoreKnown: true}, nil
	}), 2)
	require.NoError(t, err)
	require.Equal(t, []Page{{Limit: 2, Offset: 7}}, pages)
	require.Equal(t, 7, definition.Datasets["targets"].Query.Page.Offset)
	require.Equal(t, 25, definition.Datasets["targets"].Query.Page.Limit)
	require.Equal(t, PreviewDataset{RowCount: 2, Reason: "rowCap"}, preview.Datasets["targets"])
}

func TestCompilePreviewIntroducesPageForUnpagedDataset(t *testing.T) {
	definition := previewDefinition(t)
	var summaryPage Page
	preview, err := CompilePreview(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		if request.DatasetID == "operationsSummary" {
			summaryPage = *request.Query.Page
			rows := make([]map[string]any, request.Query.Page.Limit)
			for i := range rows {
				rows[i] = map[string]any{"processedUnits": 10, "operatingCost": 2.5}
			}
			return FetchResult{Rows: rows, HasMore: true, HasMoreKnown: true}, nil
		}
		return FetchResult{HasMoreKnown: true}, nil
	}), 200)
	require.NoError(t, err)
	require.Equal(t, Page{Limit: 200, Offset: 0}, summaryPage)
	require.Equal(t, PreviewDataset{RowCount: 200, Reason: "rowCap"}, preview.Datasets["operationsSummary"])
	require.Nil(t, definition.Datasets["operationsSummary"].Query.Page)
}

func TestCompilePreviewTreatsConfirmedEndAtCapAsComplete(t *testing.T) {
	definition := previewDefinition(t)
	calls := 0
	preview, err := CompilePreview(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		if request.DatasetID == "operationsSummary" {
			calls++
			return FetchResult{Rows: []map[string]any{{"processedUnits": 10, "operatingCost": 2.5}}, HasMoreKnown: true}, nil
		}
		return FetchResult{HasMoreKnown: true}, nil
	}), 1)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.True(t, preview.Complete)
	require.Equal(t, PreviewDataset{RowCount: 1, Complete: true}, preview.Datasets["operationsSummary"])
	fill, err := reportfill.DecodeJSON(preview.ReportFill)
	require.NoError(t, err)
	for _, dataset := range fill.Datasets {
		require.False(t, dataset.Provenance.Truncated)
	}
}

func TestCompilePreviewCompletenessRequiresExplicitSignal(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		known        bool
		complete     bool
	}{
		{name: "known end", known: true, complete: true},
		{name: "missing signal", reason: "completenessUnknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definition := previewDefinition(t)
			calls := 0
			preview, err := CompilePreview(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
				calls++
				return FetchResult{HasMoreKnown: tc.known}, nil
			}), 2)
			require.NoError(t, err)
			require.Equal(t, len(definition.Datasets), calls)
			require.Equal(t, tc.complete, preview.Complete)
			for _, state := range preview.Datasets {
				require.Equal(t, tc.complete, state.Complete)
				require.Equal(t, tc.reason, state.Reason)
			}
			fill, err := reportfill.DecodeJSON(preview.ReportFill)
			require.NoError(t, err)
			for _, dataset := range fill.Datasets {
				require.Equal(t, !tc.complete, dataset.Provenance.Truncated)
				require.False(t, dataset.Provenance.HasMore)
			}
			encoded, err := json.Marshal(preview)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "reportPrint")
		})
	}
}

func TestCompilePreviewRejectsInvalidCapAndFetcherViolations(t *testing.T) {
	definition := previewDefinition(t)
	called := 0
	fetcher := DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		called++
		return FetchResult{Rows: make([]map[string]any, request.Query.Page.Limit+1)}, nil
	})
	for _, cap := range []int{0, -1, maxRows + 1} {
		_, err := CompilePreview(context.Background(), definition, nil, fetcher, cap)
		require.ErrorContains(t, err, "preview row cap")
	}
	require.Zero(t, called)
	_, err := CompilePreview(context.Background(), definition, nil, fetcher, 1)
	require.ErrorContains(t, err, "above page limit")
	require.Equal(t, 1, called)
	_, err = CompilePreview(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		return FetchResult{HasMore: true, HasMoreKnown: true}, nil
	}), 1)
	require.ErrorContains(t, err, "without progress")
	_, err = CompilePreview(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		return FetchResult{Rows: []map[string]any{{"processedUnits": 10, "operatingCost": 2.5}}, HasMore: true}, nil
	}), 2)
	require.ErrorContains(t, err, "without an explicit completeness signal")
}

func TestCompilePreviewStopsAtPaginationBound(t *testing.T) {
	definition := previewDefinition(t)
	targets := definition.Datasets["targets"]
	targets.Query.Page.Offset = maxRows - 1
	definition.Datasets["targets"] = targets
	var page Page
	preview, err := CompilePreview(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		if request.DatasetID != "targets" {
			return FetchResult{HasMoreKnown: true}, nil
		}
		page = *request.Query.Page
		return FetchResult{Rows: []map[string]any{{"project": "Launch", "targetUnits": 40}}, HasMore: true, HasMoreKnown: true}, nil
	}), 3)
	require.NoError(t, err)
	require.Equal(t, Page{Limit: 1, Offset: maxRows - 1}, page)
	require.Equal(t, PreviewDataset{RowCount: 1, Reason: "paginationBound"}, preview.Datasets["targets"])
}
