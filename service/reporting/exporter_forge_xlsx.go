package reporting

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	forgexlsx "github.com/viant/forge/backend/reporting/export/xlsx"
	reportfill "github.com/viant/forge/backend/reporting/fill"
)

type ForgeXLSXExporter struct{}

func NewForgeXLSXExporter() Exporter {
	return &ForgeXLSXExporter{}
}

func (e *ForgeXLSXExporter) Export(_ context.Context, request *RenderRequest) (*RenderResult, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting forge xlsx export: request is required")
	}
	if request.Format != ExportFormatXLSX {
		return nil, fmt.Errorf("reporting forge xlsx export: unsupported format %q", strings.TrimSpace(string(request.Format)))
	}
	if len(bytes.TrimSpace(request.ReportFill)) == 0 {
		return nil, fmt.Errorf("reporting forge xlsx export: reportFill is required")
	}
	report, err := reportfill.DecodeJSON(request.ReportFill)
	if err != nil {
		return nil, fmt.Errorf("reporting forge xlsx export: %w", err)
	}
	data, err := forgexlsx.Render(report)
	if err != nil {
		return nil, fmt.Errorf("reporting forge xlsx export: %w", err)
	}
	return &RenderResult{
		ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		Data:        append([]byte{}, data...),
	}, nil
}
