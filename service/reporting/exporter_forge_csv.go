package reporting

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	forgecsv "github.com/viant/forge/backend/reporting/export/csv"
	reportfill "github.com/viant/forge/backend/reporting/fill"
)

type ForgeCSVExporter struct{}

func NewForgeCSVExporter() Exporter {
	return &ForgeCSVExporter{}
}

func (e *ForgeCSVExporter) Export(_ context.Context, request *RenderRequest) (*RenderResult, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting forge csv export: request is required")
	}
	if request.Format != ExportFormatCSV {
		return nil, fmt.Errorf("reporting forge csv export: unsupported format %q", strings.TrimSpace(string(request.Format)))
	}
	if len(bytes.TrimSpace(request.ReportFill)) == 0 {
		return nil, fmt.Errorf("reporting forge csv export: reportFill is required")
	}
	report, err := reportfill.DecodeJSON(request.ReportFill)
	if err != nil {
		return nil, fmt.Errorf("reporting forge csv export: %w", err)
	}
	data, err := forgecsv.Render(report)
	if err != nil {
		return nil, fmt.Errorf("reporting forge csv export: %w", err)
	}
	return &RenderResult{
		ContentType: "text/csv",
		Data:        append([]byte{}, data...),
	}, nil
}
