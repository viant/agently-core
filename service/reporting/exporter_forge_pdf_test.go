package reporting

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	forgepdf "github.com/viant/forge/backend/reporting/export/pdf"
	reportprint "github.com/viant/forge/backend/reporting/print"
)

func TestForgePDFExporter_ExportRendersCanonicalReportPrint(t *testing.T) {
	exporter := NewForgePDFExporter(&ForgePDFExporterOptions{
		CreationDate: time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC),
	})
	request := &RenderRequest{
		JobID:       "job-1",
		ArtifactRef: "report://draft/performance",
		OwnerID:     "owner-1",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	}

	result, err := exporter.Export(context.Background(), request)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "application/pdf", result.ContentType)
	require.NotEmpty(t, result.Data)
	require.Nil(t, result.Diagnostics)

	report, err := reportprint.DecodeJSON([]byte(validRenderableTestReportPrintJSON()))
	require.NoError(t, err)
	expected, err := forgepdf.Render(report, forgepdf.Options{
		CreationDate: time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, expected.Bytes, result.Data)
}

func TestForgePDFExporter_RejectsUnsupportedFormatsAndInvalidPrints(t *testing.T) {
	exporter := NewForgePDFExporter(nil)

	_, err := exporter.Export(context.Background(), &RenderRequest{
		Format:     ExportFormatCSV,
		ReportFill: json.RawMessage(validTestReportFillJSON()),
	})
	require.EqualError(t, err, `reporting forge pdf export: unsupported format "csv"`)

	_, err = exporter.Export(context.Background(), &RenderRequest{
		Format:      ExportFormatPDF,
		ReportPrint: json.RawMessage(`{"version":1,"kind":"reportPrint"}`),
	})
	require.ErrorContains(t, err, "reporting forge pdf export:")
	require.ErrorContains(t, err, "reportPrint.specVersion must be >= 1")
}
