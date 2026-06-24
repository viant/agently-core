package reporting

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestForgeCSVExporter_ExportRendersCanonicalReportFill(t *testing.T) {
	exporter := NewForgeCSVExporter()
	request := &RenderRequest{
		JobID:       "job-1",
		ArtifactRef: "report://draft/performance",
		OwnerID:     "owner-1",
		Format:      ExportFormatCSV,
		Scope:       ExportScopeDraft,
		ReportFill:  json.RawMessage(validRenderableTestReportFillJSON()),
	}

	result, err := exporter.Export(context.Background(), request)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "text/csv", result.ContentType)
	require.Equal(t, "Channel,Spend\nDisplay,$42.50\nCTV,$30.00\n", string(result.Data))
}

func TestForgeCSVExporter_RejectsUnsupportedFormatsAndInvalidFill(t *testing.T) {
	exporter := NewForgeCSVExporter()

	_, err := exporter.Export(context.Background(), &RenderRequest{
		Format:      ExportFormatPDF,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.EqualError(t, err, `reporting forge csv export: unsupported format "pdf"`)

	_, err = exporter.Export(context.Background(), &RenderRequest{
		Format:     ExportFormatCSV,
		ReportFill: json.RawMessage(`{"version":1,"kind":"reportFill"}`),
	})
	require.ErrorContains(t, err, "reporting forge csv export:")
	require.ErrorContains(t, err, "reportFill.specVersion must be >= 1")
}
