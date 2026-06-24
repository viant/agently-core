package reporting

import (
	"context"
	"fmt"
	"strings"
)

type ForgeExporterOptions struct {
	PDF *ForgePDFExporterOptions
}

type ForgeExporter struct {
	pdf  Exporter
	csv  Exporter
	xlsx Exporter
}

func NewForgeExporter(options *ForgeExporterOptions) Exporter {
	if options == nil {
		options = &ForgeExporterOptions{}
	}
	return &ForgeExporter{
		pdf:  NewForgePDFExporter(options.PDF),
		csv:  NewForgeCSVExporter(),
		xlsx: NewForgeXLSXExporter(),
	}
}

func (e *ForgeExporter) Export(ctx context.Context, request *RenderRequest) (*RenderResult, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting forge export: request is required")
	}
	switch request.Format {
	case ExportFormatPDF:
		return e.pdf.Export(ctx, request)
	case ExportFormatCSV:
		return e.csv.Export(ctx, request)
	case ExportFormatXLSX:
		return e.xlsx.Export(ctx, request)
	default:
		return nil, fmt.Errorf("reporting forge export: unsupported format %q", strings.TrimSpace(string(request.Format)))
	}
}
