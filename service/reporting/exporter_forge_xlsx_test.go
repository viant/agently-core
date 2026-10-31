package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/forge/backend/reporting/materializer"
	"github.com/xuri/excelize/v2"
)

func TestForgeXLSXExporter_ExportRendersCanonicalReportFill(t *testing.T) {
	exporter := NewForgeXLSXExporter()
	request := &RenderRequest{
		JobID:       "job-1",
		ArtifactRef: "report://draft/performance",
		OwnerID:     "owner-1",
		Format:      ExportFormatXLSX,
		Scope:       ExportScopeDraft,
		ReportFill:  json.RawMessage(validRenderableTestReportFillJSON()),
	}

	result, err := exporter.Export(context.Background(), request)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", result.ContentType)
	require.NotEmpty(t, result.Data)

	workbook, err := excelize.OpenReader(bytes.NewReader(result.Data))
	require.NoError(t, err)
	value, err := workbook.GetCellValue("Report", "A2")
	require.NoError(t, err)
	require.Equal(t, "Display", value)
	value, err = workbook.GetCellValue("Report", "B2")
	require.NoError(t, err)
	require.Equal(t, "$42.50", value)
}

func TestForgeXLSXExporter_RejectsUnsupportedFormatsAndInvalidFill(t *testing.T) {
	exporter := NewForgeXLSXExporter()

	_, err := exporter.Export(context.Background(), &RenderRequest{
		Format:     ExportFormatCSV,
		ReportFill: json.RawMessage(validRenderableTestReportFillJSON()),
	})
	require.EqualError(t, err, `reporting forge xlsx export: unsupported format "csv"`)

	_, err = exporter.Export(context.Background(), &RenderRequest{
		Format:     ExportFormatXLSX,
		ReportFill: json.RawMessage(`{"version":1,"kind":"reportFill"}`),
	})
	require.ErrorContains(t, err, "reporting forge xlsx export:")
	require.ErrorContains(t, err, "reportFill.specVersion must be >= 1")
}

func TestForgeXLSXExporterRendersMultipleTablesAsWorkbook(t *testing.T) {
	var fill map[string]any
	require.NoError(t, json.Unmarshal([]byte(validRenderableTestReportFillJSON()), &fill))
	blocks := fill["blocks"].([]any)
	second := map[string]any{}
	for key, value := range blocks[0].(map[string]any) {
		second[key] = value
	}
	second["id"] = "secondTable"
	second["title"] = "Second table"
	fill["blocks"] = append(blocks, second)
	raw, err := json.Marshal(fill)
	require.NoError(t, err)
	result, err := NewForgeXLSXExporter().Export(context.Background(), &RenderRequest{Format: ExportFormatXLSX, ReportFill: raw})
	require.NoError(t, err)
	book, err := excelize.OpenReader(bytes.NewReader(result.Data))
	require.NoError(t, err)
	defer book.Close()
	require.Contains(t, book.GetSheetList(), "Dashboard")
	require.Greater(t, len(book.GetSheetList()), 1)
}

func TestForgeXLSXExporterPreservesCanonicalConditionalPresentation(t *testing.T) {
	var spec map[string]any
	require.NoError(t, json.Unmarshal([]byte(validTestReportSpecJSON()), &spec))
	spec["scope"] = map[string]any{"dataSourceRef": "demo", "params": []any{map[string]any{"id": "mode", "value": "visible"}}}
	blocks := []any{}
	for _, mode := range []string{"visible", "hidden"} {
		blocks = append(blocks, map[string]any{"id": mode, "kind": "tableBlock", "title": mode, "datasetRef": "primary", "columns": []any{map[string]any{"key": "value", "label": "Value"}}, "runtime": map[string]any{"visibleWhen": map[string]any{"source": "filters", "field": "mode", "equals": mode}}})
	}
	spec["datasets"].([]any)[0].(map[string]any)["request"] = map[string]any{"limit": 25, "offset": 0}
	spec["blocks"] = blocks
	spec["layoutIntent"] = map[string]any{"kind": "stack", "blockOrder": []string{"visible", "hidden"}}
	raw, err := json.Marshal(spec)
	require.NoError(t, err)
	artifacts, err := materializer.Materialize(context.Background(), raw, map[string]json.RawMessage{"primary": json.RawMessage(`{"rows":[{"value":7}]}`)})
	require.NoError(t, err)
	for _, native := range []bool{false, true} {
		request := &RenderRequest{Format: ExportFormatXLSX, ReportFill: artifacts.ReportFill, ReportPrint: artifacts.ReportPrint, resourceDatasourceRequests: native}
		result, err := NewForgeXLSXExporter().Export(context.Background(), request)
		require.NoError(t, err)
		book, err := excelize.OpenReader(bytes.NewReader(result.Data))
		require.NoError(t, err)
		rows, err := book.GetRows("Report")
		require.NoError(t, err)
		cells, err := json.Marshal(rows)
		require.NoError(t, err)
		require.Contains(t, string(cells), `"visible"`)
		require.NotContains(t, string(cells), `"hidden"`)
		require.NoError(t, book.Close())
		var broken map[string]any
		require.NoError(t, json.Unmarshal(artifacts.ReportPrint, &broken))
		broken["fillHash"] = "fnv1a:00000000"
		request.ReportPrint, err = json.Marshal(broken)
		require.NoError(t, err)
		_, err = NewForgeXLSXExporter().Export(context.Background(), request)
		require.ErrorContains(t, err, "does not match reportFill")
	}
}
