package reporting

import (
	"context"
	"fmt"
	"strings"

	forgefenced "github.com/viant/forge/backend/reporting/fenced"
)

// CompileFencedReport compiles progressive Forge fences into an export-ready
// canonical reporting envelope.
func (s *Service) CompileFencedReport(_ context.Context, request *CompileFencedReportRequest) (*CompileFencedReportResult, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting fenced compile: request is required")
	}
	fences := make([]forgefenced.Fence, 0, len(request.Fences))
	for _, fence := range request.Fences {
		fences = append(fences, forgefenced.Fence{
			Kind: strings.TrimSpace(fence.Kind), Index: fence.Index, Payload: cloneJSON(fence.Payload),
		})
	}
	compiled, err := forgefenced.Compile(&forgefenced.CompileRequest{
		Content: request.Content, Fences: fences, ReportID: strings.TrimSpace(request.ReportID),
	})
	if err != nil {
		return nil, fmt.Errorf("reporting fenced compile: %w", err)
	}
	if compiled == nil || compiled.Assembly == nil {
		return nil, fmt.Errorf("reporting fenced compile: compiler returned no report assembly")
	}
	format := request.Format
	if format == "" {
		format = ExportFormatPDF
	}
	if format != ExportFormatPDF && format != ExportFormatCSV && format != ExportFormatXLSX {
		return nil, fmt.Errorf("reporting fenced compile: unsupported format %q", format)
	}
	reportID := strings.TrimSpace(compiled.Assembly.ID)
	title := jsonObjectText(compiled.ReportSpec, "title")
	if title == "" {
		title = reportID
	}
	envelope := &ReportExportRequest{
		Version: 1,
		Kind:    "reportExportRequest",
		Target:  ReportExportTarget{Format: format},
		Source: ReportExportSource{
			From: "draft", ArtifactKind: "dashboard.reportBuilder",
			ArtifactRef: "dashboard.reportBuilder://" + reportID,
			Title:       title, ReportID: reportID,
		},
		ReportSpec: cloneJSON(compiled.ReportSpec), ReportFill: cloneJSON(compiled.ReportFill),
		ReportPrint: cloneJSON(compiled.ReportPrint),
	}
	if normalized, err := normalizeSubmitExportRequest(&SubmitExportRequest{ReportExportRequest: envelope}); err != nil {
		return nil, fmt.Errorf("reporting fenced compile produced invalid export request: %w", err)
	} else if err := validateSubmitExportRequest(normalized); err != nil {
		return nil, fmt.Errorf("reporting fenced compile produced invalid export request: %w", err)
	}
	diagnostics := make([]Diagnostic, 0, len(compiled.Diagnostics))
	for _, item := range compiled.Diagnostics {
		diagnostics = append(diagnostics, Diagnostic{
			Code: item.Code, Severity: item.Severity, Path: item.Path,
			Message: item.Message, SuggestedFix: item.SuggestedFix,
		})
	}
	return &CompileFencedReportResult{
		ReportID: reportID, ReportDocument: cloneJSON(compiled.ReportDocument),
		ReportSpec: cloneJSON(compiled.ReportSpec), ReportFill: cloneJSON(compiled.ReportFill),
		ReportPrint: cloneJSON(compiled.ReportPrint), ReportExportRequest: envelope,
		Diagnostics: diagnostics,
	}, nil
}

func (s *Service) compileFencedReportTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*CompileFencedReportRequest)
	if !ok {
		return fmt.Errorf("invalid reporting fenced compile input %T", in)
	}
	output, ok := out.(*CompileFencedReportResult)
	if !ok {
		return fmt.Errorf("invalid reporting fenced compile output %T", out)
	}
	result, err := s.CompileFencedReport(ctx, input)
	if err != nil {
		return err
	}
	*output = *result
	return nil
}
