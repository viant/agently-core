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

func TestForgePDFExporter_PassesOptionContextToSharedRenderer(t *testing.T) {
	// Compare option rendering, not the wall-clock second of each PDF build.
	created := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	request := &RenderRequest{
		Format:      ExportFormatPDF,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
		Metadata:    json.RawMessage(`{"reportOptions":[{"name":"exposurePerspective","label":"First vs. Last Exposure"}],"options":{"exposurePerspective":"First"}}`),
	}
	result, err := NewForgePDFExporter(&ForgePDFExporterOptions{CreationDate: created}).Export(context.Background(), request)
	require.NoError(t, err)
	report, err := reportprint.DecodeJSON(request.ReportPrint)
	require.NoError(t, err)
	expected, err := forgepdf.Render(report, forgepdf.Options{Metadata: request.Metadata, CreationDate: created})
	require.NoError(t, err)
	require.Equal(t, expected.Bytes, result.Data)
	without, err := forgepdf.Render(report, forgepdf.Options{CreationDate: created})
	require.NoError(t, err)
	require.NotEqual(t, without.Bytes, result.Data)
}

// Anonymous native-profile artifacts retain their exact source/hash bindings;
// existing Core validators already accept the known optional metadata. The
// actual strict Print decoder, not requireCanonicalFields, owns unknown fields.
func TestForgePDFExporter_NativeV1MetadataPreservesArtifactsAndIdentity(t *testing.T) {
	patch := func(raw string, edit func(map[string]interface{})) json.RawMessage {
		var value map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(raw), &value))
		edit(value)
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		return encoded
	}
	spec := patch(validTestReportSpecJSON(), func(value map[string]interface{}) {
		value["subtitle"] = "Synthetic native profile"
		value["layoutIntent"].(map[string]interface{})["items"] = []interface{}{map[string]interface{}{"blockId": "primaryTable", "size": "full"}}
	})
	fill := json.RawMessage(validTestReportFillJSON())
	print := patch(validRenderableTestReportPrintJSON(), func(value map[string]interface{}) { value["subtitle"] = "Synthetic native profile" })
	request := &SubmitExportRequest{ArtifactRef: "report://synthetic", Format: ExportFormatPDF, Scope: ExportScopeDraft, ReportSpec: spec, ReportFill: fill, ReportPrint: print}
	require.NoError(t, validateSubmitExportRequest(request))
	beforeSpec, beforeFill, beforePrint := string(spec), string(fill), string(print)
	result, err := NewForgePDFExporter(nil).Export(context.Background(), &RenderRequest{Format: ExportFormatPDF, ReportSpec: spec, ReportFill: fill, ReportPrint: print})
	require.NoError(t, err)
	require.NotEmpty(t, result.Data)
	require.Empty(t, result.Diagnostics)
	require.Equal(t, beforeSpec, string(request.ReportSpec))
	require.Equal(t, beforeFill, string(request.ReportFill))
	require.Equal(t, beforePrint, string(request.ReportPrint))
	// Existing source identity and required hash presence remain enforced.
	for _, edit := range []func(map[string]interface{}){
		func(value map[string]interface{}) { value["specHash"] = "" },
		func(value map[string]interface{}) {
			value["source"].(map[string]interface{})["dataSourceRef"] = "different"
		},
	} {
		invalid := *request
		invalid.ReportPrint = patch(string(print), edit)
		require.Error(t, validateSubmitExportRequest(&invalid))
	}
	for _, edit := range []func(map[string]interface{}){
		func(value map[string]interface{}) { value["extra"] = true },
		func(value map[string]interface{}) { value["subtitle"] = map[string]interface{}{"arbitrary": true} },
	} {
		_, err := reportprint.DecodeJSON(patch(string(print), edit))
		require.Error(t, err)
	}
}
