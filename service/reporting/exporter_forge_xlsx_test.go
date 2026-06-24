package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
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
