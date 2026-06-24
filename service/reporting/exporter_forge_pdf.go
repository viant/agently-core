package reporting

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	forgepdf "github.com/viant/forge/backend/reporting/export/pdf"
	reportprint "github.com/viant/forge/backend/reporting/print"
)

// ForgePDFExporterOptions configures the canonical Forge-backed PDF exporter.
type ForgePDFExporterOptions struct {
	CreationDate time.Time
}

// ForgePDFExporter renders canonical ReportPrint payloads into deterministic
// PDF bytes using Forge's Go export engine.
type ForgePDFExporter struct {
	options ForgePDFExporterOptions
}

// NewForgePDFExporter constructs a Forge-backed PDF exporter.
func NewForgePDFExporter(options *ForgePDFExporterOptions) Exporter {
	if options == nil {
		options = &ForgePDFExporterOptions{}
	}
	return &ForgePDFExporter{
		options: *options,
	}
}

// Export renders a canonical ReportPrint payload into PDF bytes.
func (e *ForgePDFExporter) Export(_ context.Context, request *RenderRequest) (*RenderResult, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting forge pdf export: request is required")
	}
	if request.Format != ExportFormatPDF {
		return nil, fmt.Errorf("reporting forge pdf export: unsupported format %q", strings.TrimSpace(string(request.Format)))
	}
	if len(bytes.TrimSpace(request.ReportPrint)) == 0 {
		return nil, fmt.Errorf("reporting forge pdf export: reportPrint is required")
	}
	report, err := reportprint.DecodeJSON(request.ReportPrint)
	if err != nil {
		return nil, fmt.Errorf("reporting forge pdf export: %w", err)
	}
	result, err := forgepdf.Render(report, forgepdf.Options{
		CreationDate: e.options.CreationDate,
	})
	if err != nil {
		return nil, fmt.Errorf("reporting forge pdf export: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("reporting forge pdf export: renderer returned nil result")
	}
	return &RenderResult{
		ContentType: "application/pdf",
		Data:        append([]byte{}, result.Bytes...),
		Diagnostics: translateForgePDFDiagnostics(result.Diagnostics),
	}, nil
}

func translateForgePDFDiagnostics(input []forgepdf.RenderDiagnostic) []Diagnostic {
	if len(input) == 0 {
		return nil
	}
	result := make([]Diagnostic, 0, len(input))
	for _, diagnostic := range input {
		result = append(result, Diagnostic{
			Code:         strings.TrimSpace(diagnostic.Code),
			Severity:     strings.TrimSpace(diagnostic.Severity),
			Path:         strings.TrimSpace(diagnostic.Path),
			Message:      strings.TrimSpace(diagnostic.Message),
			SuggestedFix: strings.TrimSpace(diagnostic.SuggestedFix),
		})
	}
	return result
}
